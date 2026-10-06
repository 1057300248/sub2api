//go:build integration

package repository

import (
	"context"
	"fmt"
	"sync"
	"testing"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func clineQuotaOutboxCount(t *testing.T, id int64) int {
	t.Helper()
	var count int
	require.NoError(t, integrationDB.QueryRowContext(context.Background(), "SELECT count(*) FROM scheduler_outbox WHERE account_id=$1 AND event_type=$2", id, service.SchedulerOutboxEventAccountChanged).Scan(&count))
	return count
}

func TestClinePostgresQuotaOutboxEveryDimensionAndPeriod(t *testing.T) {
	ctx := context.Background()
	for _, dimension := range []string{"quota", "quota_daily", "quota_weekly"} {
		for _, mode := range []string{"rolling", "fixed"} {
			t.Run(dimension+"/"+mode, func(t *testing.T) {
				repo, a := clinePostgresAccount(t)
				settings := map[string]any{dimension + "_limit": 10.0}
				if dimension != "quota" {
					settings[dimension+"_reset_mode"] = mode
					settings["quota_reset_timezone"] = "Europe/Berlin"
				}
				require.NoError(t, repo.UpdateExtra(ctx, a.ID, settings))
				baseline := clineQuotaOutboxCount(t, a.ID)
				require.NoError(t, repo.IncrementQuotaUsed(ctx, a.ID, 9))
				require.Equal(t, baseline, clineQuotaOutboxCount(t, a.ID))
				require.NoError(t, repo.IncrementQuotaUsed(ctx, a.ID, 1))
				require.Equal(t, baseline+1, clineQuotaOutboxCount(t, a.ID))
				require.NoError(t, repo.IncrementQuotaUsed(ctx, a.ID, 1))
				require.Equal(t, baseline+1, clineQuotaOutboxCount(t, a.ID), "already-exceeded dimensions do not flood outbox")
				if dimension != "quota" {
					_, err := integrationDB.ExecContext(ctx, fmt.Sprintf(`UPDATE accounts SET extra=extra||jsonb_build_object('%s_start','2000-01-01T00:00:00Z','%s_reset_at','2000-01-01T00:00:00Z') WHERE id=$1`, dimension, dimension), a.ID)
					require.NoError(t, err)
					require.NoError(t, repo.IncrementQuotaUsed(ctx, a.ID, 10))
					require.Equal(t, baseline+2, clineQuotaOutboxCount(t, a.ID), "a new period can cross the threshold again")
					got, err := repo.GetByID(ctx, a.ID)
					require.NoError(t, err)
					require.Equal(t, 10.0, got.Extra[dimension+"_used"])
				}
			})
		}
	}
}

func TestClinePostgresQuotaOutboxConcurrentCrossingAndUnlimited(t *testing.T) {
	ctx := context.Background()
	repo, a := clinePostgresAccount(t)
	require.NoError(t, repo.UpdateExtra(ctx, a.ID, map[string]any{"quota_daily_limit": 10.0, "quota_weekly_limit": 10.0}))
	baseline := clineQuotaOutboxCount(t, a.ID)
	var wg sync.WaitGroup
	errs := make(chan error, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); errs <- repo.IncrementQuotaUsed(ctx, a.ID, 1) }()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	got, err := repo.GetByID(ctx, a.ID)
	require.NoError(t, err)
	require.Equal(t, 20.0, got.GetQuotaDailyUsed())
	require.Equal(t, 20.0, got.GetQuotaWeeklyUsed())
	require.Equal(t, baseline+1, clineQuotaOutboxCount(t, a.ID), "simultaneous dimensions produce one atomic event")
	_, b := clinePostgresAccount(t)
	baseline = clineQuotaOutboxCount(t, b.ID)
	require.NoError(t, repo.IncrementQuotaUsed(ctx, b.ID, 100))
	require.Equal(t, baseline, clineQuotaOutboxCount(t, b.ID), "absent limits are unlimited")
}

func TestClinePostgresQuotaOutboxFailureAndOuterRollback(t *testing.T) {
	ctx := context.Background()
	repo, a := clinePostgresAccount(t)
	require.NoError(t, repo.UpdateExtra(ctx, a.ID, map[string]any{"quota_daily_limit": 1.0}))
	baseline := clineQuotaOutboxCount(t, a.ID)
	tx, err := repo.client.Tx(ctx)
	require.NoError(t, err)
	require.NoError(t, repo.IncrementQuotaUsed(dbent.NewTxContext(ctx, tx), a.ID, 1))
	require.NoError(t, tx.Rollback())
	got, err := repo.GetByID(ctx, a.ID)
	require.NoError(t, err)
	require.Zero(t, got.GetQuotaUsed())
	require.Equal(t, baseline, clineQuotaOutboxCount(t, a.ID))
	fn := fmt.Sprintf("cline_quota_outbox_%d", a.ID)
	_, err = integrationDB.ExecContext(ctx, fmt.Sprintf(`CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.account_id=%d THEN RAISE EXCEPTION 'fixture outbox failure'; END IF; RETURN NEW; END $$; CREATE TRIGGER %s BEFORE INSERT ON scheduler_outbox FOR EACH ROW EXECUTE FUNCTION %s();`, fn, a.ID, fn, fn))
	require.NoError(t, err)
	t.Cleanup(func() {
		_, err := integrationDB.ExecContext(ctx, fmt.Sprintf("DROP TRIGGER IF EXISTS %s ON scheduler_outbox; DROP FUNCTION IF EXISTS %s();", fn, fn))
		require.NoError(t, err)
	})
	require.Error(t, repo.IncrementQuotaUsed(ctx, a.ID, 1))
	got, err = repo.GetByID(ctx, a.ID)
	require.NoError(t, err)
	require.Zero(t, got.GetQuotaUsed())
	require.Zero(t, got.GetQuotaDailyUsed())
	require.Equal(t, baseline, clineQuotaOutboxCount(t, a.ID))
}

func TestClinePostgresAttributionHeaderGuard(t *testing.T) {
	ctx := context.Background()
	_, a := clinePostgresAccount(t)
	_, err := integrationDB.ExecContext(ctx, `UPDATE accounts SET credentials=credentials||'{"header_overrides":{"HTTP-Referer":"https://gateway.example.invalid","X-Title":"My Gateway"},"header_override_enabled":true}'::jsonb WHERE id=$1`, a.ID)
	require.NoError(t, err)
	for _, headers := range []string{`{"X-Title":"a","x-title":"b"}`, `{"Authorization":"invalid"}`, `{"HTTP-Referer":"line\r\nInjected: yes"}`} {
		_, err = integrationDB.ExecContext(ctx, `UPDATE accounts SET credentials=jsonb_set(credentials,'{header_overrides}',$1::jsonb) WHERE id=$2`, headers, a.ID)
		require.Error(t, err)
	}
}
