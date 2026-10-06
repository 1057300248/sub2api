package service

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/cline"
)

// ClineQuotaBlock is durable evidence of exhaustion. ResetAt is an upstream
// timestamp, never a locally invented five-hour/week/month boundary. Reaching
// that timestamp permits a metadata recheck, not unconditionally an inference.
type ClineQuotaBlock struct {
	Type       string     `json:"type"`
	ResetAt    *time.Time `json:"reset_at,omitempty"`
	ObservedAt time.Time  `json:"observed_at"`
}

func clinePassWindow(name string) bool {
	return name == "five_hour" || name == "weekly" || name == "monthly"
}

// clineQuotaBlocks also upgrades observations saved before durable blocks were
// introduced. Failed/partial refreshes must not erase last-known exhaustion.
func ClineQuotaBlocksForState(state *ClineState) []ClineQuotaBlock {
	if state == nil {
		return nil
	}
	blocks := make(map[string]ClineQuotaBlock)
	for _, block := range state.QuotaBlocks {
		if clinePassWindow(block.Type) {
			blocks[block.Type] = block
		}
	}
	if state.LastSuccessAt != nil {
		for _, w := range state.Windows {
			if clinePassWindow(w.Type) && w.PercentUsed == 100 {
				if _, exists := blocks[w.Type]; !exists {
					blocks[w.Type] = ClineQuotaBlock{Type: w.Type, ResetAt: w.ResetsAt, ObservedAt: *state.LastSuccessAt}
				}
			}
		}
	}
	out := make([]ClineQuotaBlock, 0, len(blocks))
	for _, name := range []string{"five_hour", "weekly", "monthly"} {
		if b, exists := blocks[name]; exists {
			out = append(out, b)
		}
	}
	return out
}

func mergeClineQuotaBlocks(state *ClineState, windows []cline.Window, observed time.Time) []ClineQuotaBlock {
	blocks := make(map[string]ClineQuotaBlock)
	for _, b := range ClineQuotaBlocksForState(state) {
		blocks[b.Type] = b
	}
	for _, w := range windows {
		if !clinePassWindow(w.Type) {
			continue
		}
		if w.PercentUsed == 100 {
			blocks[w.Type] = ClineQuotaBlock{Type: w.Type, ResetAt: w.ResetsAt, ObservedAt: observed}
		} else if w.PercentUsed >= 0 && w.PercentUsed < 100 && (w.ResetsAt == nil || w.ResetsAt.After(observed)) {
			delete(blocks, w.Type)
		}
	}
	out := make([]ClineQuotaBlock, 0, len(blocks))
	for _, name := range []string{"five_hour", "weekly", "monthly"} {
		if b, exists := blocks[name]; exists {
			out = append(out, b)
		}
	}
	return out
}

// ClinePassWindowRecovered requires a successful observation of this specific
// window after the retry boundary. An empty/partial response is not recovery.
// Once observed, recovery does not disappear just because the UI becomes stale.
func ClinePassWindowRecovered(state *ClineState, scope string, after, now time.Time) bool {
	if state == nil || state.LastSuccessAt == nil || state.LastSuccessAt.Before(after) || state.LastSuccessAt.After(now) || (state.CredentialStatus == "invalid" || state.CredentialStatus == "reauth_required") {
		return false
	}
	name := strings.TrimPrefix(scope, cline.ScopePass)
	matched := 0
	for _, w := range state.Windows {
		if !clinePassWindow(w.Type) || w.PercentUsed < 0 || w.PercentUsed >= 100 || (w.ResetsAt != nil && !w.ResetsAt.After(*state.LastSuccessAt)) {
			continue
		}
		if w.Type == name {
			return true
		}
		matched++
	}
	return (name == "unknown" || name == "entitlement") && matched == 3
}

