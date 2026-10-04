package repository

import (
	"encoding/json"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestQualityModelCooldownLifecycle(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	account := &service.Account{Type: service.AccountTypeOAuth, Concurrency: 20, Extra: map[string]any{}, Credentials: map[string]any{"model_mapping": map[string]any{"alias": "target"}}}
	state := qualityState{}
	action, err := transitionQualityModels(account, &state, 7, []string{"alias"}, now.Add(30*time.Minute), "failed", true, now)
	require.NoError(t, err)
	require.Equal(t, "models_cooled", action)
	require.Equal(t, 5, account.Concurrency)
	require.False(t, account.IsSchedulableForModel("alias"))
	require.Equal(t, 20, *state.PreviousConcurrency)
	action, err = transitionQualityModels(account, &state, 7, []string{"alias"}, now.Add(time.Hour), "failed", true, now)
	require.NoError(t, err)
	require.Equal(t, "model_cooldown_refreshed", action)
	require.Equal(t, 20, *state.PreviousConcurrency)
	action, err = transitionQualityModels(account, &state, 7, []string{"alias"}, now.Add(time.Hour), "passed", true, now)
	require.NoError(t, err)
	require.Equal(t, "restored", action)
	require.Equal(t, 20, account.Concurrency)
	require.Empty(t, account.Extra["model_rate_limits"])
}

func TestQualityModelCooldownPreservesNativeAndManualChanges(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	old := map[string]any{"reason": "upstream_429", "rate_limit_reset_at": now.Add(time.Hour).Format(time.RFC3339)}
	account := &service.Account{Type: service.AccountTypeOAuth, Concurrency: 20, Extra: map[string]any{"model_rate_limits": map[string]any{"target": old, "other": old}}}
	state := qualityState{}
	_, err := transitionQualityModels(account, &state, 7, []string{"target"}, now.Add(30*time.Minute), "failed", true, now)
	require.NoError(t, err)
	_, err = transitionQualityModels(account, &state, 7, []string{"target"}, now.Add(30*time.Minute), "passed", true, now)
	require.NoError(t, err)
	require.Equal(t, old, account.Extra["model_rate_limits"].(map[string]any)["target"])
	require.Equal(t, old, account.Extra["model_rate_limits"].(map[string]any)["other"])
	state = qualityState{}
	_, err = transitionQualityModels(account, &state, 7, []string{"target"}, now.Add(30*time.Minute), "failed", true, now)
	require.NoError(t, err)
	account.Concurrency = 9
	account.Extra["model_rate_limits"].(map[string]any)["target"] = old
	action, err := transitionQualityModels(account, &state, 7, []string{"target"}, now.Add(30*time.Minute), "passed", true, now)
	require.NoError(t, err)
	require.Equal(t, "restore_conflict", action)
	require.Equal(t, 9, account.Concurrency)
	require.Equal(t, old, account.Extra["model_rate_limits"].(map[string]any)["target"])
	_, err = json.Marshal(state)
	require.NoError(t, err)
}

func TestQualityModelInconclusiveRetainsOwnedRestriction(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	a := &service.Account{Type: service.AccountTypeOAuth, Concurrency: 12, Extra: map[string]any{}}
	state := qualityState{RecoveryConcurrency: 3, NativeRecovery: true}
	_, err := transitionQualityModels(a, &state, 7, []string{"target"}, now.Add(time.Minute), "failed", true, now)
	require.NoError(t, err)
	_, err = transitionQualityModels(a, &state, 7, []string{"target"}, now.Add(time.Hour), "inconclusive", true, now)
	require.NoError(t, err)
	require.Equal(t, 3, a.Concurrency)
	require.Equal(t, 12, *state.PreviousConcurrency)
	limits := a.Extra["model_rate_limits"].(map[string]any)
	require.Equal(t, now.Add(time.Hour), cooldownEntryUntil(limits["target"]))
	_, err = transitionQualityModels(a, &state, 7, []string{"target"}, now.Add(time.Hour), "passed", true, now)
	require.NoError(t, err)
	require.Equal(t, 3, a.Concurrency)
	require.Equal(t, 12, *state.RecoveryTarget)
}
