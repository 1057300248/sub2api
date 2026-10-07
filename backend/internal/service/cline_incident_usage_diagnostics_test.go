//go:build unit

package service

import (
	"context"
	"fmt"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

type clineIncidentFKError struct{}

func (clineIncidentFKError) Error() string    { return "private SQL detail" }
func (clineIncidentFKError) SQLState() string { return "23503" }

func TestClineIncidentUsageFKDiagnosticsPreserveRecordIdentity(t *testing.T) {
	core, logs := observer.New(zap.ErrorLevel)
	ctx := logger.IntoContext(context.Background(), zap.New(core))
	usage := &UsageLog{RequestID: "req-ledger", AccountID: 42, APIKeyID: 9, UserID: 7}
	LogUsageLogPersistenceFailure(ctx, usage, "service.gateway", "sync_fallback", fmt.Errorf("wrapped: %w", clineIncidentFKError{}))
	rows := logs.All()
	require.Len(t, rows, 1)
	fields := rows[0].ContextMap()
	require.Equal(t, "req-ledger", fields["usage_request_id"])
	require.Equal(t, int64(42), fields["account_id"])
	require.Equal(t, int64(9), fields["api_key_id"])
	require.Equal(t, "23503", fields["sqlstate"])
	require.Equal(t, "foreign_key", fields["failure_kind"])
	require.NotContains(t, fmt.Sprint(rows), "private SQL")
	require.Equal(t, int64(42), usage.AccountID)
}
