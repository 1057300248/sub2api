package repository

import (
	"context"

	"github.com/Wei-Shaw/sub2api/internal/pkg/incidentdiag"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"go.uber.org/zap"
)

func logPreparedUsageFailure(ctx context.Context, prepared usageLogInsertPrepared, stage string, err error) {
	// IDs are captured with the SQL arguments before asynchronous batching.
	service.LogUsageLogPersistenceFailure(ctx, &service.UsageLog{RequestID: prepared.requestID, UserID: prepared.userID, APIKeyID: prepared.apiKeyID, AccountID: prepared.accountID}, "repository.usage_log", stage, err)
}
func logUsageLogBatchFailure(ctx context.Context, count int, err error) {
	kind, state := incidentdiag.ErrorClass(err)
	logger.FromContext(ctx).Error("usage_log.batch_persistence_failed", zap.String("component", "repository.usage_log"), zap.Int("batch_count", count), zap.String("failure_kind", kind), zap.String("sqlstate", state))
}
