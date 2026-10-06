package service

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/Wei-Shaw/sub2api/internal/pkg/cline"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/gin-gonic/gin"
)

const clineReasoningContextKey = "sub2api.cline.reasoning.v1"

var ErrClineReasoningUnavailable = infraerrors.New(http.StatusBadGateway, "CLINE_REASONING_REPLAY_UNAVAILABLE", "Cline returned reasoning details that could not be safely preserved")

type clineReasoningContext struct{ codec *cline.ReasoningCodec }

// Stable secret + caller key + verified upstream subject/model. Different API
// keys belonging to the same verified upstream subscription may replay after
// rotation, but an unrelated account/model/caller cannot decrypt the envelope.
func (s *OpenAIGatewayService) configureClineReasoning(c *gin.Context, a *Account, model string) *clineReasoningContext {
	if c == nil {
		return nil
	}
	c.Set(clineReasoningContextKey, nil)
	if !isClineProtocolAccount(a) {
		return nil
	}
	result := &clineReasoningContext{}
	c.Set(clineReasoningContextKey, result)
	if s == nil || s.cfg == nil {
		return result
	}
	raw, ok := c.Get("api_key")
	if !ok {
		return result
	}
	key, ok := raw.(*APIKey)
	if !ok || key == nil || key.ID <= 0 || key.UserID <= 0 {
		return result
	}
	scope := ClineCredentialFingerprint(a)
	base := a.GetClineBaseURL()
	if a.IsCline() {
		if state := a.GetClineState(); state != nil && cline.ValidSubjectHash(state.Identity) && state.IdentityVerifiedAt != nil {
			scope = "subject:" + state.Identity
		}
	} else {
		digest := sha256.Sum256([]byte(a.GetOpenAIProtocolAPIKey()))
		scope = fmt.Sprintf("legacy:%d:%x", a.ID, digest)
		base = a.GetOpenAIBaseURL()
	}
	binding, _ := json.Marshal([]any{"sub2api-cline-v1", key.UserID, key.ID, base, a.GetClineMode(), scope, model})
	codec, err := cline.NewReasoningCodec(s.cfg.JWT.Secret, string(binding))
	if err == nil {
		result.codec = codec
	}
	return result
}
func clineReasoningFromContext(c *gin.Context) *clineReasoningContext {
	if c == nil {
		return nil
	}
	v, _ := c.Get(clineReasoningContextKey)
	value, _ := v.(*clineReasoningContext)
	return value
}

