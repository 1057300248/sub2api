//go:build integration

package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"sync"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/pkg/cline"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func clineNextEventCount(t *testing.T, id int64) int {
	t.Helper()
	var n int
	require.NoError(t, integrationDB.QueryRowContext(context.Background(), "SELECT count(*) FROM scheduler_outbox WHERE account_id=$1", id).Scan(&n))
	return n
}
func TestClinePostgresAllQuotaDimensionsEmitFirstCrossing(t *testing.T) {
	ctx := context.Background()
	for _, dimension := range []string{"total", "daily", "weekly", "all"} {
		t.Run(dimension, func(t *testing.T) {
			repo, a := clinePostgresAccount(t)
			fields := map[string]any{}
			if dimension == "total" || dimension == "all" {
				fields["quota_limit"] = 10.0
			}
			if dimension == "daily" || dimension == "all" {
				fields["quota_daily_limit"] = 10.0
			}
			if dimension == "weekly" || dimension == "all" {
				fields["quota_weekly_limit"] = 10.0
			}
			require.NoError(t, repo.UpdateExtra(ctx, a.ID, fields))
			base := clineNextEventCount(t, a.ID)
			require.NoError(t, repo.IncrementQuotaUsed(ctx, a.ID, 9))
			require.Equal(t, base, clineNextEventCount(t, a.ID))
			require.NoError(t, repo.IncrementQuotaUsed(ctx, a.ID, 1))
			require.Equal(t, base+1, clineNextEventCount(t, a.ID))
			require.NoError(t, repo.IncrementQuotaUsed(ctx, a.ID, 2))
			require.Equal(t, base+1, clineNextEventCount(t, a.ID), "already exhausted must not emit again")
			current, err := repo.GetByID(ctx, a.ID)
			require.NoError(t, err)
			require.True(t, current.IsQuotaExceeded())
			require.NoError(t, repo.ResetClineLocalQuota(ctx, a.ID))
			base = clineNextEventCount(t, a.ID)
			require.NoError(t, repo.IncrementQuotaUsed(ctx, a.ID, 10))
			require.Equal(t, base+1, clineNextEventCount(t, a.ID))
		})
	}
}
func TestClinePostgresQuotaCrossingConcurrencyAndOutboxRollback(t *testing.T) {
	ctx := context.Background()
	repo, a := clinePostgresAccount(t)
	require.NoError(t, repo.UpdateExtra(ctx, a.ID, map[string]any{"quota_daily_limit": 10.0, "quota_weekly_limit": 10.0}))
	base := clineNextEventCount(t, a.ID)
	const writers = 16
	errs := make(chan error, writers)
	var wg sync.WaitGroup
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); errs <- repo.IncrementQuotaUsed(ctx, a.ID, 1) }()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	require.Equal(t, base+1, clineNextEventCount(t, a.ID))
	got, err := repo.GetByID(ctx, a.ID)
	require.NoError(t, err)
	require.Equal(t, 16.0, got.GetQuotaDailyUsed())
	require.NoError(t, repo.ResetClineLocalQuota(ctx, a.ID))
	base = clineNextEventCount(t, a.ID)
	tx, err := repo.client.Tx(ctx)
	require.NoError(t, err)
	txCtx := dbent.NewTxContext(ctx, tx)
	require.NoError(t, repo.IncrementQuotaUsed(txCtx, a.ID, 10))
	require.NoError(t, tx.Rollback())
	require.Equal(t, base, clineNextEventCount(t, a.ID))
	got, err = repo.GetByID(ctx, a.ID)
	require.NoError(t, err)
	require.Zero(t, got.GetQuotaDailyUsed())
	fn := fmt.Sprintf("cline_crossing_%d", a.ID)
	_, err = integrationDB.ExecContext(ctx, fmt.Sprintf(`CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.account_id=%d THEN RAISE EXCEPTION 'outbox fixture'; END IF; RETURN NEW; END $$; CREATE TRIGGER %s BEFORE INSERT ON scheduler_outbox FOR EACH ROW EXECUTE FUNCTION %s();`, fn, a.ID, fn, fn))
	require.NoError(t, err)
	t.Cleanup(func() {
		_, e := integrationDB.ExecContext(ctx, fmt.Sprintf("DROP TRIGGER IF EXISTS %s ON scheduler_outbox; DROP FUNCTION IF EXISTS %s();", fn, fn))
		require.NoError(t, e)
	})
	require.Error(t, repo.IncrementQuotaUsed(ctx, a.ID, 10))
	got, err = repo.GetByID(ctx, a.ID)
	require.NoError(t, err)
	require.Zero(t, got.GetQuotaDailyUsed())
	require.Zero(t, got.GetQuotaWeeklyUsed())
}
func TestClinePostgresQuotaCrossingAfterRollover(t *testing.T) {
	ctx := context.Background()
	repo, a := clinePostgresAccount(t)
	require.NoError(t, repo.UpdateExtra(ctx, a.ID, map[string]any{"quota_daily_limit": 10.0, "quota_weekly_limit": 10.0}))
	// Runtime-only fixture writes retain real rollover expressions, not a mocked clock.
	_, err := integrationDB.ExecContext(ctx, `UPDATE accounts SET extra=extra||jsonb_build_object('quota_daily_used',20,'quota_weekly_used',20,'quota_daily_start',to_char(NOW()-interval '8 days','YYYY-MM-DD"T"HH24:MI:SS"Z"'),'quota_weekly_start',to_char(NOW()-interval '8 days','YYYY-MM-DD"T"HH24:MI:SS"Z"')) WHERE id=$1`, a.ID)
	require.NoError(t, err)
	base := clineNextEventCount(t, a.ID)
	require.NoError(t, repo.IncrementQuotaUsed(ctx, a.ID, 9))
	require.Equal(t, base, clineNextEventCount(t, a.ID))
	got, err := repo.GetByID(ctx, a.ID)
	require.NoError(t, err)
	require.Equal(t, 9.0, got.GetQuotaDailyUsed())
	require.Equal(t, 9.0, got.GetQuotaWeeklyUsed())
	require.NoError(t, repo.IncrementQuotaUsed(ctx, a.ID, 1))
	require.Equal(t, base+1, clineNextEventCount(t, a.ID))
}
func TestClinePostgresToken401RotationAndObservationCAS(t *testing.T) {
	ctx := context.Background()
	repo, a := clinePostgresAccount(t)
	a.Credentials = maps.Clone(a.Credentials)
	a.Credentials["cline_auth_type"] = cline.AuthAccountToken
	require.NoError(t, repo.Update(ctx, a))
	now := time.Now().UTC().Truncate(time.Millisecond)
	state := clineQuotaState(a, clineQuotaSubject(t, a.ID), now, cline.Window{Type: "weekly", PercentUsed: 20})
	state.CredentialStatus = "valid"
	saved, err := repo.SaveClineStateIfUnchanged(ctx, a, state)
	require.NoError(t, err)
	require.True(t, saved)
	require.NoError(t, repo.SetClineRateLimitIfLater(ctx, a, cline.ScopePass+"weekly", now.Add(time.Hour), "pass_limit", true))
	original, err := repo.GetByID(ctx, a.ID)
	require.NoError(t, err)
	base := clineNextEventCount(t, a.ID)
	marked, err := repo.MarkClineReauthenticationRequired(ctx, a, now.Add(time.Second))
	require.NoError(t, err)
	require.True(t, marked)
	require.Equal(t, base+1, clineNextEventCount(t, a.ID))
	got, err := repo.GetByID(ctx, a.ID)
	require.NoError(t, err)
	require.Equal(t, "reauth_required", got.GetClineState().CredentialStatus)
	require.Equal(t, original.Status, got.Status)
	require.Equal(t, original.Extra["model_rate_limits"], got.Extra["model_rate_limits"])
	saved, err = repo.SaveClineStateIfUnchanged(ctx, a, state)
	require.NoError(t, err)
	require.False(t, saved, "old successful refresh cannot undo later 401")
	marked, err = repo.MarkClineReauthenticationRequired(ctx, a, now)
	require.NoError(t, err)
	require.False(t, marked)
	rotated := *got
	rotated.Credentials = maps.Clone(got.Credentials)
	rotated.Credentials["api_key"] = "new-token-fixture"
	require.NoError(t, repo.Update(ctx, &rotated))
	base = clineNextEventCount(t, a.ID)
	marked, err = repo.MarkClineReauthenticationRequired(ctx, a, now.Add(2*time.Second))
	require.NoError(t, err)
	require.False(t, marked)
	require.Equal(t, base, clineNextEventCount(t, a.ID))
	got, err = repo.GetByID(ctx, a.ID)
	require.NoError(t, err)
	require.Nil(t, got.GetClineState())
	require.Equal(t, "workos:new-token-fixture", got.GetOpenAIProtocolAPIKey())
}
func TestClinePostgresProviderObservationOwnershipAndOfficialHeaders(t *testing.T) {
	ctx := context.Background()
	repo, a := clinePostgresAccount(t)
	a.Credentials = maps.Clone(a.Credentials)
	a.Credentials["header_override_enabled"] = true
	a.Credentials["header_overrides"] = map[string]any{"HTTP-Referer": "https://app.example.invalid", "X-Title": "fixture"}
	require.NoError(t, repo.Update(ctx, a))
	bad := map[string]any{"HTTP-Referer": "x\r\ny", "X-Title": "fixture"}
	raw, err := json.Marshal(bad)
	require.NoError(t, err)
	_, err = integrationDB.ExecContext(ctx, `UPDATE accounts SET credentials=jsonb_set(credentials,'{header_overrides}',$1::jsonb) WHERE id=$2`, string(raw), a.ID)
	require.Error(t, err)
	now := time.Now().UTC()
	o := cline.ProviderObservation{Requested: []string{"deepseek"}, Actual: "deepseek", ConstraintStatus: "specified", Status: "matched", Source: "provider", HTTPStatus: 200, UpstreamModel: "cline-pass/model", Complete: true, ObservedAt: now}
	require.NoError(t, repo.SaveClineRouteObservation(ctx, a, o))
	got, err := repo.GetByID(ctx, a.ID)
	require.NoError(t, err)
	require.Equal(t, "deepseek", service.ClineRouteForAccount(got, time.Now()).Actual)
	preserved := got.Extra[service.ClineRouteExtraKey]
	require.NoError(t, repo.UpdateExtra(ctx, a.ID, map[string]any{service.ClineRouteExtraKey: nil, "operator_note": "allowed"}))
	_, err = repo.BulkUpdate(ctx, []int64{a.ID}, service.AccountBulkUpdate{Extra: map[string]any{service.ClineRouteExtraKey: "forged"}})
	require.NoError(t, err)
	older := o
	older.ObservedAt = now.Add(-time.Second)
	older.Actual = "other"
	older.Status = "mismatch"
	require.NoError(t, repo.SaveClineRouteObservation(ctx, a, older))
	got, err = repo.GetByID(ctx, a.ID)
	require.NoError(t, err)
	require.Equal(t, preserved, got.Extra[service.ClineRouteExtraKey])
	old := *a
	rotated := *got
	rotated.Credentials = maps.Clone(got.Credentials)
	rotated.Credentials["api_key"] = "new-key-fixture"
	require.NoError(t, repo.Update(ctx, &rotated))
	newer := o
	newer.ObservedAt = now.Add(time.Second)
	require.NoError(t, repo.SaveClineRouteObservation(ctx, &old, newer))
	got, err = repo.GetByID(ctx, a.ID)
	require.NoError(t, err)
	require.Nil(t, service.ClineRouteForAccount(got, time.Now()))
}
