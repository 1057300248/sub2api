package cline

import (
	"encoding/json"
	"strconv"
	"strings"
)

// WrappedRateLimitBody recognizes the observed Cline/Vercel error wrapper, not
// arbitrary prose containing a status code. Callers retain the original HTTP
// status/body for diagnostics and use this inner error only for scheduling.
func WrappedRateLimitBody(payload []byte) ([]byte, bool) {
	if len(payload) > MaxBodyBytes || !HasGenerationError(payload) {
		return nil, false
	}
	// A confirmed authentication/client/balance error takes precedence over
	// contradictory wrapped text. In particular it must not become a cooldown.
	var root map[string]json.RawMessage
	if json.Unmarshal(payload, &root) != nil {
		return nil, false
	}
	var nested map[string]json.RawMessage
	_ = json.Unmarshal(root["error"], &nested)
	for _, fields := range []map[string]json.RawMessage{nested, root} {
		for _, key := range []string{"status", "status_code", "code"} {
			var value json.Number
			if json.Unmarshal(fields[key], &value) == nil {
				if status, err := strconv.Atoi(value.String()); err == nil && status >= 400 && status < 500 && status != 429 {
					return nil, false
				}
			}
		}
		for _, key := range []string{"code", "type"} {
			var value string
			if json.Unmarshal(fields[key], &value) == nil && value == "insufficient_balance" {
				return nil, false
			}
		}
	}
	message := strings.TrimSpace(errorMessage(payload))
	lower := strings.ToLower(message)
	if !strings.HasPrefix(lower, "failed to create stream:") && !strings.HasPrefix(lower, "inference request failed:") {
		return nil, false
	}
	const marker = "request failed with status 429:"
	index := strings.Index(lower, marker)
	if index < 0 || !strings.Contains(lower[:index], "failed to generate stream from vercel:") {
		return nil, false
	}
	inner := []byte(strings.TrimSpace(message[index+len(marker):]))
	var envelope struct {
		Error *struct {
			Message string `json:"message"`
			Code    string `json:"code"`
			Type    string `json:"type"`
		} `json:"error"`
	}
	if json.Unmarshal(inner, &envelope) != nil || envelope.Error == nil {
		return nil, false
	}
	err := envelope.Error
	typed := err.Code == "rate_limit_exceeded" || err.Code == "rate_limit_error" || err.Code == "rate_limited" ||
		err.Type == "rate_limit_exceeded" || err.Type == "rate_limit_error" || err.Type == "rate_limited"
	text := strings.ToLower(strings.TrimSpace(err.Message))
	teamLimit := strings.HasPrefix(text, "rate limit exceeded for ") && strings.Contains(text, "this team's limit")
	if !typed && !teamLimit {
		return nil, false
	}
	return inner, true
}
