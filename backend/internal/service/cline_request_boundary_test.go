//go:build unit

package service

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/cline"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestClineDrainSendBoundaryObservesHTTPClientCancellation(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	defer cancel()
	body := []byte(`{"model":"cline-pass/model","stream":false,"messages":[{"role":"user","content":"test"}]}`)
	upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"choices":[{"message":{"content":"ok"}}]}`))}}
	gateway := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream, accountRepo: &clineAdmissionTestRepository{}}
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewReader(body)).WithContext(parent)
	// A detached service context must not hide HTTP-client cancellation.
	resp, err := gateway.sendCCUpstreamRequest(context.Background(), c, clineRoutingAccount(t, cline.ModePass, cline.AuthAPIKey), cline.BaseURL+"/chat/completions", body, false, "synthetic-key", "", "")
	require.NoError(t, err)
	require.NotNil(t, resp)
	defer func() { require.NoError(t, resp.Body.Close()) }()
	bounded, ok := resp.Body.(*clineLifetimeBody)
	require.True(t, ok, "the actual send boundary must own the response lifetime")
	// Shorten only this test instance before cancellation; no production test
	// switch or environment override is introduced.
	bounded.lifetime.mu.Lock()
	bounded.lifetime.grace = 20 * time.Millisecond
	bounded.lifetime.mu.Unlock()
	cancel()
	select {
	case <-upstream.lastReq.Context().Done():
		require.ErrorIs(t, context.Cause(upstream.lastReq.Context()), ErrClineDrainTimeout)
		require.ErrorIs(t, context.Cause(upstream.lastReq.Context()), context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("send boundary lost the HTTP-client cancellation")
	}
	_, err = io.ReadAll(resp.Body)
	require.ErrorIs(t, err, ErrClineDrainTimeout)
	require.ErrorIs(t, err, context.Canceled)
}

type clineLoopbackUpstream struct {
	HTTPUpstream
	target  *url.URL
	client  *http.Client
	lastReq *http.Request
}

func (u *clineLoopbackUpstream) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	u.lastReq = req
	local := req.Clone(req.Context())
	target := *req.URL
	target.Scheme, target.Host = u.target.Scheme, u.target.Host
	local.URL, local.Host = &target, u.target.Host
	return u.client.Do(local)
}

// Exercise all real forwarding bridges over loopback HTTP. The original client
// cancels before the first token, but usage arriving inside the bounded grace
// survives, and completing the forward closes the upstream request context.
func TestClineDrainThreeProtocolsRetainDelayedUsage(t *testing.T) {
	for _, protocol := range []string{"chat", "responses", "messages"} {
		t.Run(protocol, func(t *testing.T) {
			parent, cancel := context.WithCancel(context.Background())
			defer cancel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				w.Header().Set("X-Request-Id", "cline-late-usage-fixture")
				w.WriteHeader(http.StatusOK)
				if err := http.NewResponseController(w).Flush(); err != nil {
					return
				}
				cancel()
				timer := time.NewTimer(20 * time.Millisecond)
				defer timer.Stop()
				select {
				case <-r.Context().Done():
					return
				case <-timer.C:
				}
				payload := "data: {\"id\":\"late\",\"model\":\"cline-pass/model\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"late answer\"}}]}\n\ndata: {\"choices\":[],\"usage\":{\"prompt_tokens\":8,\"completion_tokens\":4,\"total_tokens\":12}}\n\ndata: [DONE]\n\n"
				if _, err := io.WriteString(w, payload); err != nil {
					return
				}
			}))
			defer server.Close()
			target, err := url.Parse(server.URL)
			require.NoError(t, err)
			upstream := &clineLoopbackUpstream{target: target, client: server.Client()}
			gateway := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream, accountRepo: &clineAdmissionTestRepository{}}
			body, path := clineProtocolRequest(protocol, true)
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest("POST", path, bytes.NewReader(body)).WithContext(parent)
			result, err := invokeClineProtocol(gateway, c, clineRoutingAccount(t, cline.ModePass, cline.AuthAPIKey), body, protocol)
			require.NoError(t, err)
			require.NotNil(t, result)
			require.ErrorIs(t, parent.Err(), context.Canceled)
			require.Equal(t, 8, result.Usage.InputTokens)
			require.Equal(t, 4, result.Usage.OutputTokens)
			require.Equal(t, "cline-late-usage-fixture", result.RequestID)
			require.NotNil(t, upstream.lastReq)
			require.ErrorIs(t, upstream.lastReq.Context().Err(), context.Canceled)
		})
	}
}
