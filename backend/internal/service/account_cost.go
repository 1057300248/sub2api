package service

import (
	"encoding/json"
	"math"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const AccountCostMultiplierExtraKey = "cost_multiplier"
const DefaultAccountCostMultiplier = 0.1

// CostMultiplier prefers a fresh upstream rate for profitability estimates.
// The operator-maintained value in extra is a fallback, defaulting to 0.1.
// Neither path changes account billing, user billing or stored usage logs.
func (a *Account) CostMultiplier() float64 {
	return a.costMultiplierAt(time.Now())
}

func (a *Account) costMultiplierAt(now time.Time) float64 {
	// Share freshness, token billing scope and current peak-rate semantics with
	// upstream-aware scheduling; never persist a probe as the manual fallback.
	if rate, ok := openAIFreshUpstreamBillingRate(a, now); ok {
		return rate
	}
	if a != nil {
		if value, ok := accountCostMultiplierNumber(a.Extra[AccountCostMultiplierExtraKey]); ok {
			return value
		}
	}
	return DefaultAccountCostMultiplier
}

func accountCostMultiplierNumber(raw any) (float64, bool) {
	var value float64
	switch v := raw.(type) {
	case float64:
		value = v
	case float32:
		value = float64(v)
	case int:
		value = float64(v)
	case int64:
		value = float64(v)
	case json.Number:
		var err error
		value, err = v.Float64()
		if err != nil {
			return 0, false
		}
	default:
		return 0, false
	}
	return value, !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0 && value <= 1000000
}

// Null restores the default fallback; zero is an explicit zero-cost fallback.
func ValidateAccountCostMultiplierExtra(extra map[string]any) error {
	raw, exists := extra[AccountCostMultiplierExtraKey]
	if !exists || raw == nil {
		return nil
	}
	value, ok := accountCostMultiplierNumber(raw)
	if !ok {
		return infraerrors.BadRequest("INVALID_COST_MULTIPLIER", "cost_multiplier must be a finite number between 0 and 1000000")
	}
	extra[AccountCostMultiplierExtraKey] = value
	return nil
}
