//go:build unit

package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/cline"
	"github.com/stretchr/testify/require"
)

func clineQuotaFixture(t *testing.T, now time.Time) (*Account, *ClineState) {
	a := clineRoutingAccount(t, cline.ModePass, cline.AuthAPIKey)
	state := &ClineState{Mode: cline.ModePass, FetchedAt: &now, LastSuccessAt: &now, CredentialStatus: "valid", QuotaStatus: "ok", Persisted: true, CredentialFingerprint: ClineCredentialFingerprint(a)}
	a.Extra[ClineStateExtraKey] = state
	return a, state
}

func TestClineQuotaExhaustionSurvivesStalenessErrorsAndExpiry(t *testing.T) {
	now := time.Now().UTC()
	old, reset := now.Add(-time.Hour), now.Add(-time.Minute)
	a, state := clineQuotaFixture(t, old)
	state.Windows = []cline.Window{{Type: "weekly", PercentUsed: 100, ResetsAt: &reset}}
	state.QuotaStatus = "unknown" // Last refresh failed; last-good evidence still blocks.
	view := ClineMetadataForAccount(a, now)
	require.Equal(t, "unknown", view.QuotaStatus)
	require.ErrorIs(t, ClineQuotaAdmission(a, now), ErrClineObservedCooldown)
	require.False(t, a.IsSchedulable())
	require.Len(t, view.Cooldowns, 1)
	require.Equal(t, "pending_recheck", view.RecoveryStatus)
	require.Equal(t, reset, *view.Cooldowns[0].ResetAt)
	// A partial observation is not evidence about the missing exhausted window.
	state.QuotaBlocks = mergeClineQuotaBlocks(state, []cline.Window{{Type: "monthly", PercentUsed: 10}}, now)
	state.Windows = []cline.Window{{Type: "monthly", PercentUsed: 10}}
	require.ErrorIs(t, ClineQuotaAdmission(a, now), ErrClineObservedCooldown)
	// Only a successful observation of weekly itself removes this block.
	state.QuotaBlocks = mergeClineQuotaBlocks(state, []cline.Window{{Type: "weekly", PercentUsed: 0}}, now)
	state.Windows, state.LastSuccessAt = []cline.Window{{Type: "weekly", PercentUsed: 0}}, &now
	require.NoError(t, ClineQuotaAdmission(a, now))
	require.True(t, a.IsSchedulable())
}

func TestClineQuotaRecoveryRequiresEachExhaustedWindow(t *testing.T) {
	now := time.Now().UTC()
	a, state := clineQuotaFixture(t, now)
	five, week, month := now.Add(2*time.Hour), now.Add(3*24*time.Hour), now.Add(20*24*time.Hour)
	state.Windows = []cline.Window{{Type: "five_hour", PercentUsed: 100, ResetsAt: &five}, {Type: "weekly", PercentUsed: 100, ResetsAt: &week}, {Type: "monthly", PercentUsed: 65, ResetsAt: &month}}
	view := ClineMetadataForAccount(a, now)
	require.Len(t, view.Cooldowns, 2, "a non-exhausted monthly window never blocks")
	state.QuotaBlocks = mergeClineQuotaBlocks(state, []cline.Window{{Type: "five_hour", PercentUsed: 0}}, five.Add(time.Second))
	state.Windows = []cline.Window{{Type: "five_hour", PercentUsed: 0}}
	require.Len(t, state.QuotaBlocks, 1)
	require.Equal(t, "weekly", state.QuotaBlocks[0].Type)
	require.ErrorIs(t, ClineQuotaAdmission(a, week.Add(time.Second)), ErrClineObservedCooldown)
}

func TestClineQuotaUnknownResetAndRetryAreNotFabricated(t *testing.T) {
	now := time.Now().UTC()
	a, state := clineQuotaFixture(t, now)
	state.Windows = []cline.Window{{Type: "monthly", PercentUsed: 100}}
	view := ClineMetadataForAccount(a, now)
	require.Len(t, view.Cooldowns, 1)
	require.Nil(t, view.Cooldowns[0].ResetAt)
	require.Equal(t, "pending_recheck", view.RecoveryStatus)
	state.Windows = nil
	until := now.Add(5 * time.Minute)
	key := ClineRateLimitScope(a, cline.ScopePass+"unknown")
	a.Extra["model_rate_limits"] = map[string]any{key: map[string]any{"rate_limit_reset_at": until.Format(time.RFC3339), "reset_authoritative": false}}
	view = ClineMetadataForAccount(a, now)
	require.Len(t, view.Cooldowns, 1)
	require.Nil(t, view.Cooldowns[0].ResetAt)
	require.NotNil(t, view.Cooldowns[0].RetryAt)
}

