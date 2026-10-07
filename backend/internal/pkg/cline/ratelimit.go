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

// A complete unit and a terminal boundary are required: "1ms", "1h2bad"
// and negative/overflow values must not be truncated into a short cooldown.
const durationUnit = `(?:seconds?|secs?|s|minutes?|mins?|m|hours?|hrs?|h|days?|d)`

var durationText = regexp.MustCompile(`(?i)(?:try\s+again\s+in|(?:the\s+)?limit\s+resets\s+in)\s+((?:[0-9]+(?:\.[0-9]+)?\s*` + durationUnit + `\s*)+)(?:[,;.!"\n\r]|$)`)
var durationPart = regexp.MustCompile(`(?i)([0-9]+(?:\.[0-9]+)?)\s*(` + durationUnit + `)`)
var fiveHourWindow = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])(?:5[ -]?(?:h|hr|hrs|hour|hours)|five[ -]hour)(?:[^a-z0-9]|$)`)
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

// ParseResetAt is shared by the native Cline platform and legacy OpenAI /
// DeepSeek Cline accounts. Call only for a confirmed upstream error. Header and
// body observations are combined monotonically; a short Retry-After must not
// replace a later weekly/monthly reset.
func ParseResetAt(h http.Header, body []byte, now time.Time, maximum time.Duration) *time.Time {
	at := resetFromText(errorMessage(body), now, maximum)
	if headerAt := resetFromHeader(h, now, maximum); headerAt != nil && (at == nil || headerAt.After(*at)) {
		at = headerAt
	}
	return at
}

func resetFromText(text string, now time.Time, maximum time.Duration) *time.Time {
	if len(text) > MaxBodyBytes || maximum <= 0 {
		return nil
	}
	var latest *time.Time
	for _, match := range durationText.FindAllStringSubmatch(text, -1) {
		seconds := float64(0)
		valid := true
		for _, p := range durationPart.FindAllStringSubmatch(match[1], -1) {
			n, err := strconv.ParseFloat(p[1], 64)
			if err != nil || math.IsNaN(n) || math.IsInf(n, 0) {
				valid = false
				break
			}
			switch strings.ToLower(p[2][:1]) {
			case "m":
				n *= 60
			case "h":
				n *= 3600
			case "d":
				n *= 86400
			}
			seconds += n
			if seconds > maximum.Seconds() {
				valid = false
				break
			}
		}
		if !valid || seconds <= 0 {
			continue
		}
		at := now.Add(time.Duration(math.Ceil(seconds)) * time.Second)
		if latest == nil || at.After(*latest) {
			latest = &at
		}
	}
	return latest
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
	// Do not reinterpret authentication/client errors as quota exhaustion.
	if status == http.StatusTooManyRequests || status >= http.StatusInternalServerError {
		if inner, wrapped := WrappedRateLimitBody(body); wrapped {
			status, body = http.StatusTooManyRequests, inner
		}
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
		windowLabel := lower
		if at := durationText.FindStringIndex(lower); at != nil {
			windowLabel = lower[:at[0]]
		}
		switch {
		case fiveHourWindow.MatchString(windowLabel):
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
	l.ResetAt = ParseResetAt(h, body, now, maximum)
	// A confirmed subscription quota with an unknown reset is not a normal
	// request-rate throttle. Use a conservative retry bound rather than retrying
	// every minute. ResetAt stays nil so the UI does not claim an observed reset.
	if l.Kind == "pass_limit" && l.ResetAt == nil {
		fallback := 24 * time.Hour
		switch l.Window {
		case "five_hour":
			fallback = 5 * time.Hour
		case "weekly":
			fallback = 7 * 24 * time.Hour
		case "monthly":
			fallback = 31 * 24 * time.Hour
		}
		l.RetryAt = now.Add(fallback)
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
