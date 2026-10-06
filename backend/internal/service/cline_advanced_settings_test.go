//go:build unit

package service

import (
	"context"
	"encoding/json"
	"math"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/cline"
	"github.com/stretchr/testify/require"
)

func TestClineAdvancedQuotaValidationAndDelta(t *testing.T) {
	good := map[string]any{"quota_limit": 0.0, "quota_daily_limit": 12.5, "quota_daily_reset_mode": "fixed", "quota_daily_reset_hour": 0, "quota_weekly_reset_day": 0, "quota_reset_timezone": "Europe/Berlin", "quota_notify_daily_enabled": false, "quota_notify_daily_threshold": 0, "quota_notify_daily_threshold_type": "percentage"}
	require.NoError(t, ValidateClineLocalQuotaSettings(PlatformCline, good))
	for _, tc := range []struct {
		k string
		v any
	}{
		{"quota_limit", -1}, {"quota_limit", "1"}, {"quota_limit", math.Inf(1)}, {"quota_daily_limit", 1e13}, {"quota_weekly_reset_day", 7}, {"quota_weekly_reset_hour", 0.5},
		{"quota_daily_reset_mode", "bad"}, {"quota_reset_timezone", "Local"}, {"quota_reset_timezone", "No/SuchZone"}, {"quota_notify_daily_enabled", 0}, {"quota_notify_daily_threshold_type", "evil"}, {"quota_notify_daily_threshold", 101},
	} {
		bad := make(map[string]any)
		for k, v := range good {
			bad[k] = v
		}
		bad[tc.k] = tc.v
		require.ErrorIs(t, ValidateClineLocalQuotaSettings(PlatformCline, bad), ErrClineAdvancedSettings, tc.k)
		require.NoError(t, ValidateClineLocalQuotaSettings(PlatformOpenAI, bad))
	}
	a := clineRoutingAccount(t, cline.ModePass, cline.AuthAPIKey)
	a.Extra = good
	a.Extra["unchanged"] = 1
	merged := mergeClineAccountExtra(a, map[string]any{"quota_limit": nil, "quota_notify_daily_enabled": false})
	require.NotContains(t, merged, "quota_limit")
	require.Contains(t, a.Extra, "quota_limit")
	require.Equal(t, 1, merged["unchanged"])
	require.False(t, merged["quota_notify_daily_enabled"].(bool))
}

func TestClineAdvancedProtectedErrorsAndTemporaryRules(t *testing.T) {
	a := clineRoutingAccount(t, cline.ModePass, cline.AuthAPIKey)
	a.Credentials["custom_error_codes_enabled"] = true
	a.Credentials["custom_error_codes"] = []any{float64(503)}
	a.Credentials["temp_unschedulable_enabled"] = true
	a.Credentials["temp_unschedulable_rules"] = []any{map[string]any{"error_code": float64(503), "keywords": []any{"temporary"}, "duration_minutes": float64(3), "description": "local"}}
	require.NoError(t, NormalizeClineCredentials(a.Platform, a.Type, a.Credentials))
	repo := &rateLimitAccountRepoStub{}
	s := &RateLimitService{accountRepo: repo}
	for _, tc := range []struct {
		code int
		body string
	}{{401, `{"error":"temporary"}`}, {402, `{"error":"temporary"}`}, {403, `{"error":"temporary"}`}, {429, `{"error":"temporary"}`}, {404, `{"error":{"code":"model_not_found","message":"model not found"}}`}, {400, `{"error":{"code":"model_not_found","message":"model not found"}}`}} {
		require.True(t, clineProtectedError(a, tc.code, []byte(tc.body)))
		require.Equal(t, ErrorPolicyNone, s.CheckErrorPolicy(context.Background(), a, tc.code, []byte(tc.body)))
		require.False(t, s.tryTempUnschedulable(context.Background(), a, tc.code, []byte(tc.body)))
	}
	require.Zero(t, repo.tempCalls)
	require.Equal(t, ErrorPolicyTempUnscheduled, s.CheckErrorPolicy(context.Background(), a, 503, []byte(`{"error":"temporary outage"}`), "public"))
	require.Equal(t, 1, repo.tempCalls, "Cline local pause is an account block, never a Pass model scope")
	require.Contains(t, repo.lastTempReason, "temporary")
	require.Equal(t, ErrorPolicySkipped, s.CheckErrorPolicy(context.Background(), a, 502, []byte(`{"error":"temporary"}`)))
	require.Equal(t, 1, repo.tempCalls)
	a.Platform = PlatformOpenAI
	require.Equal(t, ErrorPolicySkipped, s.CheckErrorPolicy(context.Background(), a, 429, []byte(`{"error":"temporary"}`)))
}