// Called after protocol lowering and before the send boundary. Matching by tool
// call IDs (or an unambiguous assistant text digest) works even when Responses
// places the opaque reasoning item after the tool/message item. Unknown or
// mismatched artifacts fail explicitly, never silently disappear into a cache.
func (s *OpenAIGatewayService) restoreClineReasoning(c *gin.Context, a *Account, source, body []byte, protocol string) ([]byte, error) {
	if !isClineProtocolAccount(a) {
		return body, nil
	}
	var out map[string]json.RawMessage
	if json.Unmarshal(body, &out) != nil {
		return nil, ErrClineRequestContract
	}
	var model string
	if json.Unmarshal(out["model"], &model) != nil {
		return nil, ErrClineRequestContract
	}
	state := s.configureClineReasoning(c, a, model)
	tokens, err := clineReasoningTokens(source, protocol)
	if err != nil {
		return nil, err
	}
	if len(tokens) == 0 {
		return body, nil
	}
	if state == nil || state.codec == nil {
		return nil, ErrClineRequestContract
	}
	var messages []map[string]json.RawMessage
	if json.Unmarshal(out["messages"], &messages) != nil {
		return nil, ErrClineRequestContract
	}
	seen := map[string]bool{}
	for _, token := range tokens {
		if seen[token] {
			continue
		}
		seen[token] = true
		envelope, e := state.codec.Open(token, time.Now().UTC())
		if e != nil {
			return nil, ErrClineRequestContract
		}
		found := -1
		for i, msg := range messages {
			var role string
			_ = json.Unmarshal(msg["role"], &role)
			if role != "assistant" {
				continue
			}
			var calls []apicompat.ChatToolCall
			_ = json.Unmarshal(msg["tool_calls"], &calls)
			match := false
			if len(envelope.Calls) > 0 {
				ids := map[string]bool{}
				for _, call := range calls {
					ids[call.ID] = true
				}
				match = true
				for _, id := range envelope.Calls {
					if !ids[id] {
						match = false
						break
					}
				}
			} else if len(calls) == 0 {
				match = clineTextDigest(clineChatText(msg["content"])) == envelope.TextDigest
			}
			if match {
				if found >= 0 {
					return nil, ErrClineRequestContract
				}
				found = i
			}
		}
		if found < 0 {
			return nil, ErrClineRequestContract
		}
		if existing := messages[found]["reasoning_details"]; len(existing) > 0 && !bytes.Equal(existing, envelope.Details) {
			return nil, ErrClineRequestContract
		}
		messages[found]["reasoning_details"] = envelope.Details
	}
	out["messages"], err = json.Marshal(messages)
	if err != nil {
		return nil, ErrClineRequestContract
	}
	return json.Marshal(out)
}
func clineReasoningTokens(body []byte, protocol string) ([]string, error) {
	var source struct {
		Input    json.RawMessage              `json:"input"`
		Messages []apicompat.AnthropicMessage `json:"messages"`
	}
	if json.Unmarshal(body, &source) != nil {
		return nil, ErrClineRequestContract
	}
	var tokens []string
	if protocol == "responses" {
		var items []struct {
			Type      string `json:"type"`
			Encrypted string `json:"encrypted_content"`
		}
		if json.Unmarshal(source.Input, &items) == nil {
			for _, item := range items {
				if item.Type == "reasoning" && item.Encrypted != "" {
					tokens = append(tokens, item.Encrypted)
				}
			}
		}
	} else if protocol == "messages" {
		for _, msg := range source.Messages {
			if msg.Role != "assistant" {
				continue
			}
			var blocks []apicompat.AnthropicContentBlock
			if json.Unmarshal(msg.Content, &blocks) != nil {
				continue
			}
			for _, b := range blocks {
				if b.Type == "redacted_thinking" && b.Data != "" {
					tokens = append(tokens, b.Data)
				}
			}
		}
	}
	if len(tokens) > 128 {
		return nil, ErrClineRequestContract
	}
	for _, t := range tokens {
		if !strings.HasPrefix(t, cline.ReasoningPrefix) || len(t) > 3*cline.MaxReasoningBytes {
			return nil, ErrClineRequestContract
		}
	}
	return tokens, nil
}
func clineChatText(raw json.RawMessage) string {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text
	}
	var parts []apicompat.ChatContentPart
	if json.Unmarshal(raw, &parts) != nil {
		return ""
	}
	var texts []string
	for _, p := range parts {
		if p.Type == "text" && p.Text != "" {
			texts = append(texts, p.Text)
		}
	}
	return strings.Join(texts, "\n\n")
}
func clineTextDigest(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

type clineReasoningAccumulator struct {
	context *clineReasoningContext
	details json.RawMessage
	text    hash.Hash
	calls   map[int]string
	err     error
}

func newClineReasoningAccumulator(c *gin.Context) *clineReasoningAccumulator {
	ctx := clineReasoningFromContext(c)
	if ctx == nil {
		return nil
	}
	return &clineReasoningAccumulator{context: ctx, text: sha256.New(), calls: map[int]string{}}
}
func (a *clineReasoningAccumulator) observe(chunk *apicompat.ChatCompletionsChunk) {
	if a == nil || chunk == nil || a.err != nil {
		return
	}
	for _, choice := range chunk.Choices {
		if choice.Index != 0 {
			continue
		}
		if choice.Delta.Content != nil {
			_, _ = a.text.Write([]byte(*choice.Delta.Content))
		}
		for _, call := range choice.Delta.ToolCalls {
			index := 0
			if call.Index != nil {
				index = *call.Index
			}
			if index < 0 || index >= 128 {
				a.err = cline.ErrReasoningContext
				return
			}
			if call.ID != "" {
				a.calls[index] = call.ID
			}
		}
		a.details, a.err = cline.MergeReasoningDetails(a.details, choice.Delta.ReasoningDetails)
	}
}
func (a *clineReasoningAccumulator) seal() (string, error) {
	if a == nil {
		return "", nil
	}
	if a.err != nil {
		return "", ErrClineReasoningUnavailable
	}
	if len(a.details) == 0 {
		return "", nil
	}
	if a.context.codec == nil {
		return "", ErrClineReasoningUnavailable
	}
	indexes := make([]int, 0, len(a.calls))
	for index := range a.calls {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)
	ids := make([]string, 0, len(indexes))
	for _, index := range indexes {
		ids = append(ids, a.calls[index])
	}
	token, err := a.context.codec.Seal(a.details, ids, hex.EncodeToString(a.text.Sum(nil)), time.Now().UTC())
	if err != nil {
		return "", ErrClineReasoningUnavailable
	}
	return token, nil
}
func sealClineBufferedReasoning(c *gin.Context, response *apicompat.ChatCompletionsResponse) (string, error) {
	state := clineReasoningFromContext(c)
	if state == nil || response == nil || len(response.Choices) == 0 {
		return "", nil
	}
	message := response.Choices[0].Message
	details, err := cline.MergeReasoningDetails(nil, message.ReasoningDetails)
	if err != nil {
		return "", ErrClineReasoningUnavailable
	}
	if len(details) == 0 {
		return "", nil
	}
	if state.codec == nil {
		return "", ErrClineReasoningUnavailable
	}
	ids := make([]string, 0, len(message.ToolCalls))
	for _, call := range message.ToolCalls {
		ids = append(ids, call.ID)
	}
	token, err := state.codec.Seal(details, ids, clineTextDigest(clineChatText(message.Content)), time.Now().UTC())
	if err != nil {
		return "", ErrClineReasoningUnavailable
	}
	return token, nil
}
func clineReasoningOutput(token string) apicompat.ResponsesOutput {
	id := sha256.Sum256([]byte(token))
	return apicompat.ResponsesOutput{Type: "reasoning", ID: "crs_" + hex.EncodeToString(id[:12]), Status: "completed", EncryptedContent: token, Summary: []apicompat.ResponsesSummary{}}
}
func attachClineResponsesReasoning(response *apicompat.ResponsesResponse, token string) {
	if response != nil && token != "" && response.Status != "failed" {
		response.Output = append(response.Output, clineReasoningOutput(token))
	}
}

// Add a real item lifecycle before the terminal event and to its output snapshot.
// Never merely decorate response.completed: strict event-driven clients also
// require output_item.added/done. Plaintext summary suppression leaves this item.
func attachClineResponsesReasoningEvents(events []apicompat.ResponsesStreamEvent, token string) []apicompat.ResponsesStreamEvent {
	if token == "" {
		return events
	}
	out := make([]apicompat.ResponsesStreamEvent, 0, len(events)+2)
	for _, event := range events {
		if event.Response != nil && (event.Type == "response.completed" || event.Type == "response.incomplete") {
			item := clineReasoningOutput(token)
			index := len(event.Response.Output)
			seq := event.SequenceNumber
			started := item
			started.Status = "in_progress"
			started.EncryptedContent = ""
			out = append(out, apicompat.ResponsesStreamEvent{Type: "response.output_item.added", SequenceNumber: seq, OutputIndex: index, Item: &started}, apicompat.ResponsesStreamEvent{Type: "response.output_item.done", SequenceNumber: seq + 1, OutputIndex: index, Item: &item})
			attachClineResponsesReasoning(event.Response, token)
			event.SequenceNumber += 2
		}
		out = append(out, event)
	}
	return out
}
func attachClineAnthropicReasoningEvents(events []apicompat.AnthropicStreamEvent, index int, token string) []apicompat.AnthropicStreamEvent {
	if token == "" {
		return events
	}
	out := make([]apicompat.AnthropicStreamEvent, 0, len(events)+2)
	for _, event := range events {
		if event.Type == "message_delta" {
			out = append(out, apicompat.AnthropicStreamEvent{Type: "content_block_start", Index: &index, ContentBlock: &apicompat.AnthropicContentBlock{Type: "redacted_thinking", Data: token}}, apicompat.AnthropicStreamEvent{Type: "content_block_stop", Index: &index})
		}
		out = append(out, event)
	}
	return out
}
