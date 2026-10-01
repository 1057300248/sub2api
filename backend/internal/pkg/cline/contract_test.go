package cline

import (
	"net/http"
	"slices"
	"testing"
	"time"
)

func TestClineModeIsolation(t *testing.T) {
	for _, tc := range []struct {
		mode, id string
		allowed  bool
	}{
		{ModePass, "cline-pass/deepseek-v4-flash", true}, {ModePass, "deepseek/deepseek-v4-flash", false},
		{ModePass, "cline-pass/", false}, {ModeFree, "deepseek/deepseek-v4-flash", false},
		{ModePayG, "deepseek/deepseek-v4-flash", true}, {ModePayG, "cline-pass/deepseek-v4-flash", false},
		{ModeUnknown, "cline-pass/model", false}, {ModePass, "cline-pass/*", false},
	} {
		if got := ValidateUpstreamModel(tc.mode, tc.id) == nil; got != tc.allowed {
			t.Fatalf("mode=%s model=%s: allowed=%v", tc.mode, tc.id, got)
		}
	}
}
func TestClineBaseURL(t *testing.T) {
	for _, value := range []string{"", "https://api.cline.bot", "https://API.CLINE.BOT/api/", BaseURL + "/"} {
		got, err := NormalizeBaseURL(value)
		if err != nil || got != BaseURL {
			t.Fatalf("%q: %s %v", value, got, err)
		}
	}
	for _, value := range []string{"http://api.cline.bot", BaseURL + "/chat/completions", "https://key@api.cline.bot", BaseURL + "?key=x", "https://api.cline.bot:444"} {
		if _, err := NormalizeBaseURL(value); err == nil {
			t.Fatalf("accepted %q", value)
		}
	}
	if IsOfficialBase("https://api.cline.bot.attacker.invalid/api/v1") {
		t.Fatal("hostname suffix accepted")
	}
}
func TestClineCatalogBucketsNotSuffixes(t *testing.T) {
	c, err := ParseCatalog([]byte(`{"clinePass":[{"id":"cline-pass/model"}],"free":[{"id":"vendor/model"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if !c.Contains(ModeFree, "vendor/model") || c.Contains(ModePass, "vendor/model") {
		t.Fatal("catalog bucket confused with model name")
	}
	for _, value := range []string{`{}`, `null`, `{"free":null}`, `{"free":{}}`, `{"free":[{"id":"*"}]}`, `{"free":[{"id":"a"},{"id":"a"}]}`} {
		if _, err := ParseCatalog([]byte(value)); err == nil {
			t.Fatalf("accepted %s", value)
		}
	}
}
func TestClineQuotaMissingWindowsRemainUnknown(t *testing.T) {
	w, err := ParseUsage([]byte(`{"success":true,"data":{"limits":[{"type":"weekly","percentUsed":42,"resetsAt":"2026-10-05T00:00:00Z"}]}}`))
	if err != nil || len(w) != 1 || w[0].PercentUsed != 42 || w[0].ResetsAt == nil {
		t.Fatalf("%+v %v", w, err)
	}
	for _, body := range []string{`{}`, `{"success":true,"data":{}}`, `{"success":false,"data":{"limits":[]}}`, `{"success":true,"data":{"limits":[{"type":"weekly"}]}}`, `{"success":true,"data":{"limits":[{"type":"weekly","percentUsed":-1}]}}`, `{"success":true,"data":{"limits":[{"type":"weekly","percentUsed":1,"resetsAt":"invalid"}]}}`} {
		if _, err := ParseUsage([]byte(body)); err == nil {
			t.Fatalf("accepted %s", body)
		}
	}
}
func TestClineLimitClassification(t *testing.T) {
	now := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		body, scope string
		wait        time.Duration
	}{
		{`{"error":{"message":"Daily free limit reached on model deepseek/deepseek-v4-flash. Try again in 23h 59m"}}`, ScopeFree + "deepseek/deepseek-v4-flash", 23*time.Hour + 59*time.Minute},
		{`{"error":{"message":"You have reached your weekly Clinepass limit. The limit resets in 7d, please try again later."}}`, ScopePass + "weekly", 7 * 24 * time.Hour},
		{`{"error":{"message":"You have reached your monthly Clinepass limit. The limit resets in 30d, please try again later."}}`, ScopePass + "monthly", 30 * 24 * time.Hour},
		{`{"error":{"message":"You have reached your 5-hour Clinepass limit. The limit resets in 5h, please try again later."}}`, ScopePass + "five_hour", 5 * time.Hour},
	} {
		l, ok := Classify(429, nil, []byte(tc.body), "", now)
		if !ok || l.Scope != tc.scope || l.ResetAt == nil || l.ResetAt.Sub(now) != tc.wait {
			t.Fatalf("%s: %+v", tc.body, l)
		}
	}
	for _, text := range []string{"Try again in -1h", "Try again in 1hour", "Try again in 1h4w", "Try again in 99999999999999999999999d"} {
		l, _ := Classify(429, nil, []byte(text), "", now)
		if l.ResetAt != nil {
			t.Fatalf("accepted reset %q", text)
		}
	}
	l, _ := Classify(429, http.Header{"Retry-After": []string{"120"}}, []byte("Try again in 30s"), "", now)
	if l.ResetAt == nil || l.ResetAt.Sub(now) != 2*time.Minute {
		t.Fatal("header shortened reset")
	}
	l, _ = Classify(403, nil, []byte(`{"error":{"message":"the user is not subscribed to required model plan"}}`), "", now)
	if l.Kind != "not_subscribed" || l.ResetAt != nil {
		t.Fatal("entitlement was not separated from a known reset")
	}
	if _, ok := Classify(200, nil, []byte(`{"message":"Try again in 1h"}`), "", now); ok {
		t.Fatal("classified model content")
	}
	a, b := RateLimitKeys(ModeFree, "model-a"), RateLimitKeys(ModeFree, "model-b")
	if !slices.Contains(a, ScopeFree+"model-a") || slices.Contains(b, ScopeFree+"model-a") {
		t.Fatal("model cooldown leaked")
	}
}
func TestClineExplicitStreamError(t *testing.T) {
	for _, body := range []string{`{"choices":[{"finish_reason":"error"}]}`, `{"error":{"message":"rate limited"}}`} {
		if !HasGenerationError([]byte(body)) {
			t.Fatal(body)
		}
	}
	for _, body := range []string{`{"choices":[{"delta":{"content":"error: Try again in 1h"},"finish_reason":"stop"}]}`, `{"error":null}`, `[DONE]`} {
		if HasGenerationError([]byte(body)) {
			t.Fatal(body)
		}
	}
}
