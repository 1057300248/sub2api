//go:build integration

package repository

import (
	"context"
	"maps"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/cline"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

// Uses the harness's disposable PostgreSQL database, not a transaction shared
// by all goroutines: concurrent CAS operations acquire independent connections.
func clinePostgresAccount(t *testing.T) (*accountRepository, *service.Account) {
	t.Helper()
	client := testEntClient(t)
	repo := newAccountRepositoryWithSQL(client, integrationDB, nil)
	a := clineRepositoryFixture(t)
	a.ID = 0
	require.NoError(t, repo.Create(context.Background(), a))
	t.Cleanup(func() {
		_, err := integrationDB.ExecContext(context.Background(), "DELETE FROM scheduler_outbox WHERE account_id=$1", a.ID)
		require.NoError(t, err)
		require.NoError(t, client.Account.DeleteOneID(a.ID).Exec(context.Background()))
	})
	return repo, a
}

func TestClinePostgresConcurrentScopedCAS(t *testing.T) {
	repo, a := clinePostgresAccount(t)
	ctx := context.Background()
	longest := time.Now().UTC().Add(7 * 24 * time.Hour).Truncate(time.Second)
	require.NoError(t, repo.SetClineRateLimitIfLater(ctx, a, cline.ScopePass+"weekly", longest, "pass_limit", true))
	const writers = 24
	start := make(chan struct{})
	results := make(chan error, writers)
	var wg sync.WaitGroup
	for n := 0; n < writers; n++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			scope := cline.ScopePass + "weekly"
			until := time.Now().Add(time.Duration(i+1) * time.Minute)
			if i%3 == 0 {
				scope = cline.ScopeThrottle
			}
			results <- repo.SetClineRateLimitIfLater(ctx, a, scope, until, "throttle", false)
		}(n)
	}
	close(start)
	wg.Wait()
	close(results)
	for err := range results {
		require.NoError(t, err)
	}
	got, err := repo.GetByID(ctx, a.ID)
	require.NoError(t, err)
	limits, ok := got.Extra["model_rate_limits"].(map[string]any)
	require.True(t, ok)
	weekly, ok := limits[service.ClineRateLimitScope(a, cline.ScopePass+"weekly")].(map[string]any)
	require.True(t, ok)
	require.Equal(t, float64(longest.Unix()), weekly["reset_unix"])
	require.Equal(t, true, weekly["reset_authoritative"])
	require.Contains(t, limits, service.ClineRateLimitScope(a, cline.ScopeThrottle))
	require.NotContains(t, limits, service.ClineRateLimitScope(a, cline.ScopePass+"monthly"))
	var events int
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT count(*) FROM scheduler_outbox WHERE account_id=$1", a.ID).Scan(&events))
	require.Positive(t, events)
}

func TestClinePostgresCredentialRotationRejectsStaleObservations(t *testing.T) {
	repo, a := clinePostgresAccount(t)
	ctx := context.Background()
	require.NoError(t, repo.SetClineRateLimitIfLater(ctx, a, cline.ScopePass+"weekly", time.Now().Add(time.Hour), "pass_limit", true))
	rotated, err := repo.GetByID(ctx, a.ID)
	require.NoError(t, err)
	rotated.Credentials = maps.Clone(rotated.Credentials)
	rotated.Credentials["api_key"] = "rotated-fixture-key-not-real"
	require.NoError(t, repo.Update(ctx, rotated))
	now := time.Now().UTC()
	state := &service.ClineState{Mode: cline.ModePass, AuthType: cline.AuthAccountToken, FetchedAt: &now, CredentialFingerprint: service.ClineCredentialFingerprint(a)}
	saved, err := repo.SaveClineStateIfUnchanged(ctx, a, state)
	require.NoError(t, err)
	require.False(t, saved)
	require.NoError(t, repo.SetClineRateLimitIfLater(ctx, a, cline.ScopePass+"monthly", now.Add(30*24*time.Hour), "pass_limit", true))
	got, err := repo.GetByID(ctx, a.ID)
	require.NoError(t, err)
	require.Nil(t, got.GetClineState())
	require.Zero(t, got.GetModelRateLimitRemainingTime("public-model"))
	limits, ok := got.Extra["model_rate_limits"].(map[string]any)
	require.True(t, ok)
	require.NotContains(t, limits, service.ClineRateLimitScope(a, cline.ScopePass+"monthly"))
	require.NotContains(t, limits, service.ClineRateLimitScope(rotated, cline.ScopePass+"weekly"))
}

