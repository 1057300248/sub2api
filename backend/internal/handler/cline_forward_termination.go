package handler

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// Cline owns a detached, bounded upstream lifetime. A writer failure can precede
// Request.Context cancellation, so the forward result is authoritative too.
// Both outcomes retain observed usage, never retry a disconnected client, and
// never manufacture either a failure or a recovery sample for account health.
func finishClineForward(c *gin.Context, account *service.Account, result *service.OpenAIForwardResult, err error, submit func(*service.OpenAIForwardResult), writeDeadline func()) bool {
	status, message := 0, ""
	switch {
	case errors.Is(err, service.ErrClineHeaderOverrides):
		status, message = http.StatusBadRequest, service.ErrClineHeaderOverrides.Error()
	case account.IsCline() && errors.Is(err, service.ErrClineLocalQuotaExceeded):
		status, message = http.StatusTooManyRequests, service.ErrClineLocalQuotaExceeded.Error()
	case account.IsCline() && errors.Is(err, service.ErrClineObservedCooldown):
		status, message = http.StatusTooManyRequests, "Cline quota is cooling or awaiting an automatic recheck"
	case account.IsCline() && (errors.Is(err, service.ErrClineMetadataRequired) || errors.Is(err, service.ErrClineMetadataChanged) || errors.Is(err, service.ErrClineAdmissionUnavailable)):
		status, message = http.StatusServiceUnavailable, "Cline account metadata is being checked; retry later"
	}
	if status != 0 {
		kind := "invalid_request_error"
		switch status {
		case http.StatusTooManyRequests:
			kind = "rate_limit_error"
		case http.StatusServiceUnavailable:
			kind = "admission_unavailable"
		}
		payload := gin.H{"error": gin.H{"type": kind, "message": message}}
		if strings.HasSuffix(c.Request.URL.Path, "/messages") {
			payload["type"] = "error"
		}
		if !c.Writer.Written() {
			c.JSON(status, payload)
		} else {
			service.StopOpenAICompactSSEKeepaliveCommitted(c)
			service.MarkOpsStreamError(c, kind, message, status)
			c.SSEvent("error", payload)
			c.Writer.Flush()
		}
		return true
	}
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
