//go:build integration

package repository

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/pkg/cline"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func clineVerifiedState(account *service.Account, subject string, at time.Time) *service.ClineState {
	return &service.ClineState{Mode: account.GetClineMode(), AuthType: account.GetCredential("cline_auth_type"), Transport: "chat_completions", Identity: subject, IdentityVerifiedAt: &at, FetchedAt: &at, CredentialFingerprint: service.ClineCredentialFingerprint(account), QuotaStatus: "unknown", Persisted: true}
}

func TestClinePostgresSharedSubjectLimitsAndRestart(t *testing.T) {
	ctx := context.Background()
	repo, a := clinePostgresAccount(t)
	_, b := clinePostgresAccount(t)
	b.Credentials["api_key"] = "second-synthetic-key"
	require.NoError(t, repo.Update(ctx, b))
	subject, err := cline.ParseSubjectHash([]byte(fmt.Sprintf(`{"id":"test-user","active_account_id":"test-account-%d"}`, a.ID)))
	require.NoError(t, err)
	t.Cleanup(func() {
		_, err := integrationDB.ExecContext(ctx, "DELETE FROM cline_shared_limits WHERE subject_hash=$1", subject)
		require.NoError(t, err)
	})
	for _, account := range []*service.Account{a, b} {
		state := clineVerifiedState(account, subject, time.Now().UTC())
		saved, err := repo.SaveClineStateIfUnchanged(ctx, account, state)
		account.Extra[service.ClineStateExtraKey] = state
		require.NoError(t, err)
		require.True(t, saved)
	}
	until := time.Now().UTC().Add(7 * 24 * time.Hour).Truncate(time.Second)
	require.NoError(t, repo.SetClineRateLimitIfLater(ctx, a, cline.ScopePass+"weekly", until, "pass_limit", true))
	// No Redis or process-local quota state is carried into this repository.
	restarted := &accountRepository{client: repo.client, sql: repo.sql}
	b.Extra = nil
	require.ErrorIs(t, restarted.CheckClineAdmission(ctx, b, "cline-pass/model"), service.ErrClineObservedCooldown)
	require.NoError(t, repo.SetClineRateLimitIfLater(ctx, b, cline.ScopePass+"weekly", time.Now().Add(time.Minute), "retry_only", false))
	var reset time.Time
	var authoritative bool
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT reset_at,reset_authoritative FROM cline_shared_limits WHERE subject_hash=$1 AND account_mode='pass' AND scope=$2", subject, cline.ScopePass+"weekly").Scan(&reset, &authoritative))
	require.True(t, reset.Equal(until))
	require.True(t, authoritative)
}

func TestClinePostgresRefreshLeaseAndObservationOrdering(t *testing.T) {
	ctx := context.Background()
	repo, a := clinePostgresAccount(t)
	now := time.Now().UTC()
	var winners atomic.Int32
	var wg sync.WaitGroup
	errors := make(chan error, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			won, err := repo.ClaimClineMetadataRefresh(ctx, a, now)
			if err != nil {
				errors <- err
			}
			if won {
				winners.Add(1)
			}
		}()
	}
	wg.Wait()
	close(errors)
	for err := range errors {
		require.NoError(t, err)
	}
	require.Equal(t, int32(1), winners.Load())
	won, err := repo.ClaimClineMetadataRefresh(ctx, a, now.Add(31*time.Second))
	require.NoError(t, err)
	require.True(t, won)
	state := clineVerifiedState(a, "", now)
	saved, err := repo.SaveClineStateIfUnchanged(ctx, a, state)
	require.NoError(t, err)
	require.True(t, saved)
	older := now.Add(-time.Minute)
	state.FetchedAt = &older
	saved, err = repo.SaveClineStateIfUnchanged(ctx, a, state)
	require.NoError(t, err)
	require.False(t, saved)
	a.Credentials["api_key"] = "rotated-lease-key"
	require.NoError(t, repo.Update(ctx, a))
	won, err = repo.ClaimClineMetadataRefresh(ctx, a, now)
	require.NoError(t, err)
	require.True(t, won)
}

func TestClinePostgresOutboxFailureRollsBackState(t *testing.T) {
	ctx := context.Background()
	repo, a := clinePostgresAccount(t)
	for _, kind := range []string{"limit", "metadata"} {
		t.Run(kind, func(t *testing.T) {
			tx, err := repo.client.Tx(ctx)
			require.NoError(t, err)
			defer func() { _ = tx.Rollback() }()
			txCtx := dbent.NewTxContext(ctx, tx)
			_, err = tx.Client().ExecContext(txCtx, fmt.Sprintf("ALTER TABLE scheduler_outbox ADD CONSTRAINT cline_test_outbox_reject CHECK (account_id IS DISTINCT FROM %d) NOT VALID", a.ID))
			require.NoError(t, err)
			if kind == "limit" {
				err = repo.SetClineRateLimitIfLater(txCtx, a, cline.ScopePass+"monthly", time.Now().Add(time.Hour), "pass_limit", true)
			} else {
				_, err = repo.SaveClineStateIfUnchanged(txCtx, a, clineVerifiedState(a, "", time.Now().UTC()))
			}
			require.Error(t, err)
			require.NoError(t, tx.Rollback())
			current, err := repo.GetByID(ctx, a.ID)
			require.NoError(t, err)
			require.Nil(t, current.GetClineState())
			require.Zero(t, current.GetModelRateLimitRemainingTime("public-model"))
		})
	}
}

