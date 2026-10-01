package cline

import (
	"encoding/json"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	ScopeThrottle = "cline:throttle"
	ScopePass     = "cline:pass:"
	ScopeFree     = "cline:free:"
	ScopePayG     = "cline:payg:balance"
)

type Limit struct {
	Kind    string     `json:"kind"`
	Scope   string     `json:"scope"`
	Window  string     `json:"window,omitempty"`
	Model   string     `json:"model,omitempty"`
	ResetAt *time.Time `json:"reset_at,omitempty"`
	RetryAt time.Time  `json:"retry_at"`
}

func (l Limit) Until() time.Time {
	if l.ResetAt != nil {
		return *l.ResetAt
	}
	return l.RetryAt
}

var durationText = regexp.MustCompile(`(?i)(?:try\s+again\s+in|the\s+limit\s+resets\s+in)\s+((?:[0-9]+(?:\.[0-9]+)?\s*[smhd]\s*)+)(?:[,.!"\n\r]|$)`)
var durationPart = regexp.MustCompile(`(?i)([0-9]+(?:\.[0-9]+)?)\s*([smhd])`)
var freeModel = regexp.MustCompile(`(?i)free\s+limit\s+reached\s+on\s+model\s+([^\s"<>]+)`)

func errorMessage(body []byte) string {
	if len(body) > MaxBodyBytes {
		return ""
	}
	var obj struct {
		Message string          `json:"message"`
		Error   json.RawMessage `json:"error"`
	}
	if !json.Valid(body) {
		return string(body)
	}
	if json.Unmarshal(body, &obj) != nil {
		return ""
	}
	if obj.Message != "" {
		return obj.Message
	}
	var nested struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(obj.Error, &nested) == nil && nested.Message != "" {
		return nested.Message
	}
	var text string
	if json.Unmarshal(obj.Error, &text) == nil {
		return text
	}
	return ""
}
func resetFromText(text string, now time.Time, maximum time.Duration) *time.Time {
	match := durationText.FindStringSubmatch(text)
	if len(match) != 2 {
		return nil
	}
	seconds := float64(0)
	for _, p := range durationPart.FindAllStringSubmatch(match[1], -1) {
		n, err := strconv.ParseFloat(p[1], 64)
		if err != nil || math.IsNaN(n) || math.IsInf(n, 0) {
			return nil
		}
		switch strings.ToLower(p[2]) {
		case "m":
			n *= 60
		case "h":
			n *= 3600
		case "d":
			n *= 86400
		}
		seconds += n
		if seconds > maximum.Seconds() {
			return nil
		}
	}
	if seconds <= 0 {
		return nil
	}
	at := now.Add(time.Duration(math.Ceil(seconds)) * time.Second)
	return &at
}
func resetFromHeader(h http.Header, now time.Time, maximum time.Duration) *time.Time {
	value := strings.TrimSpace(h.Get("Retry-After"))
	if value == "" {
		return nil
	}
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil {
		if seconds <= 0 || seconds > int64(maximum/time.Second) {
			return nil
		}
		at := now.Add(time.Duration(seconds) * time.Second)
		return &at
	}
	at, err := http.ParseTime(value)
	if err != nil || !at.After(now) || at.Sub(now) > maximum {
		return nil
	}
	return &at
}

// Classify must only receive a confirmed Cline error response, never model output.
func Classify(status int, h http.Header, body []byte, requestedModel string, now time.Time) (Limit, bool) {
	if status < 400 {
		return Limit{}, false
	}
	text := errorMessage(body)
	lower := strings.ToLower(text)
	l := Limit{Kind: "throttle", Scope: ScopeThrottle, RetryAt: now.Add(30 * time.Second)}
	maximum := 7 * 24 * time.Hour
	switch {
	case strings.Contains(lower, "organization accounts cannot use individual model inference subscriptions"):
		l.Kind = "organization_not_supported"
		l.Scope = ScopePass + "entitlement"
	case strings.Contains(lower, "the user is not subscribed to required model plan") || strings.Contains(lower, "no access to clinepass subscription models yet"):
		l.Kind = "not_subscribed"
		l.Scope = ScopePass + "entitlement"
	case status == 429 && strings.Contains(lower, "clinepass limit"):
		l.Kind = "pass_limit"
		l.Window = "unknown"
		switch {
		case strings.Contains(lower, "5-hour") || strings.Contains(lower, "five-hour"):
			l.Window = "five_hour"
		case strings.Contains(lower, "weekly"):
			l.Window = "weekly"
		case strings.Contains(lower, "monthly"):
			l.Window = "monthly"
		}
		l.Scope = ScopePass + l.Window
		maximum = 35 * 24 * time.Hour
	case status == 429 && strings.Contains(lower, "free limit reached on model"):
		l.Kind = "free_model_limit"
		l.Model = requestedModel
		maximum = 48 * time.Hour
		if !ValidModelID(l.Model) {
			m := freeModel.FindStringSubmatch(text)
			if len(m) == 2 {
				l.Model = strings.TrimRight(m[1], ".,")
			}
		}
		if !ValidModelID(l.Model) {
			l.Scope = ScopeFree + "unknown"
		} else {
			l.Scope = ScopeFree + l.Model
		}
	case status == 402:
		l.Kind = "insufficient_balance"
		l.Scope = ScopePayG
	case status == 429:
	default:
		return Limit{}, false
	}
	if l.Kind != "throttle" {
		l.RetryAt = now.Add(5 * time.Minute)
	}
	l.ResetAt = resetFromText(text, now, maximum)
	if at := resetFromHeader(h, now, maximum); at != nil && (l.ResetAt == nil || at.After(*l.ResetAt)) {
		l.ResetAt = at
	}
	return l, true
}
func RateLimitKeys(mode, model string) []string {
	keys := []string{ScopeThrottle}
	switch mode {
	case ModePass:
		for _, window := range []string{"five_hour", "weekly", "monthly", "unknown", "entitlement"} {
			keys = append(keys, ScopePass+window)
		}
	case ModeFree:
		keys = append(keys, ScopeFree+model, ScopeFree+"unknown")
	case ModePayG:
		keys = append(keys, ScopePayG)
	}
	return keys
}
