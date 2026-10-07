//go:build integration

package repository

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/cline"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func clineQuotaSubject(t *testing.T, id int64) string {
	t.Helper()
	subject, err := cline.ParseSubjectHash([]byte(fmt.Sprintf(`{"id":"fixture","active_account_id":"quota-recovery-%d"}`, id)))
	require.NoError(t, err)
	t.Cleanup(func() {
		_, err := integrationDB.ExecContext(context.Background(), "DELETE FROM cline_shared_limits WHERE subject_hash=$1", subject)
		require.NoError(t, err)
	})
	return subject
}

func clineQuotaState(a *service.Account, subject string, at time.Time, windows ...cline.Window) *service.ClineState {
	s := clineVerifiedState(a, subject, at)
	s.CredentialStatus, s.QuotaStatus, s.LastSuccessAt, s.Windows = "valid", "ok", &at, windows
	return s
}

func TestClinePostgresMetadataQuotaSharedRecoveryAndStaleObservations(t *testing.T) {
	ctx := context.Background()
	repo, a := clinePostgresAccount(t)
	_, b := clinePostgresAccount(t)
	subject := clineQuotaSubject(t, a.ID)
	for _, account := range []*service.Account{a, b} {
		s := clineQuotaState(account, subject, time.Now().UTC(), cline.Window{Type: "weekly", PercentUsed: 0})
		saved, err := repo.SaveClineStateIfUnchanged(ctx, account, s)
		require.NoError(t, err)
		require.True(t, saved)
		account.Extra[service.ClineStateExtraKey] = s
	}
	reset := time.Now().UTC().Add(3 * 24 * time.Hour)
	exhausted := clineQuotaState(a, subject, time.Now().UTC(), cline.Window{Type: "weekly", PercentUsed: 100, ResetsAt: &reset})
	saved, err := repo.SaveClineStateIfUnchanged(ctx, a, exhausted)
	require.NoError(t, err)
	require.True(t, saved)
	require.ErrorIs(t, repo.CheckClineAdmission(ctx, b, "cline-pass/model"), service.ErrClineObservedCooldown)
	current, err := repo.GetByID(ctx, a.ID)
	require.NoError(t, err)
	require.False(t, current.IsSchedulable())
	// A partial success on the peer cannot clear weekly. Nor may a late, older
	// observation overwrite a newer observation on the same account.
	partial := clineQuotaState(b, subject, time.Now().UTC(), cline.Window{Type: "monthly", PercentUsed: 0})
	saved, err = repo.SaveClineStateIfUnchanged(ctx, b, partial)
	require.NoError(t, err)
	require.True(t, saved)
	require.ErrorIs(t, repo.CheckClineAdmission(ctx, b, "cline-pass/model"), service.ErrClineObservedCooldown)
	old := clineQuotaState(a, subject, exhausted.FetchedAt.Add(-time.Minute), cline.Window{Type: "weekly", PercentUsed: 0})
	saved, err = repo.SaveClineStateIfUnchanged(ctx, a, old)
	require.NoError(t, err)
	require.False(t, saved)
	// A fresh observation of weekly clears metadata-origin shared evidence.
	healthy := clineQuotaState(b, subject, time.Now().UTC(), cline.Window{Type: "weekly", PercentUsed: 0})
	saved, err = repo.SaveClineStateIfUnchanged(ctx, b, healthy)
	require.NoError(t, err)
	require.True(t, saved)
	require.NoError(t, repo.CheckClineAdmission(ctx, b, "cline-pass/model"))
	var count int
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT count(*) FROM cline_shared_limits WHERE subject_hash=$1", subject).Scan(&count))
	require.Zero(t, count)
}

