//go:build integration

package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/cline"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestClinePostgresAccountSettingsRoundTripAndHeaderGuards(t *testing.T) {
	ctx := context.Background()
	repo, a := clinePostgresAccount(t)
	_, b := clinePostgresAccount(t)
	at := time.Now().UTC()
	state := clineVerifiedState(a, "", at)
	saved, err := repo.SaveClineStateIfUnchanged(ctx, a, state)
	require.NoError(t, err)
	require.True(t, saved)
	until := at.Add(time.Hour).Truncate(time.Second)
	require.NoError(t, repo.SetClineRateLimitIfLater(ctx, a, cline.ScopePass+"weekly", until, "pass_limit", true))
	a, err = repo.GetByID(ctx, a.ID)
	require.NoError(t, err)
	originalState := a.GetClineState()
	require.NotNil(t, originalState)
	key := a.GetCredential("api_key")
	zero := float64(0)
	groupRate := 2.5
	load := 7
	expires := at.Add(24 * time.Hour).Truncate(time.Second)
	a.RateMultiplier, a.GroupRateMultiplier, a.LoadFactor, a.ExpiresAt = &zero, &groupRate, &load, &expires
	a.Schedulable = false
	a.AutoPauseOnExpired = false
	a.Status = service.StatusInactive
	a.Extra[service.AccountCostMultiplierExtraKey] = 0.2
	a.Credentials["header_override_enabled"] = true
	a.Credentials["header_overrides"] = map[string]any{"User-Agent": "saved/1"}
	require.NoError(t, repo.Update(ctx, a))
	got, err := repo.GetByID(ctx, a.ID)
	require.NoError(t, err)
	require.False(t, got.Schedulable)
	require.False(t, got.AutoPauseOnExpired)
	require.Equal(t, service.StatusInactive, got.Status)
	require.Zero(t, *got.RateMultiplier)
	require.Equal(t, 2.5, *got.GroupRateMultiplier)
	require.Equal(t, 0.2, got.CostMultiplier())
	require.Equal(t, 7, *got.LoadFactor)
	require.True(t, got.ExpiresAt.Equal(expires))
	require.Equal(t, key, got.GetCredential("api_key"))
	require.Equal(t, originalState.CredentialFingerprint, got.GetClineState().CredentialFingerprint)
	require.Equal(t, map[string]string{"user-agent": "saved/1"}, got.GetHeaderOverrides())
	require.Greater(t, got.GetModelRateLimitRemainingTime("public-model"), time.Duration(0), "editing settings must not clear scoped rate limits")
	for _, bad := range []map[string]any{
		{"header_override_enabled": "true"}, {"header_overrides": nil},
		{"header_overrides": map[string]any{"Authorization": "forbidden"}},
		{"header_overrides": map[string]any{"User-Agent": "a", "user-agent": "b"}},
		{"header_overrides": map[string]any{"X-Metadata-Test": "bad\nvalue"}},
	} {
		credentials := make(map[string]any, len(got.Credentials))
		for k, v := range got.Credentials {
			credentials[k] = v
		}
		for k, v := range bad {
			credentials[k] = v
		}
		require.Error(t, repo.UpdateCredentials(ctx, got.ID, credentials))
		_, err = repo.BulkUpdate(ctx, []int64{got.ID, b.ID}, service.AccountBulkUpdate{Credentials: bad})
		require.Error(t, err)
		same, err := repo.GetByID(ctx, got.ID)
		require.NoError(t, err)
		require.Equal(t, got.Credentials, same.Credentials)
		other, err := repo.GetByID(ctx, b.ID)
		require.NoError(t, err)
		require.Equal(t, b.Credentials, other.Credentials)
	}
	got.ExpiresAt = nil
	got.LoadFactor = nil
	got.Credentials["header_override_enabled"] = false
	got.Credentials["header_overrides"] = map[string]any{}
	require.NoError(t, repo.Update(ctx, got))
	got, err = repo.GetByID(ctx, got.ID)
	require.NoError(t, err)
	require.Nil(t, got.ExpiresAt)
	require.Nil(t, got.LoadFactor)
	require.Empty(t, got.GetHeaderOverrides())
	require.Equal(t, key, got.GetCredential("api_key"))
}