func TestClineQuotaExpiredInferenceLimitNeedsFreshWindowConfirmation(t *testing.T) {
	now := time.Now().UTC()
	reset, old := now.Add(-time.Minute), now.Add(-2*time.Minute)
	a, state := clineQuotaFixture(t, old)
	key := ClineRateLimitScope(a, cline.ScopePass+"weekly")
	a.Extra["model_rate_limits"] = map[string]any{key: map[string]any{"rate_limit_reset_at": reset.Format(time.RFC3339), "reset_authoritative": true}}
	state.Windows = []cline.Window{{Type: "weekly", PercentUsed: 0}}
	require.ErrorIs(t, ClineQuotaAdmission(a, now), ErrClineMetadataRequired)
	state.LastSuccessAt = &now
	require.NoError(t, ClineQuotaAdmission(a, now))
	state.Windows = []cline.Window{{Type: "monthly", PercentUsed: 0}}
	require.ErrorIs(t, ClineQuotaAdmission(a, now), ErrClineMetadataRequired)
	a.Credentials["base_url"] = "https://custom.example.invalid/api/v1"
	require.NoError(t, ClineQuotaAdmission(a, now), "custom origins cannot be forced to use official metadata")
}

func TestClineAutomaticQuotaScheduleBoundedAndCredentialScoped(t *testing.T) {
	now := time.Now().UTC()
	a, state := clineQuotaFixture(t, now)
	a.ID = 17 // no jitter
	require.True(t, ClineMetadataRefreshDue(a, now))
	next := ClineMetadataNextRefresh(a, state, now)
	require.Equal(t, now.Add(5*time.Minute), next)
	state.NextRefreshAt = &next
	require.False(t, ClineMetadataRefreshDue(a, now))
	for n := 1; n <= 7; n++ {
		state.RefreshFailures = n
		delay := ClineMetadataNextRefresh(a, state, now).Sub(now)
		require.GreaterOrEqual(t, delay, 30*time.Second)
		require.LessOrEqual(t, delay, 30*time.Minute)
	}
	state.RefreshFailures = 0
	reset := now.Add(20 * time.Second)
	state.QuotaBlocks = []ClineQuotaBlock{{Type: "monthly", ResetAt: &reset, ObservedAt: now}}
	require.Equal(t, now.Add(30*time.Second), ClineMetadataNextRefresh(a, state, now))
	reset = now.Add(2 * 24 * time.Hour)
	require.Equal(t, now.Add(30*time.Minute), ClineMetadataNextRefresh(a, state, now))
	a.Credentials["api_key"] = "rotated-fixture"
	require.True(t, ClineMetadataRefreshDue(a, now), "credential rotation invalidates the old poll schedule")
	a.Schedulable = false
	require.False(t, ClineMetadataRefreshDue(a, now))
	a.Schedulable = true
	a.Credentials["account_mode"] = cline.ModeFree
	require.False(t, ClineMetadataRefreshDue(a, now))
}

func TestClineAutomaticRefreshReusesCatalogAndRequiresCurrentIdentity(t *testing.T) {
	a, repo, transport, gateway := clineMetadataFixture(t)
	_, err := gateway.RefreshClineMetadata(context.Background(), a)
	require.NoError(t, err)
	a.Extra[ClineStateExtraKey] = repo.stored
	transport.requests = nil
	_, err = gateway.refreshClineMetadata(context.Background(), a, true)
	require.NoError(t, err)
	require.Len(t, transport.requests, 2, "fresh public catalog is reused by automatic refresh")
	transport.requests = nil
	transport.responses[cline.ProfileURL] = `{"invalid_schema":true}`
	_, err = gateway.refreshClineMetadata(context.Background(), a, true)
	require.NoError(t, err)
	require.Len(t, transport.requests, 1, "quota cannot be attributed after identity verification fails")
	require.Equal(t, "unknown", repo.stored.QuotaStatus)
}

func TestClineAutomaticRefreshFailureKeepsExhaustionAndBackoff(t *testing.T) {
	a, repo, transport, gateway := clineMetadataFixture(t)
	old := time.Now().UTC().Add(-time.Hour)
	a.Extra[ClineStateExtraKey] = &ClineState{Mode: cline.ModePass, FetchedAt: &old, LastSuccessAt: &old, Persisted: true, CredentialFingerprint: ClineCredentialFingerprint(a), Windows: []cline.Window{{Type: "monthly", PercentUsed: 100}}}
	transport.failure = errors.New("private-fixture-error")
	view, err := gateway.RefreshClineMetadata(context.Background(), a)
	require.NoError(t, err)
	require.Len(t, view.Cooldowns, 1)
	require.Nil(t, view.Cooldowns[0].ResetAt)
	require.NotNil(t, repo.stored.NextRefreshAt)
	require.Equal(t, 1, repo.stored.RefreshFailures)
	require.NotContains(t, view.Error, "private-fixture")
}