func TestClinePostgresDirectAndBulkCredentialGuards(t *testing.T) {
	ctx := context.Background()
	repo, a := clinePostgresAccount(t)
	_, b := clinePostgresAccount(t)
	for _, bad := range []map[string]any{
		{"api_protocol": "responses"}, {"cline_paid_fallback": true}, {"cline_free_api_enabled": true},
		{"account_mode": "coding"}, {"cline_auth_type": nil}, {"model_mapping": map[string]any{"*": "cline-pass/model"}},
		{"model_mapping": map[string]any{"public-model": "vendor/paid"}}, {"base_url": "https://key@api.cline.bot"},
	} {
		credentials := make(map[string]any, len(a.Credentials))
		for key, value := range a.Credentials {
			credentials[key] = value
		}
		for key, value := range bad {
			credentials[key] = value
		}
		require.Error(t, repo.UpdateCredentials(ctx, a.ID, credentials))
		_, err := repo.BulkUpdate(ctx, []int64{a.ID, b.ID}, service.AccountBulkUpdate{Credentials: bad})
		require.Error(t, err)
		for _, expected := range []*service.Account{a, b} {
			current, err := repo.GetByID(ctx, expected.ID)
			require.NoError(t, err)
			require.Equal(t, expected.Credentials, current.Credentials)
		}
	}
	valid := make(map[string]any, len(a.Credentials))
	for key, value := range a.Credentials {
		valid[key] = value
	}
	valid["api_key"] = "valid-direct-rotation"
	require.NoError(t, repo.UpdateCredentials(ctx, a.ID, valid))
	updated, err := repo.GetByID(ctx, a.ID)
	require.NoError(t, err)
	require.Equal(t, "valid-direct-rotation", updated.GetCredential("api_key"))
}

func TestClinePostgresAdmissionRejectsStaleCredentials(t *testing.T) {
	ctx := context.Background()
	repo, a := clinePostgresAccount(t)
	stale := *a
	stale.Credentials = make(map[string]any, len(a.Credentials))
	for key, value := range a.Credentials {
		stale.Credentials[key] = value
	}
	a.Credentials["api_key"] = "next-admission-key"
	require.NoError(t, repo.Update(ctx, a))
	require.ErrorIs(t, repo.CheckClineAdmission(ctx, &stale, "cline-pass/model"), service.ErrClineMetadataChanged)
}

func TestClinePostgresIdentitySwitchDoesNotReattributeOldLimits(t *testing.T) {
	ctx := context.Background()
	repo, a := clinePostgresAccount(t)
	first, err := cline.ParseSubjectHash([]byte(fmt.Sprintf(`{"id":"user","active_account_id":"first-%d"}`, a.ID)))
	require.NoError(t, err)
	second, err := cline.ParseSubjectHash([]byte(fmt.Sprintf(`{"id":"user","active_account_id":"second-%d"}`, a.ID)))
	require.NoError(t, err)
	t.Cleanup(func() {
		_, err := integrationDB.ExecContext(ctx, "DELETE FROM cline_shared_limits WHERE subject_hash=$1 OR subject_hash=$2", first, second)
		require.NoError(t, err)
	})
	now := time.Now().UTC()
	state := clineVerifiedState(a, first, now)
	saved, err := repo.SaveClineStateIfUnchanged(ctx, a, state)
	require.NoError(t, err)
	require.True(t, saved)
	a.Extra[service.ClineStateExtraKey] = state
	require.NoError(t, repo.SetClineRateLimitIfLater(ctx, a, cline.ScopePass+"weekly", time.Now().Add(time.Hour), "pass_limit", true))
	saved, err = repo.SaveClineStateIfUnchanged(ctx, a, clineVerifiedState(a, second, now.Add(time.Second)))
	require.NoError(t, err)
	require.True(t, saved)
	// a still carries the pre-send first-subject snapshot. Its late response
	// may extend its conservative local cooldown but must not poison subject 2.
	require.NoError(t, repo.SetClineRateLimitIfLater(ctx, a, cline.ScopePass+"monthly", time.Now().Add(2*time.Hour), "pass_limit", true))
	var count int
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM cline_shared_limits WHERE subject_hash=$1", second).Scan(&count))
	require.Zero(t, count)
}
