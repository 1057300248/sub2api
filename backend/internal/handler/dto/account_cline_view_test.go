package dto

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/cline"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestClineAccountListUsesSafeCredentialBoundQuotaProjection(t *testing.T) {
	now := time.Now().UTC()
	a := &service.Account{Platform: service.PlatformCline, Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true, Credentials: map[string]any{"api_key": "must-not-leak", "account_mode": "pass", "cline_auth_type": "api_key", "base_url": cline.BaseURL}, Extra: map[string]any{"cost_multiplier": 0.2, "ordinary": "preserve"}}
	state := &service.ClineState{Mode: "pass", AuthType: "api_key", Identity: strings.Repeat("a", 64), CredentialFingerprint: service.ClineCredentialFingerprint(a), FetchedAt: &now, LastSuccessAt: &now, Persisted: true, QuotaStatus: "ok", CredentialStatus: "valid", Windows: []cline.Window{{Type: "five_hour", PercentUsed: 12}, {Type: "weekly", PercentUsed: 100}, {Type: "monthly", PercentUsed: 40}}}
	a.Extra[service.ClineStateExtraKey] = state
	a.Extra["model_rate_limits"] = map[string]any{"private-fingerprint": "not-for-ui"}
	full := AccountFromServiceShallow(a)
	item := AccountListItemFromAccount(full)
	require.NotNil(t, item.ClineUsage)
	require.Nil(t, item.ClineUsage.Catalog)
	require.Equal(t, "ok", item.ClineUsage.QuotaStatus)
	require.Equal(t, 12.0, *item.ClineUsage.Windows[0].PercentUsed)
	require.NotEmpty(t, item.ClineUsage.Cooldowns)
	raw, err := json.Marshal(item)
	require.NoError(t, err)
	for _, secret := range []string{"must-not-leak", state.CredentialFingerprint, state.Identity, "private-fingerprint", "cline_state"} {
		require.NotContains(t, string(raw), secret)
	}
	require.Equal(t, 0.2, item.Extra["cost_multiplier"])
	require.Equal(t, "preserve", item.Extra["ordinary"])
	require.Contains(t, a.Extra, service.ClineStateExtraKey, "do not mutate source Extra")
	require.Contains(t, a.Extra, "model_rate_limits")
	a.Credentials["api_key"] = "rotated-key"
	rotated := AccountFromServiceShallow(a)
	require.NotNil(t, rotated.ClineUsage)
	for _, w := range rotated.ClineUsage.Windows {
		require.Nil(t, w.PercentUsed)
	}
	a.Platform = service.PlatformDeepseek
	require.Nil(t, AccountFromServiceShallow(a).ClineUsage)
}
