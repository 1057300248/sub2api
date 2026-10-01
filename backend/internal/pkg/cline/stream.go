package cline

import (
	"encoding/json"
	"errors"
)

var ErrStreamFailure = errors.New("Cline upstream reported an explicit generation failure")

// HasGenerationError inspects structural error fields only, not generated text.
func HasGenerationError(payload []byte) bool {
	var p struct {
		Error   json.RawMessage `json:"error"`
		Choices []struct {
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
	}
	if json.Unmarshal(payload, &p) != nil {
		return false
	}
	if len(p.Error) > 0 && string(p.Error) != "null" {
		return true
	}
	for _, choice := range p.Choices {
		if choice.FinishReason == "error" {
			return true
		}
	}
	return false
}
