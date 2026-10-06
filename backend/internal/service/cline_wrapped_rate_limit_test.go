//go:build unit

package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/cline"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type clineWrappedLegacyRepository struct {
	AccountRepository
	ids   []int64
	until time.Time
}

func (r *clineWrappedLegacyRepository) SetRateLimitedIfLater(_ context.Context, id int64, until time.Time) error {
	r.ids = append(r.ids, id)
	if until.After(r.until) {
		r.until = until
	}
	return nil
}

func TestClineWrappedLimitPreservesAuditAndStopsSameAccountRetry(t *testing.T) {
	gin.SetMode(gin.TestMode)
	inner := `{"error":{"message":"Rate limit exceeded for vendor/model: this team's limit is exhausted. Try again in 45s."}}`
	body, err := json.Marshal(map[string]any{"error": map[string]any{"message": "Failed to create stream: inference request failed: failed to generate stream from Vercel: request failed with status 429: " + inner}})
	require.NoError(t, err)
	for _, platform := range []string{PlatformOpenAI, PlatformDeepseek, PlatformCline} {
		t.Run(platform, func(t *testing.T) {
			legacy := &clineWrappedLegacyRepository{}
			scoped := &clineGuardLimitRepository{}
			account := &Account{ID: 71, Platform: platform, Type: AccountTypeAPIKey, Credentials: map[string]any{"base_url": "https://api.cline.bot/api/v1", "pool_mode": true}}
			repo := AccountRepository(legacy)
			if platform == PlatformCline {
				account = clineTestAccount(cline.ModePass)
				repo = scoped
			}
			svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), rateLimitService: &RateLimitService{accountRepo: repo}}
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			headers := http.Header{"Retry-After": []string{"90"}, "X-Request-Id": []string{"fixture-upstream"}}
			before := time.Now()
			failure := svc.failoverOpenAIUpstreamHTTPError(context.Background(), c, account, &http.Response{StatusCode: 502, Header: headers}, body, "Cline wrapped limit", "vendor/model")
			require.NotNil(t, failure)
			assert.Equal(t, 429, failure.StatusCode)
			assert.False(t, failure.RetryableOnSameAccount)
			assert.Equal(t, body, failure.ResponseBody)
			assert.Equal(t, "90", failure.ResponseHeaders.Get("Retry-After"))
			raw, ok := c.Get(OpsUpstreamErrorsKey)
			require.True(t, ok)
			events, ok := raw.([]*OpsUpstreamErrorEvent)
			require.True(t, ok)
			require.NotEmpty(t, events)
			assert.Equal(t, 502, events[len(events)-1].UpstreamStatusCode)
			if platform == PlatformCline {
				require.Len(t, scoped.observations, 1)
				assert.Equal(t, cline.ScopeThrottle, scoped.observations[0].scope)
			} else {
				assert.Equal(t, []int64{account.ID}, legacy.ids)
				assert.False(t, legacy.until.Before(before.Add(90*time.Second)))
				assert.False(t, legacy.until.After(time.Now().Add(90*time.Second)))
			}
		})
	}
	other := &Account{Platform: PlatformDeepseek, Type: AccountTypeAPIKey, Credentials: map[string]any{"base_url": "https://api.deepseek.com"}}
	assert.False(t, isClineWrappedRateLimit(other, 502, body))
	assert.False(t, isClineWrappedRateLimit(clineTestAccount(cline.ModePass), 401, body))
	assert.False(t, isClineWrappedRateLimit(clineTestAccount(cline.ModePass), 502, []byte(`{"error":{"message":"unavailable"}}`)))
}
