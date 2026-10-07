package repository

import (
	"context"
	"encoding/json"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/pkg/cline"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func clineRepositoryFixture(t *testing.T) *service.Account {
	t.Helper()
	a := &service.Account{ID: 27, Name: "Cline fixture", Platform: service.PlatformCline, Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true, Concurrency: 1,
		Credentials: map[string]any{"api_key": "fixture-not-a-real-key", "account_mode": cline.ModePass, "cline_auth_type": cline.AuthAccountToken, "model_mapping": map[string]any{"public-model": "cline-pass/model"}}}
	require.NoError(t, service.NormalizeClineCredentials(a.Platform, a.Type, a.Credentials))
	return a
}

func TestClineSchedulerProjectionRetainsQuotaIdentity(t *testing.T) {
	a := clineRepositoryFixture(t)
	until := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	a.Extra = map[string]any{"model_rate_limits": map[string]any{service.ClineRateLimitScope(a, cline.ScopePass+"weekly"): map[string]any{"rate_limit_reset_at": until}}}
	a.Credentials["private_unrelated"] = "not-needed-in-candidates"
	metadata := buildSchedulerMetadataAccount(*a)
	require.Equal(t, service.PlatformCline, metadata.Platform)
	require.Equal(t, a.GetClineMode(), metadata.GetClineMode())
	require.Equal(t, service.ClineCredentialFingerprint(a), service.ClineCredentialFingerprint(&metadata))
	require.True(t, metadata.IsClineModelSupported("public-model"))
	require.Equal(t, a.Extra["model_rate_limits"], metadata.Extra["model_rate_limits"])
	require.NotContains(t, metadata.Credentials, "private_unrelated")
	a.Platform = service.PlatformDeepseek
	require.Equal(t, filterSchedulerCredentials(a.Credentials), filterSchedulerAccountCredentials(a))
}

func TestClineAccountWriteEntryPointsValidateBeforeDatabase(t *testing.T) {
	repo := newAccountRepositoryWithSQL(nil, nil, nil)
	require.ErrorIs(t, repo.Create(context.Background(), nil), service.ErrAccountNilInput)
	require.ErrorIs(t, repo.Update(context.Background(), nil), service.ErrAccountNilInput)
	require.ErrorIs(t, repo.UpdateWithAccountBillingSettings(context.Background(), nil, nil, nil, nil), service.ErrAccountNilInput)
	for _, update := range []bool{false, true} {
		a := clineRepositoryFixture(t)
		a.Credentials["api_protocol"] = "responses"
		if update {
			require.Error(t, repo.UpdateWithAccountBillingSettings(context.Background(), a, nil, nil, nil))
		} else {
			require.Error(t, createAccountRecord(context.Background(), nil, a))
		}
	}
}

func TestClineLockedEditPreservesLatestManagedState(t *testing.T) {
	client, mock := newOllamaCloudUsageRepositoryTestClient(t)
	a := clineRepositoryFixture(t)
	a.Extra = map[string]any{service.ClineStateExtraKey: "forged", "model_rate_limits": nil, "operator_note": "allowed"}
	credentials, err := json.Marshal(a.Credentials)
	require.NoError(t, err)
	mock.ExpectQuery(`(?s)`+regexp.QuoteMeta("SELECT")+`.*`+regexp.QuoteMeta("FOR NO KEY UPDATE")).
		WithArgs(a.ID, a.Platform, a.Type, string(credentials), nil).
		WillReturnRows(sqlmock.NewRows([]string{"identity", "ollama_group", "ollama_proxy", "enabled", "sync", "probe", "ollama_session", "ollama_auto", "ollama_snapshot", "opencode_group", "opencode_auto", "opencode_snapshot", "extra"}).
			AddRow(true, false, true, nil, nil, nil, nil, nil, nil, false, nil, nil, []byte(`{"cline_state":"latest","model_rate_limits":{"scope":"latest-limit"}}`)))
	extra, err := lockAndMergeAccountProbeExtra(context.Background(), client, a, nil, nil)
	require.NoError(t, err)
	require.Equal(t, "latest", extra[service.ClineStateExtraKey])
	require.Equal(t, map[string]any{"scope": "latest-limit"}, extra["model_rate_limits"])
	require.Equal(t, "allowed", extra["operator_note"])
	require.Equal(t, "forged", a.Extra[service.ClineStateExtraKey])
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestClineManagedExtraDeltaIsPlatformScoped(t *testing.T) {
	require.Equal(t, "$1::jsonb", clineExtraUpdateSQL("$1", map[string]any{"operator_note": "not managed"}))
	require.Equal(t, clineManagedExtraDeltaSQL("$1"), clineExtraUpdateSQL("$1", map[string]any{"cline_state": nil}))
	require.Equal(t, clineManagedExtraDeltaSQL("$1"), clineExtraUpdateSQL("$1", map[string]any{"model_rate_limits": nil}))
	require.Equal(t, "(CASE WHEN platform = 'cline' THEN $1::jsonb - 'cline_state' - 'cline_route' - 'model_rate_limits' - 'quota_used' - 'quota_daily_used' - 'quota_weekly_used' - 'quota_daily_start' - 'quota_weekly_start' - 'quota_daily_reset_at' - 'quota_weekly_reset_at' ELSE $1::jsonb END)", clineManagedExtraDeltaSQL("$1"))
	for _, key := range []string{"quota_used", "quota_daily_used", "quota_weekly_used", "quota_daily_start", "quota_weekly_start", "quota_daily_reset_at", "quota_weekly_reset_at"} {
		require.Equal(t, clineManagedExtraDeltaSQL("$1"), clineExtraUpdateSQL("$1", map[string]any{key: nil}), key)
	}
	exec := &recordingSQLExecutor{result: rowsAffectedResult(1)}
	repo := newAccountRepositoryWithSQL(nil, exec, nil)
	_, err := repo.BulkUpdate(context.Background(), []int64{27}, service.AccountBulkUpdate{Extra: map[string]any{"cline_state": "forged", "model_rate_limits": nil}})
	require.NoError(t, err)
	require.Contains(t, exec.execQueries[0], clineManagedExtraDeltaSQL("$1"))
}
