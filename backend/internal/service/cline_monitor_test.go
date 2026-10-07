//go:build unit

package service

import (
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/cline"
	"github.com/stretchr/testify/require"
)

func TestClineMonitorCannotTurnUnknownIntoHealthyZero(t *testing.T) {
	a := clineRoutingAccount(t, cline.ModePass, cline.AuthAPIKey)
	now := time.Now().UTC()
	require.NoError(t, clineMonitorCapability(a))
	require.False(t, ClineMonitorQuotaSnapshot(a, now).Success)
	a.Extra[ClineStateExtraKey] = &ClineState{Mode: cline.ModePass, CredentialFingerprint: ClineCredentialFingerprint(a), FetchedAt: &now, LastSuccessAt: &now, QuotaStatus: "ok", Persisted: true, Windows: []cline.Window{{Type: "five_hour", PercentUsed: 0}, {Type: "weekly", PercentUsed: 50}, {Type: "monthly", PercentUsed: 75}}}
	snapshot := ClineMonitorQuotaSnapshot(a, now)
	require.True(t, snapshot.Success)
	require.Len(t, snapshot.Tiers, 3)
	require.Equal(t, 0.0, snapshot.Tiers[0].UsedPercent)
	require.False(t, ClineMonitorQuotaSnapshot(a, now.Add(ClineQuotaFreshness)).Success)
	a.Credentials["account_mode"] = cline.ModeFree
	require.Error(t, clineMonitorCapability(a))
	require.False(t, ClineMonitorQuotaSnapshot(a, now).Success)
}