func TestClineAdvancedInvalidRulesAndFalsePreservation(t *testing.T) {
	for _, input := range []map[string]any{
		{"custom_error_codes_enabled": "yes"}, {"custom_error_codes_enabled": true}, {"custom_error_codes": []any{float64(503), float64(503)}}, {"custom_error_codes": []any{float64(200)}},
		{"temp_unschedulable_enabled": true, "temp_unschedulable_rules": []any{}}, {"temp_unschedulable_rules": []any{map[string]any{"error_code": 503, "duration_minutes": 0, "keywords": []string{"x"}}}},
		{"temp_unschedulable_rules": []any{map[string]any{"error_code": 503, "duration_minutes": 1, "keywords": []string{"a\rb"}}}},
	} {
		require.ErrorIs(t, validateClineErrorSettings(PlatformCline, input), ErrClineAdvancedSettings)
		require.NoError(t, validateClineErrorSettings(PlatformOpenAI, input))
	}
	require.NoError(t, validateClineErrorSettings(PlatformCline, map[string]any{"custom_error_codes_enabled": false, "custom_error_codes": []int{}, "temp_unschedulable_enabled": false, "temp_unschedulable_rules": []any{}}))
}

func TestClineAdvancedRuntimeOwnershipAndNotificationThreshold(t *testing.T) {
	now := time.Now().UTC().Format(time.RFC3339)
	current := map[string]any{"quota_used": 45.0, "quota_daily_used": 9.0, "quota_daily_start": now, "quota_daily_limit": 10.0, "cline_state": map[string]any{"marker": "protected"}}
	next := PreserveClineStateExtra(PlatformCline, current, map[string]any{"quota_used": 0.0, "quota_daily_used": 0.0, "quota_daily_limit": 12.0})
	require.Equal(t, 45.0, next["quota_used"])
	require.Equal(t, 9.0, next["quota_daily_used"])
	require.Equal(t, 12.0, next["quota_daily_limit"])
	require.Equal(t, current["cline_state"], next["cline_state"])
	created := PreserveClineStateExtra(PlatformCline, nil, map[string]any{"quota_used": 99.0, "quota_limit": 100.0})
	require.NotContains(t, created, "quota_used")
	require.Equal(t, 100.0, created["quota_limit"])
	a := clineRoutingAccount(t, cline.ModePass, cline.AuthAPIKey)
	a.Extra = map[string]any{"quota_limit": 100.0, "quota_used": 80.0, "quota_notify_total_enabled": true, "quota_notify_total_threshold": 20.0, "quota_notify_total_threshold_type": "percentage"}
	dims := buildQuotaDims(a)
	require.True(t, dims[2].enabled)
	require.Equal(t, 80.0, dims[2].resolvedThreshold())
	raw, _ := json.Marshal(a.Extra)
	copy := string(raw)
	a.Extra["quota_notify_total_enabled"] = false
	require.False(t, buildQuotaDims(a)[2].enabled)
	require.Contains(t, copy, `"quota_used":80`)
}

type clineAdvancedResetRepo struct {
	AccountRepository
	account             *Account
	localResets, pauses int
}

func (r *clineAdvancedResetRepo) GetByID(context.Context, int64) (*Account, error) {
	return r.account, nil
}
func (r *clineAdvancedResetRepo) ResetClineLocalQuota(context.Context, int64) error {
	r.localResets++
	return nil
}
func (r *clineAdvancedResetRepo) ClearClineTemporaryPause(context.Context, int64) (bool, error) {
	r.pauses++
	return true, nil
}
func TestClineAdvancedResetsUseIsolatedOperations(t *testing.T) {
	r := &clineAdvancedResetRepo{account: clineRoutingAccount(t, cline.ModePass, cline.AuthAPIKey)}
	admin := &adminServiceImpl{accountRepo: r}
	err := admin.ResetAccountQuota(context.Background(), r.account.ID)
	require.NoError(t, err)
	require.Equal(t, 1, r.localResets)
	limiter := &RateLimitService{accountRepo: r}
	require.NoError(t, limiter.ClearTempUnschedulable(context.Background(), r.account.ID))
	require.Equal(t, 1, r.pauses)
	// All generic reset methods are intentionally unimplemented and would panic.
}
