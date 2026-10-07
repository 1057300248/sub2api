//go:build unit

package repository

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func TestClineIncidentSnapshotDiagnosticsSeparateMissingFromBackend(t *testing.T) {
	core, logs := observer.New(zap.WarnLevel)
	ctx := logger.IntoContext(context.WithValue(context.Background(), ctxkey.RequestID, "req-snapshot"), zap.New(core))
	logSchedulerSnapshotFailure(ctx, "read", []int64{42}, service.ErrAccountNotFound)
	logSchedulerSnapshotFailure(ctx, "read", []int64{43}, errors.New("private database connection detail"))
	rows := logs.All()
	require.Len(t, rows, 2)
	require.Equal(t, "account_not_found", rows[0].ContextMap()["failure_kind"])
	require.Equal(t, int64(42), rows[0].ContextMap()["account_id"])
	require.Equal(t, "req-snapshot", rows[0].ContextMap()["snapshot_request_id"])
	require.Equal(t, "persistence_error", rows[1].ContextMap()["failure_kind"])
	require.NotContains(t, fmt.Sprint(rows), "private database")
	ids := make([]int64, 100)
	for i := range ids {
		ids[i] = int64(i + 1)
	}
	logSchedulerSnapshotFailure(ctx, "batch_read", ids, context.DeadlineExceeded)
	require.EqualValues(t, 100, logs.All()[2].ContextMap()["account_count"])
	require.Len(t, logs.All()[2].ContextMap()["account_ids"], 32)
}