// ClineQuotaAdmission is separate from ClineMetadataForAccount (a display view).
// It preserves known exhaustion across metadata errors, staleness and restarts.
func ClineQuotaAdmission(account *Account, now time.Time) error {
	if !account.IsCline() {
		return nil
	}
	state := account.GetClineState()
	if state != nil && (state.CredentialStatus == "invalid" || state.CredentialStatus == "reauth_required") {
		return ErrClineMetadataRequired
	}
	if account.GetClineMode() != cline.ModePass {
		return nil
	}
	if state != nil && len(ClineQuotaBlocksForState(state)) > 0 {
		return ErrClineObservedCooldown
	}
	for _, scope := range cline.RateLimitKeys(cline.ModePass, "") {
		if !strings.HasPrefix(scope, cline.ScopePass) {
			continue
		}
		reset := account.modelRateLimitResetAt(ClineRateLimitScope(account, scope))
		if reset == nil {
			continue
		}
		if reset.After(now) {
			return ErrClineObservedCooldown
		}
		// Custom origins do not expose the official usage endpoint. Preserve
		// their inference Retry-After behavior instead of locking them forever.
		if cline.IsOfficialBase(account.GetClineBaseURL()) && !ClinePassWindowRecovered(state, scope, *reset, now) {
			return ErrClineMetadataRequired
		}
	}
	return nil
}

func ClineMetadataAutoEligible(a *Account, now time.Time) bool {
	return a.IsCline() && a.Type == AccountTypeAPIKey && a.GetClineMode() == cline.ModePass &&
		cline.IsOfficialBase(a.GetClineBaseURL()) && a.Status == StatusActive && a.Schedulable &&
		(!a.AutoPauseOnExpired || a.ExpiresAt == nil || a.ExpiresAt.After(now))
}

// ClineMetadataNextRefresh bounds polling: normal five minutes, long exhaustion
// thirty minutes, errors exponential (30s..30m). A known reset gets a prompt
// recheck, without interpreting the label as a fresh duration.
func ClineMetadataNextRefresh(a *Account, state *ClineState, now time.Time) time.Time {
	interval := 5 * time.Minute
	if len(ClineQuotaBlocksForState(state)) > 0 {
		interval = 30 * time.Minute
		for _, b := range ClineQuotaBlocksForState(state) {
			if b.ResetAt == nil || !b.ResetAt.After(now) {
				interval = 5 * time.Minute
			}
		}
	}
	if state.RefreshFailures > 0 {
		shift := min(state.RefreshFailures-1, 6)
		interval = min(30*time.Second*time.Duration(1<<shift), 30*time.Minute)
	}
	// Stable per-account jitter avoids synchronized metadata requests on boot.
	next := now.Add(interval + time.Duration(a.ID%17)*time.Second)
	for _, b := range ClineQuotaBlocksForState(state) {
		if b.ResetAt != nil && b.ResetAt.After(now) {
			at := b.ResetAt.Add(2 * time.Second)
			if at.Before(now.Add(30 * time.Second)) {
				at = now.Add(30 * time.Second)
			}
			if at.Before(next) {
				next = at
			}
		}
	}
	for _, scope := range cline.RateLimitKeys(cline.ModePass, "") {
		if strings.HasPrefix(scope, cline.ScopePass) {
			if reset := a.modelRateLimitResetAt(ClineRateLimitScope(a, scope)); reset != nil && reset.After(now) {
				at := reset.Add(2 * time.Second)
				if at.Before(now.Add(30 * time.Second)) {
					at = now.Add(30 * time.Second)
				}
				if at.Before(next) {
					next = at
				}
			}
		}
	}
	return next
}

func ClineMetadataRefreshDue(a *Account, now time.Time) bool {
	if !ClineMetadataAutoEligible(a, now) {
		return false
	}
	state := a.GetClineState()
	if state == nil || state.NextRefreshAt == nil || !state.NextRefreshAt.After(now) {
		return true
	}
	// A newly persisted inference limit requests one immediate observation;
	// subsequent failures respect the persisted backoff and the database lease.
	var limits map[string]struct {
		Observed string `json:"rate_limited_at"`
	}
	body, _ := json.Marshal(a.Extra["model_rate_limits"])
	if json.Unmarshal(body, &limits) == nil {
		for scope, limit := range limits {
			if !strings.HasPrefix(scope, "cline:"+ClineCredentialFingerprint(a)+":pass:") {
				continue
			}
			at, err := time.Parse(time.RFC3339, limit.Observed)
			if err == nil && state.FetchedAt != nil && at.After(*state.FetchedAt) && !at.After(now) {
				return true
			}
		}
	}
	return false
}
