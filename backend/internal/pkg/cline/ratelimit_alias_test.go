package cline

import (
	"testing"
	"time"
)

func TestClineFiveHourQuotaAliasesDoNotGuessFromRetryDuration(t *testing.T) {
	now := time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC)
	for _, label := range []string{"5hr", "5 hr", "5hrs", "5h", "5-hour", "five-hour", "5 hours"} {
		limit, ok := Classify(429, nil, []byte(label+" ClinePass limit. Try again in 2h"), "", now)
		if !ok || limit.Window != "five_hour" || limit.ResetAt == nil || !limit.ResetAt.Equal(now.Add(2*time.Hour)) {
			t.Fatalf("alias %s: %+v", label, limit)
		}
	}
	for _, label := range []string{"15hr", "25-hour", ""} {
		limit, ok := Classify(429, nil, []byte(label+" ClinePass limit. Try again in 5h"), "", now)
		if !ok || limit.Window != "unknown" {
			t.Fatalf("inferred quota label from a wait duration: %s %+v", label, limit)
		}
	}
}
