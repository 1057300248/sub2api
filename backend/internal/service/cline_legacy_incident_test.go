//go:build unit

package service

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestClineLegacyProductionWeeklyCooldown(t *testing.T) {
	for _, platform := range []string{PlatformOpenAI, PlatformDeepseek} {
		for _, tc := range []struct {
			name, body, retry string
			want              time.Duration
		}{
			{"weekly", `{"error":{"message":"You have reached your weekly Clinepass limit. The limit resets in 5d 21h"}}`, "", 141 * time.Hour},
			{"weekly short retry header", `{"error":{"message":"You have reached your weekly Clinepass limit. The limit resets in 5d 21h"}}`, "60", 141 * time.Hour},
			{"unknown weekly reset", `{"error":{"message":"You have reached your weekly Clinepass limit. The limit resets in unknown"}}`, "", 168 * time.Hour},
			{"monthly", `{"error":{"message":"You have reached your monthly Clinepass limit. The limit resets in 28d 4h"}}`, "", 676 * time.Hour},
		} {
			t.Run(platform+"/"+tc.name, func(t *testing.T) {
				repo := &clineRateLimitCASAccountRepoStub{}
				svc := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
				account := &Account{ID: 42, Platform: platform, Type: AccountTypeAPIKey, Credentials: map[string]any{"base_url": "https://api.cline.bot/api/v1"}}
				headers := http.Header{}
				if tc.retry != "" {
					headers.Set("Retry-After", tc.retry)
				}
				before := time.Now()
				svc.handle429(context.Background(), account, headers, []byte(tc.body))
				after := time.Now()
				require.Equal(t, 1, repo.extendCalls)
				require.Zero(t, repo.rateLimitCalls)
				require.False(t, repo.lastRateLimitReset.Before(before.Add(tc.want)))
				require.False(t, repo.lastRateLimitReset.After(after.Add(tc.want)))
			})
		}
	}
}

func TestClineLegacyIncidentDoesNotClaimOtherOrigins(t *testing.T) {
	for _, base := range []string{"https://api.cline.bot.evil.example/api/v1", "http://api.cline.bot/api/v1", "https://user@api.cline.bot/api/v1", "https://api.cline.bot:8443/api/v1"} {
		account := &Account{ID: 42, Platform: PlatformDeepseek, Type: AccountTypeAPIKey, Credentials: map[string]any{"base_url": base}}
		repo := &clineRateLimitCASAccountRepoStub{}
		svc := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
		require.False(t, svc.handleLegacyCline429(context.Background(), account, nil, []byte("The limit resets in 5d 21h")), base)
		require.Zero(t, repo.extendCalls)
	}
}
