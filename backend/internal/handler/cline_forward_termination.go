package handler

import (
	"context"
	"errors"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// Cline owns a detached, bounded upstream lifetime. A writer failure can precede
// Request.Context cancellation, so the forward result is authoritative too.
// Both outcomes retain observed usage, never retry a disconnected client, and
// never manufacture either a failure or a recovery sample for account health.
func finishClineForward(c *gin.Context, account *service.Account, result *service.OpenAIForwardResult, err error, submit func(*service.OpenAIForwardResult), writeDeadline func()) bool {
	if !account.IsCline() {
		return false
	}
	ctx := c.Request.Context()
	// Forward may have completed and committed a valid response before the
	// caller deadline expires during handler bookkeeping. Never append an
	// error to that successful JSON/SSE or bypass its ordinary settlement.
	if err != nil && (errors.Is(err, service.ErrClineCallerDeadline) || ctx.Err() == context.DeadlineExceeded) {
		c.Set("cline_forward_stop_reason", "deadline_exceeded")
		submit(result)
		writeDeadline()
		return true
	}
	if (result != nil && result.ClientDisconnect) || errors.Is(err, service.ErrClineDrainTimeout) ||
		(ctx.Err() == context.Canceled && context.Cause(ctx) == context.Canceled) {
		c.Set("cline_forward_stop_reason", "client_disconnected")
		submit(result)
		return true
	}
	return false
}
