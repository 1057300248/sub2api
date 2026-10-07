//go:build integration

package repository

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/incidentdiag"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestClinePostgresLegacyIncidentCooldownIsMonotonicAndDurable(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	repo := newAccountRepositoryWithSQL(client, integrationDB, nil)
	a := &service.Account{Name: "cline-legacy-reset-" + time.Now().Format("150405.000000000"), Platform: service.PlatformDeepseek, Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true, Concurrency: 1, Credentials: map[string]any{"base_url": "https://api.cline.bot/api/v1", "api_key": "fixture-not-a-real-key"}, Extra: map[string]any{}}
	require.NoError(t, repo.Create(ctx, a))
	t.Cleanup(func() {
		_, err := integrationDB.ExecContext(ctx, "DELETE FROM scheduler_outbox WHERE account_id=$1", a.ID)
		require.NoError(t, err)
		require.NoError(t, client.Account.DeleteOneID(a.ID).Exec(ctx))
	})
	longest := time.Now().UTC().Add(141 * time.Hour).Truncate(time.Second)
	require.NoError(t, repo.SetRateLimitedIfLater(ctx, a.ID, longest))
	var wg sync.WaitGroup
	results := make(chan error, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			results <- repo.SetRateLimitedIfLater(ctx, a.ID, time.Now().Add(time.Duration(n+1)*time.Minute))
		}(i)
	}
	wg.Wait()
	close(results)
	for err := range results {
		require.NoError(t, err)
	}
	// A new repository instance must recover the longer database cooldown.
	restarted := newAccountRepositoryWithSQL(client, integrationDB, nil)
	got, err := restarted.GetByID(ctx, a.ID)
	require.NoError(t, err)
	require.NotNil(t, got.RateLimitResetAt)
	require.WithinDuration(t, longest, *got.RateLimitResetAt, time.Second)
	require.False(t, got.IsSchedulable())
	require.Equal(t, service.StatusActive, got.Status)
}

func TestClinePostgresUsageForeignKeyFailureIsNotHidden(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	repo := newUsageLogRepositoryWithSQL(client, integrationDB)
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	user := mustCreateUser(t, client, &service.User{Email: "cline-incident-" + suffix + "@example.test"})
	key := mustCreateApiKey(t, client, &service.APIKey{UserID: user.ID, Key: "fixture-incident-" + suffix, Name: "fixture"})
	usage := &service.UsageLog{UserID: user.ID, APIKeyID: key.ID, AccountID: 1 << 60, RequestID: "incident-fk-" + suffix, Model: "test-model", InputTokens: 1, OutputTokens: 1}
	inserted, err := repo.Create(ctx, usage)
	require.Error(t, err)
	require.False(t, inserted)
	kind, state := incidentdiag.ErrorClass(err)
	require.Equal(t, "foreign_key", kind)
	require.Equal(t, "23503", state)
	var count int
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT count(*) FROM usage_logs WHERE request_id=$1", usage.RequestID).Scan(&count))
	require.Zero(t, count)
	require.Equal(t, int64(1<<60), usage.AccountID)
}
