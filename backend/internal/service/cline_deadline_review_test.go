//go:build unit

package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/cline"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestClineDeadlinePreemptsDrainEvenAfterEarlyCancel(t *testing.T) {
	parent, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	deadline, _ := parent.Deadline()
	life := newClineRequestLifetime(parent, time.Second, 2*time.Second)
	defer life.close()
	got, ok := life.ctx.Deadline()
	require.True(t, ok)
	require.True(t, got.Equal(deadline))
	cancel() // This stops the parent's deadline timer, not our preserved deadline.
	select {
	case <-life.ctx.Done():
		require.ErrorIs(t, context.Cause(life.ctx), ErrClineCallerDeadline)
		require.ErrorIs(t, context.Cause(life.ctx), context.DeadlineExceeded)
		require.ErrorIs(t, life.ctx.Err(), context.DeadlineExceeded)
		require.NotErrorIs(t, context.Cause(life.ctx), context.Canceled)
	case <-time.After(800 * time.Millisecond):
		t.Fatal("parent deadline was extended by the disconnect grace")
	}
	require.False(t, clineBodyClientDisconnected(&clineLifetimeBody{lifetime: life}, false))
}

func TestClineDeadlineFromEitherParentAndAlreadyExpired(t *testing.T) {
	for _, source := range []string{"forward", "http", "expired"} {
		t.Run(source, func(t *testing.T) {
			at := time.Now().Add(70 * time.Millisecond)
			if source == "expired" {
				at = time.Now().Add(-time.Second)
			}
			limited, cancel := context.WithDeadline(context.Background(), at)
			defer cancel()
			forward, incoming := limited, context.Background()
			if source == "http" {
				forward, incoming = incoming, forward
			}
			life := newClineRequestLifetime(forward, time.Second, 2*time.Second, incoming)
			defer life.close()
			deadline, ok := life.ctx.Deadline()
			require.True(t, ok)
			require.True(t, at.Equal(deadline))
			select {
			case <-life.ctx.Done():
				require.ErrorIs(t, life.ctx.Err(), context.DeadlineExceeded)
				require.ErrorIs(t, context.Cause(life.ctx), ErrClineCallerDeadline)
			case <-time.After(800 * time.Millisecond):
				t.Fatal("lost caller deadline")
			}
			life.mu.Lock()
			draining := life.draining
			life.mu.Unlock()
			require.False(t, draining, "deadline must never start disconnect grace")
		})
	}
}

func TestClineDeadlineDoesNotRelabelServiceAbortAsDisconnect(t *testing.T) {
	parent, cancel := context.WithCancelCause(context.Background())
	life := newClineRequestLifetime(parent, time.Second, 2*time.Second)
	defer life.close()
	abort := errors.New("synthetic service shutdown")
	cancel(abort)
	select {
	case <-life.ctx.Done():
		require.ErrorIs(t, context.Cause(life.ctx), abort)
	case <-time.After(500 * time.Millisecond):
		t.Fatal("service abort started a disconnect grace")
	}
	require.False(t, clineBodyClientDisconnected(&clineLifetimeBody{lifetime: life}, false))
}

func TestClineDeadlineInterruptsHeadersAndBodyOverHTTP(t *testing.T) {
	for _, headers := range []bool{false, true} {
		t.Run(map[bool]string{false: "headers", true: "body"}[headers], func(t *testing.T) {
			stopped := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				defer close(stopped)
				if headers {
					w.WriteHeader(http.StatusOK)
					if err := http.NewResponseController(w).Flush(); err != nil {
						return
					}
				}
				<-r.Context().Done()
			}))
			defer server.Close()
			parent, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
			defer cancel()
			life := newClineRequestLifetime(parent, time.Second, 2*time.Second)
			defer life.close()
			req, err := http.NewRequestWithContext(life.ctx, http.MethodGet, server.URL, nil)
			require.NoError(t, err)
			resp, err := server.Client().Do(req)
			if headers {
				require.NoError(t, err)
				body := &clineLifetimeBody{source: resp.Body, lifetime: life}
				_, err = io.ReadAll(body)
				require.NoError(t, body.Close())
			}
			require.ErrorIs(t, err, context.DeadlineExceeded)
			require.NotErrorIs(t, err, context.Canceled)
			select {
			case <-stopped:
			case <-time.After(time.Second):
				t.Fatal("deadline did not release HTTP upstream")
			}
		})
	}
}

