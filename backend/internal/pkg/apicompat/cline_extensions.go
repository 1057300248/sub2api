package apicompat

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
)

// ClineReasoningDetails is an opaque vendor extension, not an OpenAI encrypted
// content blob or an Anthropic thinking signature. Only JSON representation is
// changed; unknown fields, large integers and signed/encrypted strings survive.
type ClineReasoningDetails []json.RawMessage

const maxClineReasoningBytes = 1 << 20

var ErrClineReasoningDetails = errors.New("invalid or excessive Cline reasoning_details")

func (d *ClineReasoningDetails) UnmarshalJSON(raw []byte) error {
	if len(raw) > maxClineReasoningBytes {
		return ErrClineReasoningDetails
	}
	var list []json.RawMessage
	if json.Unmarshal(raw, &list) != nil || len(list) > 2048 {
		return ErrClineReasoningDetails
	}
	for _, v := range list {
		var obj map[string]json.RawMessage
		if json.Unmarshal(v, &obj) != nil || obj == nil {
			return ErrClineReasoningDetails
		}
	}
	*d = list
	return nil
}

type clineReasoningPart struct {
	metadata map[string]json.RawMessage
	strings  map[string]*strings.Builder
}
type clineReasoningAccumulator struct {
	parts   []clineReasoningPart
	indexes map[string]int
	bytes   int
	err     error
}

// Merge only documented text/summary/encrypted delta string fields for the
// same declared type+index (or id). Unknown types remain independent opaque
// objects in arrival order. Conflicting metadata fails instead of corrupting a
// signature. Builders avoid quadratic copying for token-sized text deltas.
func (a *clineReasoningAccumulator) Add(details ClineReasoningDetails) {
	if a.err != nil {
		return
	}
	if a.indexes == nil {
		a.indexes = map[string]int{}
	}
	for _, raw := range details {
		a.bytes += len(raw)
		if a.bytes > maxClineReasoningBytes {
			a.err = ErrClineReasoningDetails
			return
		}
		var fields map[string]json.RawMessage
		if json.Unmarshal(raw, &fields) != nil || fields == nil {
			a.err = ErrClineReasoningDetails
			return
		}
		typ := rawString(fields["type"])
		known := typ == "reasoning.text" || typ == "reasoning.summary" || typ == "reasoning.encrypted"
		key := ""
		if known {
			var index int
			if raw := bytes.TrimSpace(fields["index"]); len(raw) > 0 && string(raw) != "null" && json.Unmarshal(raw, &index) == nil && index >= 0 && index < 65536 {
				key = typ + "/index/" + string(raw)
			}
			if key == "" {
				if id := rawString(fields["id"]); id != "" && len(id) <= 256 {
					key = typ + "/id/" + id
				}
			}
		}
		pos, exists := a.indexes[key]
		if key == "" || !exists {
			if len(a.parts) >= 2048 {
				a.err = ErrClineReasoningDetails
				return
			}
			pos = len(a.parts)
			a.parts = append(a.parts, clineReasoningPart{metadata: map[string]json.RawMessage{}, strings: map[string]*strings.Builder{}})
			if key != "" {
				a.indexes[key] = pos
			}
		}
		part := &a.parts[pos]
		for name, value := range fields {
			deltaField := known && (name == "text" || name == "summary" || name == "signature" || name == "data")
			if deltaField {
				var v string
				if json.Unmarshal(value, &v) == nil && string(value) != "null" {
					if _, exists := part.metadata[name]; exists {
						a.err = ErrClineReasoningDetails
						return
					}
					if part.strings[name] == nil {
						part.strings[name] = &strings.Builder{}
					}
					_, _ = part.strings[name].WriteString(v)
					continue
				}
			}
			if _, exists := part.strings[name]; exists {
				a.err = ErrClineReasoningDetails
				return
			}
			if old, exists := part.metadata[name]; exists {
				var left, right bytes.Buffer
				if json.Compact(&left, old) != nil || json.Compact(&right, value) != nil || !bytes.Equal(left.Bytes(), right.Bytes()) {
					a.err = ErrClineReasoningDetails
					return
				}
			} else {
				part.metadata[name] = append(json.RawMessage(nil), value...)
			}
		}
	}
}
func (a *clineReasoningAccumulator) Values() ClineReasoningDetails {
	if a.err != nil {
		return nil
	}
	var out ClineReasoningDetails
	for _, part := range a.parts {
		fields := make(map[string]json.RawMessage, len(part.metadata)+len(part.strings))
		for k, v := range part.metadata {
			fields[k] = v
		}
		for k, b := range part.strings {
			fields[k], _ = json.Marshal(b.String())
		}
		raw, err := json.Marshal(fields)
		if err != nil {
			return nil
		}
		out = append(out, raw)
	}
	return out
}
func (s *ChatCompletionsToResponsesStreamState) ClineDetailsError() error { return s.clineDetails.err }
func (s *ChatCompletionsToAnthropicStreamState) ClineDetailsError() error { return s.clineDetails.err }

func attachClineDetailsToResponses(outputs []ResponsesOutput, details ClineReasoningDetails) {
	if len(details) == 0 {
		return
	}
	for i := range outputs {
		if outputs[i].Type != "reasoning" {
			outputs[i].ReasoningDetails = details
			return
		}
	}
}
func attachClineDetailsToAnthropic(blocks []AnthropicContentBlock, details ClineReasoningDetails) {
	if len(details) == 0 {
		return
	}
	for i := range blocks {
		if blocks[i].Type == "text" || blocks[i].Type == "tool_use" {
			blocks[i].ReasoningDetails = details
			return
		}
	}
}

// This is upstream self-reported provenance, not independently attested model
// execution. It is never used to change model mapping, account mode or billing.
type ClineProviderObservation struct {
	Source   string `json:"source"`
	Provider string `json:"provider,omitempty"`
	Conflict bool   `json:"conflict,omitempty"`
}
type clineProviderObserver struct {
	name     string
	conflict bool
}

func (o *clineProviderObserver) Observe(raw json.RawMessage) {
	var name string
	if json.Unmarshal(raw, &name) != nil {
		return
	}
	name = strings.TrimSpace(name)
	if len(name) == 0 || len(name) > 128 {
		return
	}
	for _, c := range name {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune(" ._/-", c)) {
			return
		}
	}
	if o.name == "" {
		o.name = name
	} else if !strings.EqualFold(o.name, name) {
		o.conflict = true
	}
}
func (o *clineProviderObserver) View() *ClineProviderObservation {
	if o.name == "" {
		return nil
	}
	out := &ClineProviderObservation{Source: "upstream_response", Conflict: o.conflict}
	if !o.conflict {
		out.Provider = o.name
	}
	return out
}
func observedClineProvider(raw json.RawMessage) *ClineProviderObservation {
	var o clineProviderObserver
	o.Observe(raw)
	return o.View()
}
