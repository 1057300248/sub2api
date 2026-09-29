package service

import (
	"context"
	"encoding/json"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestAccountCostMultiplierDefaultsAndValidation(t *testing.T) {
	var missing *Account
	require.Equal(t, 0.1, missing.CostMultiplier())
	billing, group := 7.0, 3.0
	a := &Account{RateMultiplier: &billing, GroupRateMultiplier: &group}
	require.Equal(t, 0.1, a.CostMultiplier())
	for _, value := range []any{0.0, 0, 0.25, json.Number("0.25"), int64(2), float32(0.5)} {
		a.Extra = map[string]any{AccountCostMultiplierExtraKey: value}
		require.NoError(t, ValidateAccountCostMultiplierExtra(a.Extra))
		require.Equal(t, a.Extra[AccountCostMultiplierExtraKey], a.CostMultiplier())
		require.Equal(t, 7.0, a.BillingRateMultiplier())
		require.Equal(t, 3.0, a.UserGroupRateMultiplier())
	}
	for _, value := range []any{-0.1, math.NaN(), math.Inf(1), 1000001.0, "0.1", true, json.Number("invalid")} {
		a.Extra = map[string]any{AccountCostMultiplierExtraKey: value}
		require.Error(t, ValidateAccountCostMultiplierExtra(a.Extra))
		require.Equal(t, 0.1, a.CostMultiplier(), "malformed legacy metadata must not poison scores")
	}
	a.Extra = map[string]any{AccountCostMultiplierExtraKey: nil}
	require.NoError(t, ValidateAccountCostMultiplierExtra(a.Extra))
	require.Equal(t, 0.1, a.CostMultiplier())
}

func TestAccountCostMultiplierRejectsInvalidWritesBeforeRepositoryAccess(t *testing.T) {
	s := &adminServiceImpl{}
	ctx := context.Background()
	extra := map[string]any{AccountCostMultiplierExtraKey: -1.0}
	_, err := s.CreateAccount(ctx, &CreateAccountInput{Extra: extra})
	require.ErrorContains(t, err, "cost_multiplier")
	_, err = s.UpdateAccount(ctx, 1, &UpdateAccountInput{Extra: extra})
	require.ErrorContains(t, err, "cost_multiplier")
	_, err = s.BulkUpdateAccounts(ctx, &BulkUpdateAccountsInput{AccountIDs: []int64{1}, Extra: extra})
	require.ErrorContains(t, err, "cost_multiplier")
	require.ErrorContains(t, s.UpdateAccountExtra(ctx, 1, extra), "cost_multiplier")
}

func TestPriorityCostMultiplierReestimatesProfitWithoutChangingBilling(t *testing.T) {
	c := DefaultPrioritySchedulingConfig()
	item := priorityCandidate(402, 1, 20)
	item.account.Type = AccountTypeOAuth
	item.account.Credentials = map[string]any{"plan_type": "self_serve_business_prolite"}
	item.account.Extra = map[string]any{"priority_teams_first_used_at": time.Now().Format(time.RFC3339)}
	signal := PrioritySchedulingSignal{Samples: 93, P90TTFTMs: 1000, QualityPassed: 10, QualitySamples: 10, ProfitSamples: 93, Revenue: 2.8177, BaseCost: 10.8098}
	score := scorePriorityCandidate(c, item, signal, time.Now())
	require.Equal(t, 0.1, *score.Rate)
	require.Equal(t, "usage", score.EconomicsSource)
	require.InDelta(t, 1.08098, score.TheoreticalCost, 0.000001)
	require.InDelta(t, 1.73672, *score.Profit, 0.000001)
	require.NotContains(t, score.Reasons, "historical_loss")

	// Account billing can already be 0.1; procurement estimates must not apply it twice.
	otherBilling := 0.1
	item.account.RateMultiplier = &otherBilling
	again := scorePriorityCandidate(c, item, signal, time.Now())
	require.Equal(t, score.TheoreticalCost, again.TheoreticalCost)
	item.account.Extra[AccountCostMultiplierExtraKey] = 0.2
	again = scorePriorityCandidate(c, item, signal, time.Now())
	require.InDelta(t, 2.16196, again.TheoreticalCost, 0.000001, "cached base costs can be revalued immediately")
	require.Equal(t, 0.1, item.account.BillingRateMultiplier())
	item.account.Extra[AccountCostMultiplierExtraKey] = 0.0
	again = scorePriorityCandidate(c, item, signal, time.Now())
	require.Zero(t, again.TheoreticalCost)
	require.Equal(t, signal.Revenue, *again.Profit)
	require.Equal(t, 10.8098, signal.BaseCost, "cached source signals are immutable")
}

func TestPriorityConfigIgnoresRetiredPurchaseWindow(t *testing.T) {
	c := DefaultPrioritySchedulingConfig()
	require.NoError(t, json.Unmarshal([]byte(`{"enabled":true,"teams":{"enabled":true,"cost_cny":50,"window_hours":4,"window_source":"first_usage"}}`), &c))
	require.NoError(t, ValidatePrioritySchedulingConfig(c))
	raw, err := json.Marshal(c)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "teams")
	item := priorityCandidate(1, 1, 0)
	item.account.Type = AccountTypeOAuth
	item.account.Credentials = map[string]any{"plan_type": "team"}
	item.account.Extra = nil
	score := scorePriorityCandidate(c, item, PrioritySchedulingSignal{ProfitSamples: 5, Revenue: 10, BaseCost: 20}, time.Now())
	require.Equal(t, "usage", score.EconomicsSource)
	require.Equal(t, 8.0, *score.Profit)
	require.NotContains(t, score.Reasons, "teams_window_unavailable")
}

