//go:build integration

package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/pkg/cline"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestClinePostgresAdvancedSettingsGuardsAndBulkAtomicity(t *testing.T) {
	ctx := context.Background()
	repo, a := clinePostgresAccount(t)
	_, b := clinePostgresAccount(t)
	_, err := integrationDB.ExecContext(ctx, "UPDATE accounts SET platform='openai' WHERE id=$1", b.ID)
	require.NoError(t, err)
	for _, bad := range []map[string]any{{"quota_limit": -1}, {"quota_daily_limit": "10"}, {"quota_daily_reset_hour": 24}, {"quota_weekly_reset_day": 0.5}, {"quota_reset_timezone": "Invalid/Zone"}, {"quota_notify_daily_enabled": 0}, {"quota_notify_weekly_threshold": 101, "quota_notify_weekly_threshold_type": "percentage"}} {
		_, err = repo.BulkUpdate(ctx, []int64{a.ID, b.ID}, service.AccountBulkUpdate{Extra: bad})
		require.Error(t, err)
		other, err := repo.GetByID(ctx, b.ID)
		require.NoError(t, err)
		for k := range bad {
			require.NotContains(t, other.Extra, k, "mixed statement must roll back")
		}
	}
	for _, bad := range []map[string]any{{"custom_error_codes_enabled": true, "custom_error_codes": []int{}}, {"custom_error_codes": []int{503, 503}}, {"temp_unschedulable_enabled": true, "temp_unschedulable_rules": []any{}}, {"temp_unschedulable_rules": []any{map[string]any{"error_code": 503, "duration_minutes": 0, "keywords": []string{"bad"}}}}} {
		body, e := json.Marshal(bad)
		require.NoError(t, e)
		_, err = integrationDB.ExecContext(ctx, "UPDATE accounts SET credentials=credentials||$1::jsonb WHERE id IN ($2,$3)", string(body), a.ID, b.ID)
		require.Error(t, err)
		other, err := repo.GetByID(ctx, b.ID)
		require.NoError(t, err)
		for k := range bad {
			require.NotContains(t, other.Credentials, k)
		}
	}
	good := map[string]any{"quota_limit": 100.0, "quota_daily_limit": 10.0, "quota_weekly_limit": 50.0, "quota_daily_reset_mode": "fixed", "quota_daily_reset_hour": 0, "quota_weekly_reset_mode": "fixed", "quota_weekly_reset_day": 0, "quota_weekly_reset_hour": 0, "quota_reset_timezone": "Europe/Berlin", "quota_notify_total_enabled": false}
	_, err = repo.BulkUpdate(ctx, []int64{a.ID}, service.AccountBulkUpdate{Extra: good})
	require.NoError(t, err)
	got, err := repo.GetByID(ctx, a.ID)
	require.NoError(t, err)
	for _, key := range []string{"quota_daily_reset_at", "quota_weekly_reset_at"} {
		at, e := time.Parse(time.RFC3339, got.Extra[key].(string))
		require.NoError(t, e)
		require.True(t, at.After(time.Now()))
	}
	require.Equal(t, float64(0), got.Extra["quota_weekly_reset_day"])
	_, err = repo.BulkUpdate(ctx, []int64{a.ID}, service.AccountBulkUpdate{Extra: map[string]any{"quota_limit": nil, "quota_daily_reset_mode": "rolling"}})
	require.NoError(t, err)
	got, err = repo.GetByID(ctx, a.ID)
	require.NoError(t, err)
	require.NotContains(t, got.Extra, "quota_limit")
	require.NotContains(t, got.Extra, "quota_daily_reset_at")
}

func TestClinePostgresLocalQuotaCountersCannotBeReplayed(t *testing.T) {
	ctx := context.Background()
	repo, a := clinePostgresAccount(t)
	require.NoError(t, repo.UpdateExtra(ctx, a.ID, map[string]any{"quota_limit": 100.0, "quota_daily_limit": 20.0, "quota_weekly_limit": 50.0}))
	stale, err := repo.GetByID(ctx, a.ID)
	require.NoError(t, err)
	require.NoError(t, repo.IncrementQuotaUsed(ctx, a.ID, 7.0))
	stale.Name = "edited after usage"
	stale.Extra["quota_used"] = 0.0
	require.NoError(t, repo.Update(ctx, stale))
	require.NoError(t, repo.UpdateExtra(ctx, a.ID, map[string]any{"quota_used": 0.0, "quota_daily_used": 0.0, "quota_weekly_used": 0.0, "quota_limit": 101.0}))
	_, err = repo.BulkUpdate(ctx, []int64{a.ID}, service.AccountBulkUpdate{Extra: map[string]any{"quota_used": 0.0, "quota_daily_start": nil, "quota_daily_limit": 21.0}})
	require.NoError(t, err)
	got, err := repo.GetByID(ctx, a.ID)
	require.NoError(t, err)
	require.Equal(t, 7.0, got.GetQuotaUsed())
	require.Equal(t, 7.0, got.GetQuotaDailyUsed())
	require.Equal(t, 7.0, got.GetQuotaWeeklyUsed())
	require.Equal(t, 101.0, got.GetQuotaLimit())
	require.Equal(t, 21.0, got.GetQuotaDailyLimit())
}

