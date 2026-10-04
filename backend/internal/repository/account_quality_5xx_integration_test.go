//go:build integration

package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestQuality5xxImmediateDuringRunningProbe(t *testing.T) {
	ctx := context.Background()
	cfg := service.DefaultOAuthAutoConfig()
	cfg.UpgradeEnabled = true
	cfg.Revision = "immediate-test"
	cfg.MaxConcurrency = 30
	raw, _ := json.Marshal(cfg)
	var prior string
	err := integrationDB.QueryRowContext(ctx, `SELECT value FROM settings WHERE key=$1`, service.SettingKeyOAuthAutoConfig).Scan(&prior)
	require.True(t, err == nil || err == sql.ErrNoRows)
	had := err == nil
	t.Cleanup(func() {
		if had {
			_, _ = integrationDB.ExecContext(ctx, `UPDATE settings SET value=$2 WHERE key=$1`, service.SettingKeyOAuthAutoConfig, prior)
		} else {
			_, _ = integrationDB.ExecContext(ctx, `DELETE FROM settings WHERE key=$1`, service.SettingKeyOAuthAutoConfig)
		}
	})
	_, err = integrationDB.ExecContext(ctx, `INSERT INTO settings(key,value) VALUES($1,$2) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, service.SettingKeyOAuthAutoConfig, string(raw))
	require.NoError(t, err)
	f := newQualityBPSFixture(t, `{"unrelated":"kept"}`, &service.QualityPolicy{Action: service.QualityActionRemoveModel, TriggerOnUpstream5xx: true, RemoveModels: []string{"gpt-6-astra"}, RecoveryConcurrency: 4, AutoRestore: true})
	f.exec(`UPDATE accounts SET concurrency=17 WHERE id=$1`)
	repo := &accountRepository{sql: integrationDB}
	// f already owns a running probe lease; protection must not wait for it.
	require.NoError(t, repo.ApplyQuality5xx(ctx, f.account))
	var concurrency int
	var schedulable bool
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT concurrency,schedulable FROM accounts WHERE id=$1`, f.account).Scan(&concurrency, &schedulable))
	require.Equal(t, 4, concurrency)
	require.True(t, schedulable)
	extra := f.extra()
	require.Equal(t, "kept", extra["unrelated"])
	require.NotContains(t, extra, "model_rate_limits", "5xx lowers concurrency but is not model degradation evidence")
	require.Equal(t, "stale_run", f.apply("passed"), "probe started before 5xx cannot restore")
	require.NoError(t, f.plans.FinishPelican(ctx, f.plan.ID, f.until, time.Now()))
	f.claim()
	require.Positive(t, f.plan.Quality5xxEpisode)
	firstEpisode := f.plan.Quality5xxEpisode
	require.NoError(t, repo.ApplyQuality5xx(ctx, f.account))
	require.Equal(t, "stale_run", f.apply("passed"), "second 5xx must invalidate running recovery probe")
	require.NoError(t, f.plans.FinishPelican(ctx, f.plan.ID, f.until, time.Now()))
	f.claim()
	require.Greater(t, f.plan.Quality5xxEpisode, firstEpisode)
	require.Equal(t, "model_cooldown_refreshed", f.apply("failed"))
	require.Equal(t, "recovery_started", f.apply("passed"))
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT concurrency FROM accounts WHERE id=$1`, f.account).Scan(&concurrency))
	require.Equal(t, 4, concurrency, "probe only unlocks native ramp")
	extra = f.extra()
	require.NotContains(t, extra, "model_rate_limits")
	ramp := extra[service.AutoConfigConcurrencyExtraKey].(map[string]any)
	require.EqualValues(t, 17, ramp["recovery_target"])
}

func TestQualityModelResultsRemainIndependentInTransaction(t *testing.T) {
	f := newQualityBPSFixture(t, `{"unrelated":"kept"}`, &service.QualityPolicy{Action: service.QualityActionRemoveModel, RemoveModels: []string{"gpt-6-astra", "gpt-6.1-sol"}, AutoRestore: true, RecoveryConcurrency: 4})
	f.exec(`UPDATE accounts SET concurrency=20 WHERE id=$1`)
	f.plan.QualityModelOutcomes = map[string]string{"gpt-6-astra": "failed", "gpt-6.1-sol": "passed"}
	require.Equal(t, "models_cooled", f.apply("failed"))
	limits := f.extra()["model_rate_limits"].(map[string]any)
	require.Contains(t, limits, "gpt-6-astra")
	require.NotContains(t, limits, "gpt-6.1-sol")
	f.plan.QualityModelOutcomes = map[string]string{"gpt-6-astra": "failed", "gpt-6.1-sol": "failed"}
	f.apply("failed")
	require.Len(t, f.extra()["model_rate_limits"].(map[string]any), 2)
	f.plan.QualityModelOutcomes = map[string]string{"gpt-6-astra": "passed", "gpt-6.1-sol": "failed"}
	f.apply("failed")
	limits = f.extra()["model_rate_limits"].(map[string]any)
	require.NotContains(t, limits, "gpt-6-astra")
	require.Contains(t, limits, "gpt-6.1-sol")
	f.plan.QualityModelOutcomes = map[string]string{"gpt-6-astra": "passed", "gpt-6.1-sol": "passed"}
	require.Equal(t, "restored", f.apply("passed"))
	require.NotContains(t, f.extra(), "model_rate_limits")
}

func TestQualitySkippedRoundHistoryCountsOnlyTestedModels(t *testing.T) {
	f := newQualityBPSFixture(t, `{}`, &service.QualityPolicy{Action: service.QualityActionRemoveModel, RemoveModels: []string{"gpt-6-astra"}, AutoRestore: true})
	results := NewScheduledTestResultRepository(integrationDB)
	svc := service.NewScheduledTestService(f.plans, results)
	cfg := *f.plan.PelicanConfig
	cfg.ModelIDs = []string{"gpt-6-astra", "gpt-6.1-sol"}
	cfg.ParallelCount = 1
	now := time.Now()
	for _, status := range []string{"success", "skipped"} {
		_, err := results.Create(context.Background(), &service.ScheduledTestResult{PlanID: f.plan.ID, QualityRoundID: "supported-model-round", Status: status, PelicanConfig: &cfg, StartedAt: now, FinishedAt: now})
		require.NoError(t, err)
	}
	page, err := svc.ListQualityHistory(context.Background(), 0)
	require.NoError(t, err)
	require.NotEmpty(t, page.Items)
	require.Equal(t, 1, page.Items[0].PassedCount)
	require.Equal(t, 1, page.Items[0].TotalCount)
	require.Equal(t, 1, page.Items[0].SkippedCount)
	require.Equal(t, "success", page.Items[0].Status)
}
