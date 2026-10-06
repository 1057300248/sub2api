//go:build unit

package handler

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestClineForwardTerminationKeepsDeadlineSeparate(t *testing.T) {
	for _, mode := range []string{"deadline", "cancel_before_deadline", "drain", "writer", "success", "other_provider", "ceiling"} {
		t.Run(mode, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest("POST", "/messages", nil)
			a := &service.Account{Platform: service.PlatformCline}
			result := &service.OpenAIForwardResult{Usage: service.OpenAIUsage{InputTokens: 8, OutputTokens: 4}}
			var err error
			switch mode {
			case "deadline":
				ctx, cancel := context.WithDeadline(c.Request.Context(), time.Now().Add(-time.Second))
				defer cancel()
				c.Request = c.Request.WithContext(ctx)
				err = service.ErrClineCallerDeadline
			case "cancel_before_deadline":
				ctx, cancel := context.WithCancel(c.Request.Context())
				cancel()
				c.Request = c.Request.WithContext(ctx)
				err = service.ErrClineCallerDeadline
			case "drain":
				err = service.ErrClineDrainTimeout
			case "writer":
				result.ClientDisconnect = true
				err = errors.New("simulated subsequent upstream error")
			case "other_provider":
				a.Platform = service.PlatformDeepseek
				result.ClientDisconnect = true
			case "ceiling":
				err = service.ErrClineRequestTimeout
			}
			calls, writes := 0, 0
			finished := finishClineForward(c, a, result, err, func(got *service.OpenAIForwardResult) { calls++; require.Same(t, result, got) }, func() { writes++ })
			if mode == "success" || mode == "other_provider" || mode == "ceiling" {
				require.False(t, finished)
				require.Zero(t, calls)
				require.Zero(t, writes)
				return
			}
			require.True(t, finished)
			require.Equal(t, 1, calls)
			if mode == "deadline" || mode == "cancel_before_deadline" {
				require.Equal(t, 1, writes)
				require.Equal(t, "deadline_exceeded", c.GetString("cline_forward_stop_reason"))
			} else {
				require.Zero(t, writes)
				require.Equal(t, "client_disconnected", c.GetString("cline_forward_stop_reason"))
			}
		})
	}
}

func TestClineLocalPolicyErrorsAreNeutralAndProtocolCorrect(t *testing.T) {
	for _, path := range []string{"/v1/chat/completions", "/v1/responses", "/v1/messages"} {
		for _, tc := range []struct {
			err    error
			status int
		}{
			{service.ErrClineHeaderOverrides, http.StatusBadRequest},
			{service.ErrClineObservedCooldown, http.StatusTooManyRequests},
			{service.ErrClineMetadataRequired, http.StatusServiceUnavailable},
			{service.ErrClineAdmissionUnavailable, http.StatusServiceUnavailable},
		} {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest("POST", path, nil)
			account := &service.Account{Platform: service.PlatformCline}
			submitted, deadline := 0, 0
			require.True(t, finishClineForward(c, account, nil, tc.err, func(*service.OpenAIForwardResult) { submitted++ }, func() { deadline++ }))
			require.Equal(t, tc.status, rec.Code)
			require.Contains(t, rec.Body.String(), `"error"`)
			if path == "/v1/messages" {
				require.Contains(t, rec.Body.String(), `"type":"error"`)
			}
			require.Zero(t, submitted)
			require.Zero(t, deadline)
		}
	}
}
