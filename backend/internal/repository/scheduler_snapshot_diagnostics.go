package repository

import (
	"context"
	"errors"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/pkg/incidentdiag"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"go.uber.org/zap"
)

func logSchedulerSnapshotFailure(ctx context.Context, operation string, ids []int64, err error) {
	if ctx == nil {
		ctx = context.Background()
	}
	kind, state := incidentdiag.ErrorClass(err)
	if errors.Is(err, service.ErrAccountNotFound) {
		kind = "account_not_found"
	}
	count := len(ids)
	if len(ids) > 32 {
		ids = ids[:32]
	}
	requestID, _ := ctx.Value(ctxkey.RequestID).(string)
	fields := []zap.Field{zap.String("component", "repository.account"), zap.String("operation", operation),
		zap.String("snapshot_request_id", incidentdiag.RequestID(requestID)), zap.Int64s("account_ids", ids), zap.Int("account_count", count),
		zap.String("failure_kind", kind), zap.String("sqlstate", state), zap.Bool("context_done", ctx.Err() != nil)}
	if count == 1 {
		fields = append(fields, zap.Int64("account_id", ids[0]))
	}
	logger.FromContext(ctx).Warn("scheduler.account_snapshot_failed", fields...)
}
