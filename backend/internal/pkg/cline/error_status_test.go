package cline

import (
	"net/http"
	"strings"
	"testing"
)

func TestClineGenerationErrorStatusOnlyUsesErrorEnvelopes(t *testing.T) {
	for _, tc := range []struct {
		name, payload string
		status        int
		confirmed     bool
	}{
		{"numeric", `{"error":{"code":429}}`, 429, true},
		{"string", `{"error":{"status_code":"402"}}`, 402, true},
		{"typed", `{"type":"error","code":"rate_limit_exceeded"}`, 429, true},
		{"nested_precedence", `{"status":500,"error":{"status":429}}`, 429, true},
		{"invalid_sibling", `{"error":{"code":429},"choices":{}}`, 429, true},
		{"pass", `{"error":{"message":"ClinePass limit weekly"}}`, 429, true},
		{"free", `{"error":"Free limit reached on model vendor/model"}`, 429, true},
		{"entitlement", `{"error":{"message":"No access to ClinePass subscription models yet"}}`, 403, true},
		{"unknown", `{"error":{"message":"generation failed"}}`, 502, true},
		{"finish_reason", `{"choices":[{"finish_reason":"error"}]}`, 502, true},
		{"ordinary_text", `{"choices":[{"delta":{"content":"ClinePass limit weekly"},"code":429}]}`, 0, false},
		{"root_message_without_error", `{"message":"ClinePass limit weekly","code":429}`, 0, false},
		{"null_error", `{"error":null,"code":429}`, 0, false},
		{"malformed", `{"error":`, 0, false},
		{"not_http_status", `{"error":{"code":999}}`, 502, true},
		{"fractional_status", `{"error":{"status":429.5}}`, 502, true},
		{"huge", `{"error":"` + strings.Repeat("x", MaxBodyBytes) + `"}`, http.StatusBadGateway, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			status, confirmed := GenerationErrorStatus([]byte(tc.payload))
			if status != tc.status || confirmed != tc.confirmed {
				t.Fatalf("got %d/%v want %d/%v", status, confirmed, tc.status, tc.confirmed)
			}
		})
	}
}
