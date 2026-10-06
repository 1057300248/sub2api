//go:build unit

package service

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/cline"
	"github.com/stretchr/testify/require"
)

type clineSettingsRepository struct {
	AccountRepository
	account *Account
	writes  int
}

func (r *clineSettingsRepository) GetByID(context.Context, int64) (*Account, error) {
	return r.account, nil
}
func (r *clineSettingsRepository) Update(_ context.Context, a *Account) error {
	r.writes++
	r.account = a
	return nil
}
func (r *clineSettingsRepository) ListShadowsByParent(context.Context, int64) ([]*Account, error) {
	return nil, nil
}

func TestClineAccountSettingsCreateScheduling(t *testing.T) {
	for _, mode := range []string{cline.ModePass, cline.ModePayG, cline.ModeFree, cline.ModeUnknown} {
		for _, enabled := range []bool{false, true} {
			t.Run(mode+"/"+map[bool]string{false: "paused", true: "enabled"}[enabled], func(t *testing.T) {
				a, err := buildAccountForCreate(&CreateAccountInput{Platform: PlatformCline, Type: AccountTypeAPIKey, Credentials: map[string]any{"account_mode": mode}, Schedulable: &enabled, Concurrency: 3}, nil)
				require.NoError(t, err)
				require.Equal(t, enabled && (mode == cline.ModePass || mode == cline.ModePayG), a.Schedulable)
			})
		}
	}
	disabled := false
	a, err := buildAccountForCreate(&CreateAccountInput{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Schedulable: &disabled}, nil)
	require.NoError(t, err)
	require.True(t, a.Schedulable, "unrelated creation behavior is unchanged")
}

func TestClineAccountSettingsUpdatePreservesUneditedConfiguration(t *testing.T) {
	a := clineRoutingAccount(t, cline.ModePass, cline.AuthAPIKey)
	a.Credentials["fixture_unedited_option"] = "preserve"
	a.Extra["fixture_unedited_extra"] = "preserve"
	a.Extra[AccountCostMultiplierExtraKey] = 0.6
	a.Extra[ClineStateExtraKey] = map[string]any{"quota_status": "exhausted"}
	a.Extra["model_rate_limits"] = map[string]any{"fixture": "preserve"}
	load := 4
	proxy := int64(8)
	expires := time.Now().Add(time.Hour)
	a.LoadFactor, a.ProxyID, a.ExpiresAt = &load, &proxy, &expires
	note := "old note"
	a.Notes = &note
	r := &clineSettingsRepository{account: a}
	svc := &adminServiceImpl{accountRepo: r}
	zero := float64(0)
	two := 2.5
	no := false
	empty := ""
	clear := int64(0)
	clearLoad := 0
	input := &UpdateAccountInput{Credentials: map[string]any{"header_override_enabled": true, "header_overrides": map[string]any{"User-Agent": "fixture/2"}}, Extra: map[string]any{AccountCostMultiplierExtraKey: 0.2}, Notes: &empty, RateMultiplier: &zero, GroupRateMultiplier: &two, Schedulable: &no, Status: StatusInactive, ProxyID: &clear, LoadFactor: &clearLoad, ExpiresAt: &clear, AutoPauseOnExpired: &no}
	updated, err := svc.UpdateAccount(context.Background(), a.ID, input)
	require.NoError(t, err)
	require.Equal(t, 1, r.writes)
	require.False(t, updated.Schedulable)
	require.Equal(t, StatusInactive, updated.Status)
	require.Equal(t, 0.0, *updated.RateMultiplier)
	require.Equal(t, 2.5, *updated.GroupRateMultiplier)
	require.Equal(t, 0.2, updated.CostMultiplier())
	require.Nil(t, updated.ProxyID)
	require.Nil(t, updated.ExpiresAt)
	require.Nil(t, updated.LoadFactor)
	require.Nil(t, updated.Notes)
	require.False(t, updated.AutoPauseOnExpired)
	require.Equal(t, "test-key-not-real", updated.Credentials["api_key"])
	require.Equal(t, "preserve", updated.Credentials["fixture_unedited_option"])
	require.Equal(t, "preserve", updated.Extra["fixture_unedited_extra"])
	require.Equal(t, map[string]any{"quota_status": "exhausted"}, updated.Extra[ClineStateExtraKey])
	require.Equal(t, map[string]any{"fixture": "preserve"}, updated.Extra["model_rate_limits"])
	require.Equal(t, map[string]string{"user-agent": "fixture/2"}, updated.GetHeaderOverrides())
	require.Equal(t, map[string]any{AccountCostMultiplierExtraKey: float64(0.2)}, input.Extra, "do not mutate caller delta into full Extra")
	require.NotContains(t, input.Credentials, "api_key")
}

