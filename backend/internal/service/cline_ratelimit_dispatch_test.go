//go:build unit

package service

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type clineRateLimitCASAccountRepoStub struct {
	rateLimit429AccountRepoStub
	extendCalls int
	extendErr   error
}

func (r *clineRateLimitCASAccountRepoStub) SetRateLimitedIfLater(_ context.Context, id int64, resetAt time.Time) error {
	r.extendCalls++
	r.lastRateLimitID = id
	r.lastRateLimitReset = resetAt
	return r.extendErr
}

func TestHandle429ClineUsesParsedCooldownBeforeProviderFallback(t *testing.T) {
	for _, platform := range []string{PlatformDeepSeek, PlatformOpenAI} {
		t.Run(platform, func(t *testing.T) {
			repo := &clineRateLimitCASAccountRepoStub{}
			svc := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
			account := &Account{
				ID: 42, Platform: platform, Type: AccountTypeAPIKey,
				Credentials: map[string]any{"base_url": "https://api.cline.bot/api/v1"},
			}
			before := time.Now()
			svc.handle429(context.Background(), account, http.Header{}, []byte(`{"error":{"message":"Daily free limit reached. Try again in 1h25m"}}`))
			after := time.Now()

			require.Equal(t, 1, repo.extendCalls)
			require.Zero(t, repo.rateLimitCalls)
			require.Equal(t, account.ID, repo.lastRateLimitID)
			require.False(t, repo.lastRateLimitReset.Before(before.Add(85*time.Minute)))
			require.False(t, repo.lastRateLimitReset.After(after.Add(85*time.Minute)))
		})
	}
}

func TestHandle429ClineCASFailureDoesNotUseRegularSetter(t *testing.T) {
	repo := &clineRateLimitCASAccountRepoStub{extendErr: errors.New("database unavailable")}
	svc := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
	account := &Account{
		ID: 42, Platform: PlatformDeepSeek, Type: AccountTypeAPIKey,
		Credentials: map[string]any{"base_url": "https://api.cline.bot/api/v1"},
	}
	svc.handle429(context.Background(), account, http.Header{}, []byte(`Try again in 1h25m`))
	require.Equal(t, 1, repo.extendCalls)
	require.Zero(t, repo.rateLimitCalls)
}

func TestHandle429ClinePatchPreservesOtherProviderFallback(t *testing.T) {
	repo := &clineRateLimitCASAccountRepoStub{}
	svc := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
	account := &Account{
		ID: 42, Platform: PlatformGemini, Type: AccountTypeAPIKey,
		Credentials: map[string]any{"base_url": "https://example.com/v1"},
	}
	before := time.Now()
	svc.handle429(context.Background(), account, http.Header{}, []byte(`{"error":{"message":"Try again in 1h25m"}}`))
	after := time.Now()

	require.Zero(t, repo.extendCalls)
	require.Equal(t, 1, repo.rateLimitCalls)
	require.Equal(t, account.ID, repo.lastRateLimitID)
	require.False(t, repo.lastRateLimitReset.Before(before.Add(5*time.Second)))
	require.False(t, repo.lastRateLimitReset.After(after.Add(5*time.Second)))
}