func TestPriorityCostMultiplierPrefersFreshUpstreamRate(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	for _, tt := range []struct {
		name     string
		status   string
		age      time.Duration
		upstream float64
		fallback any
		want     float64
	}{
		{"upstream overrides saved default", UpstreamBillingProbeStatusOK, time.Minute, 0.14, 0.1, 0.14},
		{"upstream overrides custom fallback", UpstreamBillingProbeStatusOK, time.Minute, 0.14, 0.25, 0.14},
		{"upstream overrides zero fallback", UpstreamBillingProbeStatusOK, time.Minute, 0.14, 0.0, 0.14},
		{"upstream without fallback", UpstreamBillingProbeStatusOK, time.Minute, 0.14, nil, 0.14},
		{"zero upstream", UpstreamBillingProbeStatusOK, time.Minute, 0, 0.1, 0},
		{"fresh cached success after failure", UpstreamBillingProbeStatusFailed, time.Minute, 0.14, 0.1, 0.14},
		{"stale uses custom fallback", UpstreamBillingProbeStatusOK, 61 * time.Minute, 0.14, 0.25, 0.25},
		{"stale uses default", UpstreamBillingProbeStatusOK, 61 * time.Minute, 0.14, nil, 0.1},
		{"stale preserves zero fallback", UpstreamBillingProbeStatusOK, 61 * time.Minute, 0.14, 0.0, 0},
		{"unsupported", UpstreamBillingProbeStatusUnsupported, time.Minute, 0.14, 0.1, 0.1},
		{"future snapshot", UpstreamBillingProbeStatusOK, -time.Minute, 0.14, 0.1, 0.1},
		{"invalid upstream", UpstreamBillingProbeStatusOK, time.Minute, -0.14, 0.1, 0.1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			item := priorityCandidate(1, 7, 20)
			item.account.Extra = upstreamCostTestAccount(1, tt.status, tt.upstream, now.Add(-tt.age), 30*time.Minute).Extra
			item.account.Extra[AccountCostMultiplierExtraKey] = tt.fallback
			signal := PrioritySchedulingSignal{ProfitSamples: 20, Revenue: 20, BaseCost: 100}
			before, err := json.Marshal(item.account.Extra)
			require.NoError(t, err)
			score := scorePriorityCandidate(DefaultPrioritySchedulingConfig(), item, signal, now)
			require.Equal(t, tt.want, *score.Rate)
			require.InDelta(t, 100*tt.want, score.TheoreticalCost, 1e-9)
			require.InDelta(t, 20-100*tt.want, *score.Profit, 1e-9)
			require.Equal(t, 7.0, item.account.BillingRateMultiplier())
			require.Equal(t, 100.0, signal.BaseCost)
			after, err := json.Marshal(item.account.Extra)
			require.NoError(t, err)
			require.Equal(t, string(before), string(after), "scoring must not persist the upstream rate as a fallback")
		})
	}
}

func TestPriorityCostMultiplierRecomputesUpstreamPeakAndExpiry(t *testing.T) {
	now := time.Date(2026, 9, 29, 17, 30, 0, 0, time.UTC)
	item := priorityCandidate(1, 7, 20)
	item.account.Extra = upstreamCostTestAccount(1, UpstreamBillingProbeStatusOK, 0.14, now, time.Hour).Extra
	item.account.Extra[AccountCostMultiplierExtraKey] = 0.1
	snapshot := item.account.Extra[UpstreamBillingProbeExtraKey].(map[string]any)
	data := snapshot["data"].(map[string]any)
	data["peak_rate_enabled"] = true
	data["peak_start"] = "09:00"
	data["peak_end"] = "18:00"
	data["peak_rate_multiplier"] = 2.0
	data["timezone"] = "UTC"
	signal := PrioritySchedulingSignal{ProfitSamples: 20, Revenue: 10, BaseCost: 100}
	for _, tt := range []struct {
		at   time.Time
		want float64
	}{
		{now, 0.28},
		{now.Add(time.Hour), 0.14},
		{now.Add(3 * time.Hour), 0.1},
	} {
		score := scorePriorityCandidate(DefaultPrioritySchedulingConfig(), item, signal, tt.at)
		require.InDelta(t, tt.want, *score.Rate, 1e-9)
		require.InDelta(t, 100*tt.want, score.TheoreticalCost, 1e-9)
	}
	// OAuth accounts cannot use an API-key probe left in imported metadata.
	item.account.Type = AccountTypeOAuth
	score := scorePriorityCandidate(DefaultPrioritySchedulingConfig(), item, signal, now)
	require.Equal(t, 0.1, *score.Rate)
}
