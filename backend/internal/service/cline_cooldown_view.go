package service

import (
	"encoding/json"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/cline"
)

// ClineCooldownView separates an authoritative reset from a safe retry probe.
// It contains no credential, upstream subject or raw provider error text.
type ClineCooldownView struct {
	Type    string     `json:"type"`
	Source  string     `json:"source"`
	ResetAt *time.Time `json:"reset_at,omitempty"`
	RetryAt *time.Time `json:"retry_at,omitempty"`
	Status  string     `json:"status"`
}

func clineCooldownViews(a *Account, now time.Time) []ClineCooldownView {
	out := make([]ClineCooldownView, 0)
	if !a.IsCline() || a.GetClineMode() != cline.ModePass {
		return out
	}
	state := a.GetClineState()
	for _, b := range ClineQuotaBlocksForState(state) {
		status := "cooling"
		if b.ResetAt == nil || !b.ResetAt.After(now) {
			status = "pending_recheck"
		}
		out = append(out, ClineCooldownView{Type: b.Type, Source: "usage", ResetAt: b.ResetAt, Status: status})
	}
	var limits map[string]struct {
		Authoritative bool `json:"reset_authoritative"`
	}
	body, _ := json.Marshal(a.Extra["model_rate_limits"])
	_ = json.Unmarshal(body, &limits)
	for _, name := range []string{"five_hour", "weekly", "monthly", "unknown", "entitlement"} {
		scope := cline.ScopePass + name
		key := ClineRateLimitScope(a, scope)
		at := a.modelRateLimitResetAt(key)
		if at == nil || (!at.After(now) && ClinePassWindowRecovered(state, scope, *at, now)) {
			continue
		}
		entry := ClineCooldownView{Type: name, Source: "inference", Status: "cooling"}
		if limits[key].Authoritative {
			entry.ResetAt = at
		} else {
			entry.RetryAt = at
		}
		if !at.After(now) {
			entry.Status = "pending_recheck"
		}
		out = append(out, entry)
	}
	return out
}
