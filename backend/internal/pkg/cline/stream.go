package cline

import (
	"bytes"
	"encoding/json"
	"errors"
)

var ErrStreamFailure = errors.New("cline upstream reported an explicit generation failure")

// HasGenerationError inspects structural error fields only, not generated text.
// Parse fields independently so a malformed sibling cannot hide an explicit
// error envelope. Never inspect choices[].delta or generated tool arguments.
func HasGenerationError(payload []byte) bool {
	var p map[string]json.RawMessage
	if json.Unmarshal(payload, &p) != nil || p == nil {
		return false
	}
	if raw, ok := p["error"]; ok && !bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return true
	}
	var eventType string
	if json.Unmarshal(p["type"], &eventType) == nil && (eventType == "error" || eventType == "response.failed") {
		return true
	}
	var choices []map[string]json.RawMessage
	if json.Unmarshal(p["choices"], &choices) != nil {
		return false
	}
	for _, choice := range choices {
		var finishReason string
		if json.Unmarshal(choice["finish_reason"], &finishReason) == nil && finishReason == "error" {
			return true
		}
	}
	return false
}
