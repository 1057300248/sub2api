package service

import (
	"context"

	"github.com/Wei-Shaw/sub2api/internal/pkg/incidentdiag"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"go.uber.org/zap"
)

// LogUsageLogPersistenceFailure associates failed writes with the exact record
// identity. It never changes the account reference, disables a foreign key,
// retries billing, or treats a failed write as durable usage.
func LogUsageLogPersistenceFailure(ctx context.Context, usage *UsageLog, component, stage string, err error) {
	if usage == nil || err == nil {
		return
	}
	kind, state := incidentdiag.ErrorClass(err)
	logger.FromContext(ctx).Error("usage_log.persistence_failed",
		zap.String("component", component), zap.String("failure_stage", stage),
		zap.String("usage_request_id", incidentdiag.RequestID(usage.RequestID)),
		zap.Int64("account_id", usage.AccountID), zap.Int64("api_key_id", usage.APIKeyID), zap.Int64("user_id", usage.UserID),
		zap.String("failure_kind", kind), zap.String("sqlstate", state))
}