func TestClinePostgresExpiredQuotaRequiresConfirmedWindow(t *testing.T) {
	ctx := context.Background()
	repo, a := clinePostgresAccount(t)
	subject := clineQuotaSubject(t, a.ID)
	now := time.Now().UTC()
	reset := now.Add(-time.Minute)
	state := clineQuotaState(a, subject, now.Add(-2*time.Minute), cline.Window{Type: "weekly", PercentUsed: 100, ResetsAt: &reset})
	saved, err := repo.SaveClineStateIfUnchanged(ctx, a, state)
	require.NoError(t, err)
	require.True(t, saved)
	require.ErrorIs(t, repo.CheckClineAdmission(ctx, a, "cline-pass/model"), service.ErrClineObservedCooldown)
	state = clineQuotaState(a, subject, time.Now().UTC(), cline.Window{Type: "weekly", PercentUsed: 0})
	saved, err = repo.SaveClineStateIfUnchanged(ctx, a, state)
	require.NoError(t, err)
	require.True(t, saved)
	require.NoError(t, repo.CheckClineAdmission(ctx, a, "cline-pass/model"))
}

func TestClinePostgresHealthyMetadataCannotShortenInferenceCooldown(t *testing.T) {
	ctx := context.Background()
	repo, a := clinePostgresAccount(t)
	subject := clineQuotaSubject(t, a.ID)
	state := clineQuotaState(a, subject, time.Now().UTC(), cline.Window{Type: "weekly", PercentUsed: 0})
	saved, err := repo.SaveClineStateIfUnchanged(ctx, a, state)
	require.NoError(t, err)
	require.True(t, saved)
	a.Extra[service.ClineStateExtraKey] = state
	until := time.Now().UTC().Add(time.Hour).Truncate(time.Second)
	require.NoError(t, repo.SetClineRateLimitIfLater(ctx, a, cline.ScopePass+"weekly", until, "pass_limit", true))
	state = clineQuotaState(a, subject, time.Now().UTC(), cline.Window{Type: "weekly", PercentUsed: 0})
	saved, err = repo.SaveClineStateIfUnchanged(ctx, a, state)
	require.NoError(t, err)
	require.True(t, saved)
	require.ErrorIs(t, repo.CheckClineAdmission(ctx, a, "cline-pass/model"), service.ErrClineObservedCooldown)
	var got time.Time
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT reset_at FROM cline_shared_limits WHERE subject_hash=$1 AND scope=$2", subject, cline.ScopePass+"weekly").Scan(&got))
	require.True(t, got.Equal(until))
}

func TestClinePostgresAutomaticMetadataCandidatesIncludeCoolingNotDisabled(t *testing.T) {
	ctx := context.Background()
	repo, a := clinePostgresAccount(t)
	_, b := clinePostgresAccount(t)
	b.Schedulable = false
	require.NoError(t, repo.Update(ctx, b))
	require.NoError(t, repo.SetClineRateLimitIfLater(ctx, a, cline.ScopePass+"monthly", time.Now().Add(20*24*time.Hour), "pass_limit", true))
	page, err := repo.ListClineMetadataCandidates(ctx, a.ID-1, 100)
	require.NoError(t, err)
	seen := map[int64]bool{}
	for _, candidate := range page {
		seen[candidate.ID] = true
	}
	require.True(t, seen[a.ID])
	require.False(t, seen[b.ID])
	_, err = repo.ListClineMetadataCandidates(ctx, 0, 101)
	require.Error(t, err)
	// Metadata Retry-After extends only its own cross-process query lease.
	now := time.Now().UTC()
	claimed, err := repo.ClaimClineMetadataRefresh(ctx, a, now)
	require.NoError(t, err)
	require.True(t, claimed)
	state := clineVerifiedState(a, "", now)
	retry := now.Add(time.Hour)
	state.MetadataRetryAt = &retry
	saved, err := repo.SaveClineStateIfUnchanged(ctx, a, state)
	require.NoError(t, err)
	require.True(t, saved)
	claimed, err = repo.ClaimClineMetadataRefresh(ctx, a, now.Add(31*time.Second))
	require.NoError(t, err)
	require.False(t, claimed)
}
