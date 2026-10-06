package service

import (
	"bytes"
	"encoding/json"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

var ErrClineRequestContract = infraerrors.BadRequest("UNSUPPORTED_CLINE_REQUEST", "Cline cannot preserve the requested tool choice or reasoning context; use supported tools and the same authenticated account/model")

func isClineProtocolAccount(a *Account) bool {
	return a != nil && (a.IsCline() || isLegacyClineAccount(a))
}

// field lookup matches encoding/json's case folding but rejects ambiguous
// aliases instead of silently resolving different source/target interpretations.
func clineContractField(m map[string]json.RawMessage, name string) (json.RawMessage, bool) {
	var found json.RawMessage
	for k, v := range m {
		if strings.EqualFold(k, name) {
			if found != nil {
				return nil, false
			}
			found = v
		}
	}
	return found, true
}
func clineForcedChoice(raw json.RawMessage) bool {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return false
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text != "auto" && text != "none"
	}
	var v struct {
		Type string `json:"type"`
		Mode string `json:"mode"`
	}
	if json.Unmarshal(raw, &v) != nil {
		return true
	}
	return v.Type != "auto" && v.Type != "none" && (v.Type != "allowed_tools" || v.Mode != "auto")
}

// A forced tool must survive lowering. Merely dropping server tools and then
// dropping tool_choice changes the user's request into an ordinary text answer.
func validateClineLoweredToolChoice(source, target map[string]json.RawMessage) error {
	choice, ok := clineContractField(source, "tool_choice")
	if !ok {
		return ErrClineRequestContract
	}
	if !clineForcedChoice(choice) {
		return nil
	}
	out, ok := clineContractField(target, "tool_choice")
	if !ok || !clineForcedChoice(out) {
		return ErrClineRequestContract
	}
	var tools []apicompat.ChatTool
	raw, ok := clineContractField(target, "tools")
	if !ok || json.Unmarshal(raw, &tools) != nil || len(tools) == 0 {
		return ErrClineRequestContract
	}
	names := map[string]bool{}
	for _, t := range tools {
		if t.Type == "function" && t.Function != nil && t.Function.Name != "" {
			names[t.Function.Name] = true
		}
	}
	if len(names) == 0 {
		return ErrClineRequestContract
	}
	var text string
	if json.Unmarshal(out, &text) == nil {
		if text == "required" {
			return nil
		}
		return ErrClineRequestContract
	}
	var v struct {
		Type     string `json:"type"`
		Function struct {
			Name string `json:"name"`
		} `json:"function"`
	}
	if json.Unmarshal(out, &v) != nil || v.Type != "function" || !names[v.Function.Name] {
		return ErrClineRequestContract
	}
	return nil
}

// Cline defaults omitted stream to true. Always serialize the gateway's actual
// selected mode, including false after typed Responses/Messages conversion.
func normalizeClineFinalRequest(account *Account, body []byte, stream bool) ([]byte, error) {
	if !isClineProtocolAccount(account) {
		return body, nil
	}
	var request map[string]json.RawMessage
	if json.Unmarshal(body, &request) != nil || request == nil {
		return nil, ErrClineRequestContract
	}
	raw, ok := clineContractField(request, "stream")
	if !ok {
		return nil, ErrClineRequestContract
	}
	if raw != nil {
		if !bytes.Equal(bytes.TrimSpace(raw), []byte("true")) && !bytes.Equal(bytes.TrimSpace(raw), []byte("false")) {
			return nil, ErrClineRequestContract
		}
	}
	if err := validateClineLoweredToolChoice(request, request); err != nil {
		return nil, err
	}
	for k := range request {
		if strings.EqualFold(k, "stream") {
			delete(request, k)
		}
	}
	request["stream"] = json.RawMessage("false")
	if stream {
		request["stream"] = json.RawMessage("true")
	}
	return json.Marshal(request)
}
