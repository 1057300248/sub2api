package apicompat

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func clineDetailsFixture(t *testing.T) ClineReasoningDetails {
	t.Helper()
	var d ClineReasoningDetails
	if err := json.Unmarshal([]byte(`[{"type":"reasoning.encrypted","index":0,"data":"opaque+/=","signature":"signed.fixture","format":"vendor-v1","counter":9007199254740993}]`), &d); err != nil {
		t.Fatal(err)
	}
	return d
}
func assertClineDetails(t *testing.T, d ClineReasoningDetails) {
	t.Helper()
	b, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range []string{"opaque+/=", "signed.fixture", "9007199254740993"} {
		if !strings.Contains(string(b), v) {
			t.Fatalf("reasoning detail lost: %s", v)
		}
	}
}

func TestClineReasoningDetailsJSONAndRoundTrip(t *testing.T) {
	details := clineDetailsFixture(t)
	resp := &ChatCompletionsResponse{ID: "fixture", Provider: json.RawMessage(`"DeepSeek"`), Choices: []ChatChoice{{Message: ChatMessage{Role: "assistant", Content: json.RawMessage(`"answer"`), ReasoningDetails: details}, FinishReason: "stop"}}}
	out := ChatCompletionsResponseToResponses(resp, "public-model", nil, nil, false, nil)
	if len(out.Output) != 1 {
		t.Fatal("unexpected output")
	}
	assertClineDetails(t, out.Output[0].ReasoningDetails)
	if out.Output[0].EncryptedContent != "" {
		t.Fatal("must not forge native encrypted_content")
	}
	if out.ClineProvider == nil || out.ClineProvider.Provider != "DeepSeek" {
		t.Fatal("missing observed provider")
	}
	raw, err := json.Marshal(out.Output)
	if err != nil {
		t.Fatal(err)
	}
	back, err := ResponsesToChatCompletionsRequest(&ResponsesRequest{Model: "public-model", Input: raw})
	if err != nil {
		t.Fatal(err)
	}
	assertClineDetails(t, back.Messages[0].ReasoningDetails)
	a := ChatCompletionsResponseToAnthropic(resp, "public-model")
	assertClineDetails(t, a.Content[0].ReasoningDetails)
	raw, err = json.Marshal(a.Content)
	if err != nil {
		t.Fatal(err)
	}
	back, err = AnthropicToChatCompletionsRequest(&AnthropicRequest{Model: "public-model", Messages: []AnthropicMessage{{Role: "assistant", Content: raw}}})
	if err != nil {
		t.Fatal(err)
	}
	assertClineDetails(t, back.Messages[0].ReasoningDetails)
}

func TestClineReasoningDetailsStreamLateFragmentsAndToolIDs(t *testing.T) {
	for _, tool := range []bool{false, true} {
		r := NewChatCompletionsToResponsesStreamState("public-model")
		a := NewChatCompletionsToAnthropicStreamState("public-model")
		first := `{"provider":"DeepSeek","choices":[{"index":0,"delta":{"content":"answer"}}]}`
		if tool {
			first = `{"provider":"DeepSeek","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_fixture","type":"function","function":{"name":"lookup","arguments":"{}"}}]}}]}`
		}
		for _, raw := range []string{first,
			`{"choices":[{"index":0,"delta":{"reasoning_details":[{"type":"reasoning.encrypted","index":0,"data":"opaque","signature":"signed.","format":"vendor-v1","counter":9007199254740993}]}}]}`,
			`{"choices":[{"index":0,"delta":{"reasoning_details":[{"type":"reasoning.encrypted","index":0,"data":"+/=","signature":"fixture"}]},"finish_reason":"stop"}]}`} {
			var chunk ChatCompletionsChunk
			if err := json.Unmarshal([]byte(raw), &chunk); err != nil {
				t.Fatal(err)
			}
			ChatCompletionsChunkToResponsesEvents(&chunk, r)
			ChatCompletionsChunkToAnthropicEvents(&chunk, a)
		}
		events := FinalizeChatCompletionsResponsesStream(r)
		var final *ResponsesResponse
		for _, e := range events {
			if e.Type == "response.completed" {
				final = e.Response
			}
		}
		if final == nil || len(final.Output) != 1 {
			t.Fatal("no complete output")
		}
		assertClineDetails(t, final.Output[0].ReasoningDetails)
		matched := false
		for _, e := range events {
			if e.Type == "response.output_item.done" && e.Item.ID == final.Output[0].ID {
				assertClineDetails(t, e.Item.ReasoningDetails)
				matched = true
			}
		}
		if !matched {
			t.Fatal("final item ID does not match done event")
		}
		for _, e := range FinalizeChatCompletionsAnthropicStream(a) {
			if e.Type == "message_delta" {
				assertClineDetails(t, e.Delta.ReasoningDetails)
			}
		}
	}
}

