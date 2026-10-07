package cline

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestClineIncidentResetObservations(t *testing.T) {
	now := time.Date(2026, 10, 7, 7, 26, 0, 0, time.UTC)
	for _, tc := range []struct {
		name, body, retry string
		want              time.Duration
	}{
		{"production weekly text", `{"error":{"message":"You have reached your weekly Clinepass limit. The limit resets in 5d 21h"}}`, "", 141 * time.Hour},
		{"JSON error string", `{"error":"The limit resets in 5d21h"}`, "", 141 * time.Hour},
		{"long units", "The limit resets in 5 days 21 hours", "", 141 * time.Hour},
		{"fraction rounds up", "Try again in 0.25s", "", time.Second},
		{"short header cannot shorten", "The limit resets in 5d21h", "60", 141 * time.Hour},
		{"later header", "Try again in 1m", "3600", time.Hour},
		{"two reset observations", "Try again in 1m. The limit resets in 5d21h", "", 141 * time.Hour},
		{"monthly", "The limit resets in 28d 4h", "", 676 * time.Hour},
		{"malformed suffix", "Try again in 1h2bad", "", 0},
		{"milliseconds not minutes", "Try again in 1ms", "", 0},
		{"negative", "Try again in -1h", "", 0},
		{"zero", "Try again in 0s", "", 0},
		{"overflow", "Try again in " + strings.Repeat("9", 400) + "h", "", 0},
		{"too long", "The limit resets in 36d", "", 0},
		{"unknown JSON field is not an error", `{"output":"Try again in 5d"}`, "", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := http.Header{}
			if tc.retry != "" {
				h.Set("Retry-After", tc.retry)
			}
			got := ParseResetAt(h, []byte(tc.body), now, 35*24*time.Hour)
			if tc.want == 0 {
				if got != nil {
					t.Fatalf("unexpected reset %v", got)
				}
				return
			}
			if got == nil || !got.Equal(now.Add(tc.want)) {
				t.Fatalf("reset=%v want=%v", got, now.Add(tc.want))
			}
		})
	}
	h := http.Header{"Retry-After": []string{now.Add(48 * time.Hour).Format(http.TimeFormat)}}
	if at := ParseResetAt(h, []byte("Try again in 1h"), now, 35*24*time.Hour); at == nil || !at.Equal(now.Add(48*time.Hour)) {
		t.Fatalf("HTTP-date reset %v", at)
	}
}

func TestClineIncidentUnknownQuotaDoesNotRetryEveryMinute(t *testing.T) {
	now := time.Date(2026, 10, 7, 7, 26, 0, 0, time.UTC)
	for _, tc := range []struct {
		label    string
		fallback time.Duration
	}{{"weekly", 7 * 24 * time.Hour}, {"monthly", 31 * 24 * time.Hour}, {"5-hour", 5 * time.Hour}, {"", 24 * time.Hour}} {
		limit, ok := Classify(429, nil, []byte("You have reached your "+tc.label+" Clinepass limit. The limit resets in malformed"), "model", now)
		if !ok || limit.ResetAt != nil || !limit.Until().Equal(now.Add(tc.fallback)) {
			t.Fatalf("%s: %+v handled=%v", tc.label, limit, ok)
		}
	}
	if _, ok := Classify(200, nil, []byte("You have reached your weekly Clinepass limit. The limit resets in 5d 21h"), "model", now); ok {
		t.Fatal("model output classified as quota")
	}
}