func TestClinePostgresManagedStateSurvivesAllExtraEdits(t *testing.T) {
	repo, a := clinePostgresAccount(t)
	ctx := context.Background()
	stale, err := repo.GetByID(ctx, a.ID)
	require.NoError(t, err)
	now := time.Now().UTC()
	state := &service.ClineState{Mode: cline.ModePass, AuthType: cline.AuthAccountToken, FetchedAt: &now, QuotaStatus: "unknown", CredentialFingerprint: service.ClineCredentialFingerprint(a)}
	saved, err := repo.SaveClineStateIfUnchanged(ctx, a, state)
	require.NoError(t, err)
	require.True(t, saved)
	require.NoError(t, repo.SetClineRateLimitIfLater(ctx, a, cline.ScopePass+"weekly", now.Add(3*time.Hour), "pass_limit", true))
	original, err := repo.GetByID(ctx, a.ID)
	require.NoError(t, err)
	originalState := original.Extra[service.ClineStateExtraKey]
	originalLimits := original.Extra["model_rate_limits"]
	stale.Name = "Cline edited name"
	stale.Extra = map[string]any{service.ClineStateExtraKey: "forged", "model_rate_limits": nil, "operator_note": "preserved"}
	require.NoError(t, repo.UpdateWithAccountBillingSettings(ctx, stale, nil, nil, nil))
	require.NoError(t, repo.UpdateExtra(ctx, a.ID, map[string]any{service.ClineStateExtraKey: nil, "model_rate_limits": map[string]any{}, "operator_note": "direct"}))
	affected, err := repo.BulkUpdate(ctx, []int64{a.ID}, service.AccountBulkUpdate{Extra: map[string]any{service.ClineStateExtraKey: "forged-bulk", "model_rate_limits": nil, "operator_note": "bulk"}})
	require.NoError(t, err)
	require.EqualValues(t, 1, affected)
	got, err := repo.GetByID(ctx, a.ID)
	require.NoError(t, err)
	require.Equal(t, originalState, got.Extra[service.ClineStateExtraKey])
	require.Equal(t, originalLimits, got.Extra["model_rate_limits"])
	require.Equal(t, "bulk", got.Extra["operator_note"])
	require.Positive(t, got.GetModelRateLimitRemainingTime("public-model"))
}

func TestClinePostgresCreationCannotImportManagedState(t *testing.T) {
	client := testEntClient(t)
	repo := newAccountRepositoryWithSQL(client, integrationDB, nil)
	a := clineRepositoryFixture(t)
	a.ID = 0
	a.Extra = map[string]any{service.ClineStateExtraKey: "forged", "model_rate_limits": map[string]any{"forged": true}, "operator_note": "keep"}
	require.NoError(t, repo.Create(context.Background(), a))
	t.Cleanup(func() {
		_, err := integrationDB.ExecContext(context.Background(), "DELETE FROM scheduler_outbox WHERE account_id=$1", a.ID)
		require.NoError(t, err)
		require.NoError(t, client.Account.DeleteOneID(a.ID).Exec(context.Background()))
	})
	got, err := repo.GetByID(context.Background(), a.ID)
	require.NoError(t, err)
	require.NotContains(t, got.Extra, service.ClineStateExtraKey)
	require.NotContains(t, got.Extra, "model_rate_limits")
	require.Equal(t, "keep", got.Extra["operator_note"])
}

func TestClinePostgresMigrationAndQuotaSchema(t *testing.T) {
	tx := testEntTx(t)
	ctx := context.Background()
	user := mustCreateUser(t, tx.Client(), &service.User{})
	_, err := tx.Client().UserPlatformQuota.Create().SetUserID(user.ID).SetPlatform(service.PlatformCline).Save(ctx)
	require.NoError(t, err, "Ent validator and the migrated PostgreSQL CHECK must both accept Cline")
	group := mustCreateGroup(t, tx.Client(), &service.Group{Name: "Cline composite fixture", Platform: service.PlatformComposite})
	_, err = tx.Client().CompositeModelRoute.Create().SetGroupID(group.ID).SetPublicModel("cline-fixture").SetTargetPlatform(service.PlatformCline).SetUpstreamModel("cline-pass/model").SetEndpoint("responses").Save(ctx)
	require.NoError(t, err)
	for _, name := range []string{"user_platform_quotas_platform_check", "composite_model_routes_target_platform_check", "channel_monitors_provider_check", "channel_monitor_request_templates_provider_check"} {
		var definition string
		require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conname=$1", name).Scan(&definition))
		require.Contains(t, definition, "'cline'")
	}
}