func TestClinePostgresAdvancedOperationsPreservePassAndOtherBlocks(t *testing.T) {
	ctx := context.Background()
	repo, a := clinePostgresAccount(t)
	subject := clineQuotaSubject(t, a.ID)
	now := time.Now().UTC()
	state := clineQuotaState(a, subject, now, cline.Window{Type: "weekly", PercentUsed: 20})
	saved, err := repo.SaveClineStateIfUnchanged(ctx, a, state)
	require.NoError(t, err)
	require.True(t, saved)
	a, err = repo.GetByID(ctx, a.ID)
	require.NoError(t, err)
	require.NoError(t, repo.SetClineRateLimitIfLater(ctx, a, cline.ScopePass+"weekly", now.Add(3*24*time.Hour), "pass_limit", true))
	require.NoError(t, repo.UpdateExtra(ctx, a.ID, map[string]any{"quota_limit": 10.0, "quota_daily_limit": 10.0}))
	require.NoError(t, repo.IncrementQuotaUsed(ctx, a.ID, 10.0))
	require.NoError(t, repo.SetTempUnschedulable(ctx, a.ID, now.Add(time.Hour), "local pause"))
	before, err := repo.GetByID(ctx, a.ID)
	require.NoError(t, err)
	require.NoError(t, repo.ResetClineLocalQuota(ctx, a.ID))
	after, err := repo.GetByID(ctx, a.ID)
	require.NoError(t, err)
	require.Zero(t, after.GetQuotaUsed())
	require.Zero(t, after.GetQuotaDailyUsed())
	require.Equal(t, before.Extra["cline_state"], after.Extra["cline_state"])
	require.Equal(t, before.Extra["model_rate_limits"], after.Extra["model_rate_limits"])
	require.Equal(t, before.TempUnschedulableUntil, after.TempUnschedulableUntil)
	handled, err := repo.ClearClineTemporaryPause(ctx, a.ID)
	require.NoError(t, err)
	require.True(t, handled)
	require.NoError(t, repo.ClearModelRateLimits(ctx, a.ID)) // generic recovery must not clear Cline scopes either
	after, err = repo.GetByID(ctx, a.ID)
	require.NoError(t, err)
	require.Nil(t, after.TempUnschedulableUntil)
	require.Equal(t, before.Extra["model_rate_limits"], after.Extra["model_rate_limits"])
	require.ErrorIs(t, repo.CheckClineAdmission(ctx, after, "cline-pass/model"), service.ErrClineObservedCooldown)
	var shared int
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT count(*) FROM cline_shared_limits WHERE subject_hash=$1", subject).Scan(&shared))
	require.Positive(t, shared)
}

func TestClinePostgresLocalQuotaFinalAdmissionAndResetRollback(t *testing.T) {
	ctx := context.Background()
	repo, a := clinePostgresAccount(t)
	subject := clineQuotaSubject(t, a.ID)
	state := clineQuotaState(a, subject, time.Now().UTC(), cline.Window{Type: "weekly", PercentUsed: 20})
	saved, err := repo.SaveClineStateIfUnchanged(ctx, a, state)
	require.NoError(t, err)
	require.True(t, saved)
	require.NoError(t, repo.UpdateExtra(ctx, a.ID, map[string]any{"quota_limit": 1.0}))
	snapshot, err := repo.GetByID(ctx, a.ID)
	require.NoError(t, err)
	require.True(t, snapshot.IsSchedulable())
	require.NoError(t, repo.IncrementQuotaUsed(ctx, a.ID, 1.0))
	require.ErrorIs(t, repo.CheckClineAdmission(ctx, snapshot, "cline-pass/model"), service.ErrClineLocalQuotaExceeded)
	tx, err := repo.client.Tx(ctx)
	require.NoError(t, err)
	txCtx := dbent.NewTxContext(ctx, tx)
	require.NoError(t, repo.ResetClineLocalQuota(txCtx, a.ID))
	require.NoError(t, tx.Rollback())
	require.ErrorIs(t, repo.CheckClineAdmission(ctx, snapshot, "cline-pass/model"), service.ErrClineLocalQuotaExceeded)
	// A failure inserting this account's outbox event rolls back the counters.
	fn := fmt.Sprintf("cline_local_reset_%d", a.ID)
	_, err = integrationDB.ExecContext(ctx, fmt.Sprintf(`CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.account_id=%d THEN RAISE EXCEPTION 'fixture outbox failure'; END IF; RETURN NEW; END $$; CREATE TRIGGER %s BEFORE INSERT ON scheduler_outbox FOR EACH ROW EXECUTE FUNCTION %s();`, fn, a.ID, fn, fn))
	require.NoError(t, err)
	t.Cleanup(func() {
		_, e := integrationDB.ExecContext(ctx, fmt.Sprintf("DROP TRIGGER IF EXISTS %s ON scheduler_outbox; DROP FUNCTION IF EXISTS %s();", fn, fn))
		require.NoError(t, e)
	})
	require.Error(t, repo.ResetClineLocalQuota(ctx, a.ID))
	got, err := repo.GetByID(ctx, a.ID)
	require.NoError(t, err)
	require.Equal(t, 1.0, got.GetQuotaUsed())
	_, err = integrationDB.ExecContext(ctx, fmt.Sprintf("DROP TRIGGER %s ON scheduler_outbox; DROP FUNCTION %s();", fn, fn))
	require.NoError(t, err)
	require.NoError(t, repo.ResetClineLocalQuota(ctx, a.ID))
	require.NoError(t, repo.CheckClineAdmission(ctx, snapshot, "cline-pass/model"))
}