func TestClineReasoningDetailsToolPassbackAndTurnIsolation(t *testing.T) {
	req := &ResponsesRequest{Model: "public-model", Input: json.RawMessage(`[
 {"role":"user","content":"hello"},
 {"type":"reasoning","reasoning_details":[{"type":"reasoning.encrypted","data":"opaque+/=","signature":"signed.fixture","counter":9007199254740993}]},
 {"type":"function_call","call_id":"call_one","name":"lookup","arguments":"{}"},
 {"type":"function_call_output","call_id":"call_one","output":"done"},
 {"role":"user","content":"new turn"},
 {"role":"assistant","content":"next"}]`)}
	out, err := ResponsesToChatCompletionsRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, m := range out.Messages {
		if len(m.ToolCalls) > 0 {
			assertClineDetails(t, m.ReasoningDetails)
			found = true
		}
		if string(m.Content) == `"next"` && len(m.ReasoningDetails) > 0 {
			t.Fatal("cross-turn details leak")
		}
	}
	if !found {
		t.Fatal("missing assistant tool turn")
	}
}

func TestClineReasoningDetailsBoundsAndConflictsFail(t *testing.T) {
	for _, raw := range []string{`{}`, `[1]`, `[null]`, `["ciphertext"]`, `[{}] trailing`, strings.Repeat("x", maxClineReasoningBytes+1)} {
		var d ClineReasoningDetails
		if json.Unmarshal([]byte(raw), &d) == nil {
			t.Fatal("accepted malformed details")
		}
	}
	var a clineReasoningAccumulator
	a.Add(ClineReasoningDetails{json.RawMessage(`{"type":"reasoning.text","index":0,"format":"a","text":"hello"}`)})
	a.Add(ClineReasoningDetails{json.RawMessage(`{"type":"reasoning.text","index":0,"format":"b","text":"world"}`)})
	if !errors.Is(a.err, ErrClineReasoningDetails) {
		t.Fatal("ambiguous signature metadata must fail")
	}
	state := NewChatCompletionsToResponsesStreamState("public-model")
	state.clineDetails.Add(ClineReasoningDetails{json.RawMessage(strings.Repeat("x", maxClineReasoningBytes+1))})
	if state.ClineDetailsError() == nil || len(FinalizeChatCompletionsResponsesStream(state)) != 0 {
		t.Fatal("excessive details cannot emit completed")
	}
}

func TestClineProviderObservationNeverInfersOrBills(t *testing.T) {
	var o clineProviderObserver
	for _, raw := range []string{`null`, `{}`, `"bad\nprovider"`, `42`} {
		o.Observe(json.RawMessage(raw))
	}
	if o.View() != nil {
		t.Fatal("invalid/missing provider must be unknown")
	}
	o.Observe(json.RawMessage(`"DeepSeek"`))
	o.Observe(json.RawMessage(`"deepseek"`))
	if o.View().Conflict || o.View().Provider != "DeepSeek" {
		t.Fatal("consistent provider lost")
	}
	o.Observe(json.RawMessage(`"Alibaba"`))
	if !o.View().Conflict || o.View().Provider != "" {
		t.Fatal("conflicting self-report must not select a provider")
	}
}

func TestClineReasoningDetailsOrphanHistoryFailsInsteadOfDropping(t *testing.T) {
	for _, input := range []string{
		`[{"type":"reasoning","reasoning_details":[{"type":"reasoning.encrypted","data":"opaque"}]}]`,
		`[{"type":"reasoning","reasoning_details":[{"type":"reasoning.encrypted","data":"opaque"}]},{"role":"user","content":"new turn"}]`,
		`[{"type":"function_call","name":"lookup","call_id":"unanswered","arguments":"{}","reasoning_details":[{"type":"reasoning.encrypted","data":"opaque"}]}]`,
	} {
		_, err := ResponsesToChatCompletionsRequest(&ResponsesRequest{Input: json.RawMessage(input)})
		if !errors.Is(err, ErrClineReasoningDetails) {
			t.Fatal("orphan signed history must not disappear", err)
		}
	}
}
