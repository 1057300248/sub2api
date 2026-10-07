package cline

import "encoding/json"

// failureUsageEvent preserves only explicitly reported, structurally valid
// cumulative usage on a confirmed terminal error. The caller has already
// established the failure. Never copy model output, error text, tool arguments
// or any unrecognized field into a synthetic usage-only event.
func failureUsageEvent(payload []byte) []byte {
	if len(payload) > MaxBodyBytes {
		return nil
	}
	var envelope struct {
		Usage json.RawMessage `json:"usage"`
	}
	if json.Unmarshal(payload, &envelope) != nil || len(envelope.Usage) == 0 {
		return nil
	}
	var usage struct {
		Prompt        *int64 `json:"prompt_tokens"`
		Completion    *int64 `json:"completion_tokens"`
		Total         *int64 `json:"total_tokens,omitempty"`
		PromptDetails *struct {
			Cached *int64 `json:"cached_tokens,omitempty"`
		} `json:"prompt_tokens_details,omitempty"`
		CompletionDetails *struct {
			Reasoning *int64 `json:"reasoning_tokens,omitempty"`
		} `json:"completion_tokens_details,omitempty"`
	}
	if json.Unmarshal(envelope.Usage, &usage) != nil || usage.Prompt == nil || usage.Completion == nil {
		return nil
	}
	valid := func(n *int64) bool { return n == nil || (*n >= 0 && *n <= 1<<31-1) }
	if !valid(usage.Prompt) || !valid(usage.Completion) || !valid(usage.Total) {
		return nil
	}
	if usage.Total != nil && *usage.Total != *usage.Prompt+*usage.Completion {
		return nil
	}
	if usage.PromptDetails != nil && (!valid(usage.PromptDetails.Cached) || usage.PromptDetails.Cached != nil && *usage.PromptDetails.Cached > *usage.Prompt) {
		return nil
	}
	if usage.CompletionDetails != nil && (!valid(usage.CompletionDetails.Reasoning) || usage.CompletionDetails.Reasoning != nil && *usage.CompletionDetails.Reasoning > *usage.Completion) {
		return nil
	}
	body, err := json.Marshal(struct {
		Choices []any `json:"choices"`
		Usage   any   `json:"usage"`
	}{Choices: []any{}, Usage: usage})
	if err != nil {
		return nil
	}
	return append(append([]byte("data: "), body...), '\n', '\n')
}