type clineBlockedReviewBody struct {
	ctx    context.Context
	prefix *strings.Reader
	hang   bool
}

func (b *clineBlockedReviewBody) Read(p []byte) (int, error) {
	if b.prefix.Len() > 0 {
		return b.prefix.Read(p)
	}
	if !b.hang {
		return 0, io.EOF
	}
	<-b.ctx.Done()
	return 0, context.Cause(b.ctx)
}
func (b *clineBlockedReviewBody) Close() error { return nil }

type clineFailingReviewWriter struct {
	gin.ResponseWriter
	target string
	failed int
}

func (w *clineFailingReviewWriter) Write(p []byte) (int, error) {
	if w.target == "first" || strings.Contains(string(p), w.target) {
		w.failed++
		return 0, io.ErrClosedPipe
	}
	return w.ResponseWriter.Write(p)
}
func (w *clineFailingReviewWriter) WriteString(s string) (int, error) { return w.Write([]byte(s)) }

func TestClineWriterFailureAllBridgesStartDrainAndRetainUsage(t *testing.T) {
	for _, protocol := range []string{"chat", "responses", "messages"} {
		for _, phase := range []string{"first", "terminal"} {
			t.Run(protocol+"/"+phase, func(t *testing.T) {
				life := newClineRequestLifetime(context.Background(), 40*time.Millisecond, 2*time.Second)
				defer life.close()
				payload := "data: {\"id\":\"review\",\"model\":\"cline-pass/model\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"answer\"},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":8,\"completion_tokens\":4,\"total_tokens\":12}}\n\n"
				if phase == "terminal" {
					payload += "data: [DONE]\n\n"
				}
				body := &clineLifetimeBody{source: &clineBlockedReviewBody{ctx: life.ctx, prefix: strings.NewReader(payload), hang: phase == "first"}, lifetime: life}
				defer func() { require.NoError(t, body.Close()) }()
				resp := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}, "X-Request-Id": []string{"writer-review"}}, Body: body}
				rec := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(rec)
				c.Request = httptest.NewRequest("POST", "/v1/"+protocol, nil)
				target := "first"
				if phase == "terminal" {
					target = map[string]string{"chat": "[DONE]", "responses": "[DONE]", "messages": "message_stop"}[protocol]
				}
				writer := &clineFailingReviewWriter{ResponseWriter: c.Writer, target: target}
				c.Writer = writer
				s := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig()}
				var result *OpenAIForwardResult
				var err error
				switch protocol {
				case "chat":
					result, err = s.streamRawChatCompletions(c, resp, clineRoutingAccount(t, cline.ModePass, cline.AuthAPIKey), "public-model", "cline-pass/model", "cline-pass/model", nil, nil, time.Now(), 100)
				case "responses":
					result, err = s.streamChatCompletionsAsResponses(c, resp, "public-model", nil, nil, false, nil, "cline-pass/model", "cline-pass/model", nil, nil, false, time.Now())
				case "messages":
					result, err = s.streamChatCompletionsAsAnthropic(c, resp, "public-model", "cline-pass/model", "cline-pass/model", nil, nil, time.Now())
				}
				require.Positive(t, writer.failed)
				require.NoError(t, c.Request.Context().Err(), "writer failure must not rely on incoming context notification")
				require.NotNil(t, result)
				require.True(t, result.ClientDisconnect)
				require.Equal(t, 8, result.Usage.InputTokens)
				require.Equal(t, 4, result.Usage.OutputTokens)
				if phase == "first" {
					require.ErrorIs(t, err, ErrClineDrainTimeout)
					require.NotContains(t, rec.Body.String(), "response.completed")
				} else {
					require.NoError(t, err)
				}
				life.mu.Lock()
				draining := life.draining
				life.mu.Unlock()
				require.True(t, draining)
			})
		}
	}
}