func TestClineAccountHeaderSettingsValidationAndIsolation(t *testing.T) {
	for _, bad := range []map[string]any{
		{"header_override_enabled": "true"}, {"header_overrides": nil}, {"header_overrides": []string{"x"}},
		{"header_overrides": map[string]any{"User-Agent": nil}}, {"header_overrides": map[string]any{"Authorization": "secret-marker"}},
		{"header_overrides": map[string]any{"User-Agent": "a", "user-agent": "b"}},
		{"header_overrides": map[string]any{"User-Agent": "bad\r\nInjected: value"}},
		{"header_overrides": map[string]any{"X-Metadata-Test": strings.Repeat("中", 683)}},
	} {
		a := clineRoutingAccount(t, cline.ModePass, cline.AuthAPIKey)
		for k, v := range bad {
			a.Credentials[k] = v
		}
		err := NormalizeClineCredentials(a.Platform, a.Type, a.Credentials)
		require.ErrorIs(t, err, ErrClineHeaderOverrides)
		require.NotContains(t, err.Error(), "secret-marker")
		// A malformed imported config fails closed at the runtime header boundary too.
		require.Empty(t, a.GetHeaderOverrides())
	}
	a := clineRoutingAccount(t, cline.ModePass, cline.AuthAPIKey)
	a.Credentials["header_override_enabled"] = true
	a.Credentials["header_overrides"] = map[string]any{"User-Agent": "saved/1", "X-Metadata-Test": "ok"}
	before, _ := json.Marshal(a.Credentials)
	require.True(t, a.IsHeaderOverrideEligible())
	require.Len(t, a.GetHeaderOverrides(), 2)
	req, _ := http.NewRequest("POST", "https://example.invalid", nil)
	req.Header.Set("Authorization", "Bearer fixture")
	a.ApplyHeaderOverrides(req.Header)
	applyClineRequestHeaders(req.Header, http.Header{"User-Agent": []string{"request/2"}})
	require.Equal(t, []string{"request/2"}, req.Header.Values("User-Agent"))
	require.Equal(t, "Bearer fixture", req.Header.Get("Authorization"))
	count := 0
	for k := range req.Header {
		if strings.EqualFold(k, "User-Agent") {
			count++
		}
	}
	require.Equal(t, 1, count)
	after, _ := json.Marshal(a.Credentials)
	require.Equal(t, before, after)
	merged := mergeClineAccountCredentials(a, map[string]any{"header_override_enabled": false, "header_overrides": map[string]any{}})
	require.Equal(t, "test-key-not-real", merged["api_key"])
	require.Empty(t, merged["header_overrides"])
	require.NotEmpty(t, a.Credentials["header_overrides"])
	a.Platform = PlatformDeepseek
	a.Credentials["unrelated-option"] = "old"
	generic := mergeClineAccountCredentials(a, map[string]any{"base_url": "https://api.deepseek.com"})
	require.Equal(t, "test-key-not-real", generic["api_key"])
	require.NotContains(t, generic, "unrelated-option", "generic full-PUT semantics are preserved")
}

func TestClineBulkHeaderSettingsRejectBeforeAnyWrite(t *testing.T) {
	a := clineRoutingAccount(t, cline.ModePass, cline.AuthAPIKey)
	a.ID = 101
	other := &Account{ID: 102, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": "other-fixture"}}
	repo := &accountRepoStubForBulkUpdate{getByIDsAccounts: []*Account{a, other}}
	svc := &adminServiceImpl{accountRepo: repo}
	_, err := svc.BulkUpdateAccounts(context.Background(), &BulkUpdateAccountsInput{AccountIDs: []int64{101, 102}, Credentials: map[string]any{"header_overrides": map[string]any{"X-Tenant-ID": "forbidden"}, "header_override_enabled": true}})
	require.ErrorIs(t, err, ErrClineHeaderOverrides)
	require.Zero(t, repo.bulkUpdateCalls)
	require.Empty(t, repo.updatedAccounts)
}
