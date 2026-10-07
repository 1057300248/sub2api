//go:build unit

package repository

import (
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestClineIncidentUsageIdentityMatchesSQLSnapshot(t *testing.T) {
	log := &service.UsageLog{UserID: 7, APIKeyID: 9, AccountID: 42, RequestID: "req-incident", Model: "model"}
	prepared := prepareUsageLogInsert(log)
	log.UserID = 70
	log.APIKeyID = 90
	log.AccountID = 420
	log.RequestID = "later-request"
	require.Equal(t, int64(7), prepared.userID)
	require.Equal(t, int64(9), prepared.apiKeyID)
	require.Equal(t, int64(42), prepared.accountID)
	require.Equal(t, "req-incident", prepared.requestID)
	require.Equal(t, prepared.userID, prepared.args[0])
	require.Equal(t, prepared.apiKeyID, prepared.args[1])
	require.Equal(t, prepared.accountID, prepared.args[2])
}
