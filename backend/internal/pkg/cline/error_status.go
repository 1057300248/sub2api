package cline

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
)

// GenerationErrorStatus interprets status hints only inside a confirmed error
// envelope. Unknown failures remain 502, never an invented quota exhaustion.
func GenerationErrorStatus(payload []byte) (int, bool) {
	if !HasGenerationError(payload) {
		return 0, false
	}
	if len(payload) > MaxBodyBytes {
		return http.StatusBadGateway, true
	}
	var root map[string]json.RawMessage
	if json.Unmarshal(payload, &root) != nil {
		return http.StatusBadGateway, true
	}
	var nested map[string]json.RawMessage
	_ = json.Unmarshal(root["error"], &nested)
	for _, fields := range []map[string]json.RawMessage{nested, root} {
		for _, key := range []string{"status", "status_code", "code"} {
			var number int
			if json.Unmarshal(fields[key], &number) == nil && number >= 400 && number <= 599 {
				return number, true
			}
			var text string
			if json.Unmarshal(fields[key], &text) == nil {
				if n, err := strconv.Atoi(text); err == nil && n >= 400 && n <= 599 {
					return n, true
				}
			}
		}
		for _, key := range []string{"code", "type"} {
			var value string
			if json.Unmarshal(fields[key], &value) != nil {
				continue
			}
			switch value {
			case "rate_limit_error", "rate_limit_exceeded", "rate_limited":
				return http.StatusTooManyRequests, true
			case "insufficient_balance":
				return http.StatusPaymentRequired, true
			}
		}
	}
	// These are the same provider-specific phrases accepted by Classify. Do
	// not scan choices, delta, reasoning, tool arguments or arbitrary prose.
	message := strings.ToLower(errorMessage(payload))
	switch {
	case strings.Contains(message, "clinepass limit"), strings.Contains(message, "free limit reached on model"):
		return http.StatusTooManyRequests, true
	case strings.Contains(message, "organization accounts cannot use individual model inference subscriptions"),
		strings.Contains(message, "the user is not subscribed to required model plan"),
		strings.Contains(message, "no access to clinepass subscription models yet"):
		return http.StatusForbidden, true
	default:
		return http.StatusBadGateway, true
	}
}
