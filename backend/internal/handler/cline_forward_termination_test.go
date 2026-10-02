//go:build unit

package handler

import (
	"context"
	"errors"
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
