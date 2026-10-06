//go:build unit

package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/Wei-Shaw/sub2api/internal/pkg/cline"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

func TestClineFinalStreamModeAndForcedTools(t *testing.T) {
	a := clineRoutingAccount(t, cline.ModePass, cline.AuthAPIKey)
	for _, stream := range []bool{false, true} {
		for _, body := range []string{`{"model":"cline-pass/model","messages":[]}`, fmt.Sprintf(`{"model":"cline-pass/model","stream":%t}`, stream)} {
			got, err := normalizeClineFinalRequest(a, []byte(body), stream)
			require.NoError(t, err)
			require.Equal(t, stream, gjson.GetBytes(got, "stream").Bool())
			require.True(t, gjson.GetBytes(got, "stream").Exists())
		}
	}
	for _, bad := range []string{`{"stream":null}`, `{"stream":"false"}`, `{"stream":false,"Stream":true}`, `{"tool_choice":"required","tools":[]}`, `{"tool_choice":{"type":"function","function":{"name":"missing"}},"tools":[{"type":"function","function":{"name":"exists"}}]}`} {
		_, err := normalizeClineFinalRequest(a, []byte(bad), false)
		require.ErrorIs(t, err, ErrClineRequestContract)
	}
	valid := []byte(`{"tool_choice":"required","tools":[{"type":"function","function":{"name":"f"}}],"providerOptions":{"gateway":{"only":["deepseek"]}}}`)
	got, err := normalizeClineFinalRequest(a, valid, false)
	require.NoError(t, err)
	require.Equal(t, "deepseek", gjson.GetBytes(got, "providerOptions.gateway.only.0").String())
	source := []byte(`{"model":"public-model","input":"hello","tools":[{"type":"web_search"}],"tool_choice":"required"}`)
	var rr apicompat.ResponsesRequest
	require.NoError(t, json.Unmarshal(source, &rr))
	converted, err := apicompat.ResponsesToChatCompletionsRequest(&rr)
	require.NoError(t, err)
	target, err := json.Marshal(converted)
	require.NoError(t, err)
	_, err = mergeClineCustomRequestParameters(a, source, target, apicompat.ResponsesRequest{})
	require.ErrorIs(t, err, ErrClineRequestContract)
	other := &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"base_url": "https://api.openai.com/v1"}}
	got, err = normalizeClineFinalRequest(other, []byte(`{"stream":null}`), false)
	require.NoError(t, err)
	require.Equal(t, `{"stream":null}`, string(got))
}

func TestClineThreeProtocolOmittedStreamAndOfficialHeaders(t *testing.T) {
	for _, protocol := range []string{"chat", "responses", "messages"} {
		t.Run(protocol, func(t *testing.T) {
			a := clineRoutingAccount(t, cline.ModePass, cline.AuthAccountToken)
			a.Credentials["header_override_enabled"] = true
			a.Credentials["header_overrides"] = map[string]any{"HTTP-Referer": "https://app.example.invalid", "X-Title": "saved title"}
			payload := `{"id":"chatcmpl-cline","object":"chat.completion","model":"cline-pass/model","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":7,"completion_tokens":3,"total_tokens":10}}`
			transport := &httpUpstreamRecorder{resp: &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(payload))}}
			gateway := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: transport, accountRepo: &clineAdmissionTestRepository{}}
			body, path := clineProtocolRequest(protocol, false)
			body, err := sjson.DeleteBytes(body, "stream")
			require.NoError(t, err)
			body, err = sjson.SetBytes(body, "header_overrides", map[string]string{"X-Title": "request title"})
			require.NoError(t, err)
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest("POST", path, bytes.NewReader(body))
			result, err := invokeClineProtocol(gateway, c, a, body, protocol)
			require.NoError(t, err)
			require.False(t, result.Stream)
			require.True(t, gjson.GetBytes(transport.lastBody, "stream").Exists())
			require.False(t, gjson.GetBytes(transport.lastBody, "stream").Bool())
			require.False(t, gjson.GetBytes(transport.lastBody, "header_overrides").Exists())
			require.Equal(t, "request title", transport.lastReq.Header.Get("X-Title"))
			require.Equal(t, "https://app.example.invalid", transport.lastReq.Header.Get("HTTP-Referer"))
			require.Equal(t, "Bearer workos:test-key-not-real", transport.lastReq.Header.Get("Authorization"))
		})
	}
}
func TestClineAccountTokenMetadataReauthenticationAndRecovery(t *testing.T) {
	for _, key := range []string{"token-fixture", "workos:token-fixture"} {
		t.Run(key, func(t *testing.T) {
			a, repo, transport, gateway := clineMetadataFixture(t)
			a.Credentials["cline_auth_type"] = cline.AuthAccountToken
			a.Credentials["api_key"] = key
			fp := ClineCredentialFingerprint(a)
			require.NoError(t, NormalizeClineCredentials(a.Platform, a.Type, a.Credentials))
			require.Equal(t, fp, ClineCredentialFingerprint(a))
			transport.statuses = map[string]int{cline.ProfileURL: 401}
			view, err := gateway.RefreshClineMetadata(context.Background(), a)
			require.NoError(t, err)
			require.Equal(t, "reauth_required", view.CredentialStatus)
			require.Equal(t, StatusActive, a.Status)
			for _, req := range transport.requests {
				if req.URL.String() == cline.ProfileURL {
					require.Equal(t, "Bearer workos:token-fixture", req.Header.Get("Authorization"))
				} else {
					require.Empty(t, req.Header.Get("Authorization"))
				}
			}
			a.Extra[ClineStateExtraKey] = repo.stored
			require.ErrorIs(t, ClineQuotaAdmission(a, time.Now()), ErrClineMetadataRequired)
			transport.statuses = nil
			transport.requests = nil
			view, err = gateway.RefreshClineMetadata(context.Background(), a)
			require.NoError(t, err)
			require.Equal(t, "valid", view.CredentialStatus)
			a.Extra[ClineStateExtraKey] = repo.stored
			require.NoError(t, ClineQuotaAdmission(a, time.Now()))
			transport.statuses = map[string]int{cline.UsageURL: 401}
			view, err = gateway.RefreshClineMetadata(context.Background(), a)
			require.NoError(t, err)
			require.Equal(t, "reauth_required", view.CredentialStatus)
		})
	}
}

type clineNextAuthRepo struct {
	AccountRepository
	marked    bool
	accountID int64
}

func (r *clineNextAuthRepo) MarkClineReauthenticationRequired(_ context.Context, a *Account, _ time.Time) (bool, error) {
	r.marked = true
	r.accountID = a.ID
	return true, nil
}
func TestClineAccountToken401UsesCredentialBoundReauth(t *testing.T) {
	a := clineRoutingAccount(t, cline.ModePass, cline.AuthAccountToken)
	a.ID = 27
	repo := &clineNextAuthRepo{}
	s := &RateLimitService{accountRepo: repo}
	require.True(t, s.handleClineScopedUpstreamError(context.Background(), a, 401, nil, []byte(`{"error":{"message":"unauthorized"}}`), "cline-pass/model"))
	require.True(t, repo.marked)
	require.EqualValues(t, 27, repo.accountID)
	require.Equal(t, StatusActive, a.Status)
	a.Credentials["cline_auth_type"] = cline.AuthAPIKey
	require.False(t, s.handleClineReauthentication(context.Background(), a))
}

func TestClineReasoningThreeProtocolRoundTripAndCallerIsolation(t *testing.T) {
	for _, protocol := range []string{"responses", "messages"} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream%t", protocol, stream), func(t *testing.T) {
				a := clineRoutingAccount(t, cline.ModePass, cline.AuthAPIKey)
				details := `[{"index":0,"type":"reasoning.encrypted","data":"provider-opaque","signature":"provider-signature"}]`
				payload := `{"id":"chatcmpl-r","object":"chat.completion","model":"cline-pass/model","choices":[{"index":0,"message":{"role":"assistant","content":"answer","reasoning_details":` + details + `},"finish_reason":"stop"}],"usage":{"prompt_tokens":7,"completion_tokens":3,"total_tokens":10}}`
				media := "application/json"
				if stream {
					media = "text/event-stream"
					payload = "data: {\"id\":\"chatcmpl-r\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"answer\",\"reasoning_details\":" + details + "}}]}\n\ndata: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":7,\"completion_tokens\":3,\"total_tokens\":10}}\n\ndata: [DONE]\n\n"
				}
				transport := &httpUpstreamRecorder{resp: &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{media}}, Body: io.NopCloser(strings.NewReader(payload))}}
				cfg := rawChatCompletionsTestConfig()
				cfg.JWT.Secret = strings.Repeat("fixture-secret-", 3)
				svc := &OpenAIGatewayService{cfg: cfg, httpUpstream: transport, accountRepo: &clineAdmissionTestRepository{}}
				body, path := clineProtocolRequest(protocol, stream)
				rec := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(rec)
				c.Request = httptest.NewRequest("POST", path, bytes.NewReader(body))
				c.Set("api_key", &APIKey{ID: 31, UserID: 41})
				result, err := invokeClineProtocol(svc, c, a, body, protocol)
				require.NoError(t, err)
				require.Equal(t, 7, result.Usage.InputTokens)
				var token string
				if !stream {
					if protocol == "responses" {
						for _, item := range gjson.Get(rec.Body.String(), "output").Array() {
							if item.Get("type").String() == "reasoning" {
								token = item.Get("encrypted_content").String()
							}
						}
					} else {
						for _, item := range gjson.Get(rec.Body.String(), "content").Array() {
							if item.Get("type").String() == "redacted_thinking" {
								token = item.Get("data").String()
							}
						}
					}
				}
				if stream {
					for _, line := range strings.Split(rec.Body.String(), "\n") {
						if !strings.HasPrefix(line, "data:") {
							continue
						}
						event := gjson.Parse(strings.TrimSpace(strings.TrimPrefix(line, "data:")))
						if protocol == "responses" && event.Get("type").String() == "response.output_item.done" && event.Get("item.type").String() == "reasoning" {
							if value := event.Get("item.encrypted_content").String(); value != "" {
								token = value
							}
						}
						if protocol == "messages" && event.Get("content_block.type").String() == "redacted_thinking" {
							token = event.Get("content_block.data").String()
						}
					}
				}
				require.True(t, strings.HasPrefix(token, cline.ReasoningPrefix), rec.Body.String())
				require.NotContains(t, token, "provider-opaque")
				var source []byte
				if protocol == "responses" {
					source, _ = json.Marshal(map[string]any{"input": []any{map[string]any{"type": "reasoning", "encrypted_content": token}}})
				} else {
					source, _ = json.Marshal(map[string]any{"messages": []any{map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "redacted_thinking", "data": token}}}}})
				}
				lowered := []byte(`{"model":"cline-pass/model","messages":[{"role":"user","content":"hello"},{"role":"assistant","content":"answer"},{"role":"user","content":"continue"}]}`)
				restored, err := svc.restoreClineReasoning(c, a, source, lowered, protocol)
				require.NoError(t, err)
				require.JSONEq(t, details, gjson.GetBytes(restored, "messages.1.reasoning_details").Raw)
				c.Set("api_key", &APIKey{ID: 32, UserID: 41})
				_, err = svc.restoreClineReasoning(c, a, source, lowered, protocol)
				require.ErrorIs(t, err, ErrClineRequestContract)
			})
		}
	}
}
func TestClineReasoningToolBindingAndTerminalEvents(t *testing.T) {
	a := clineRoutingAccount(t, cline.ModePass, cline.AuthAPIKey)
	cfg := rawChatCompletionsTestConfig()
	cfg.JWT.Secret = strings.Repeat("x", 32)
	svc := &OpenAIGatewayService{cfg: cfg}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Set("api_key", &APIKey{ID: 1, UserID: 2})
	state := svc.configureClineReasoning(c, a, "cline-pass/model")
	token, err := state.codec.Seal([]byte(`[{"type":"reasoning.encrypted","data":"opaque"}]`), []string{"call1"}, clineTextDigest(""), time.Now())
	require.NoError(t, err)
	source, _ := json.Marshal(map[string]any{"input": []any{map[string]any{"type": "reasoning", "encrypted_content": token}}})
	body := []byte(`{"model":"cline-pass/model","messages":[{"role":"assistant","tool_calls":[{"id":"call1","type":"function","function":{"name":"f","arguments":"{}"}}]}]}`)
	restored, err := svc.restoreClineReasoning(c, a, source, body, "responses")
	require.NoError(t, err)
	require.True(t, gjson.GetBytes(restored, "messages.0.reasoning_details").Exists())
	bad, _ := sjson.SetBytes(body, "messages.0.tool_calls.0.id", "other")
	_, err = svc.restoreClineReasoning(c, a, source, bad, "responses")
	require.Error(t, err)
	events := attachClineResponsesReasoningEvents([]apicompat.ResponsesStreamEvent{{Type: "response.completed", SequenceNumber: 4, Response: &apicompat.ResponsesResponse{Status: "completed"}}}, token)
	require.Len(t, events, 3)
	require.Equal(t, "response.output_item.added", events[0].Type)
	require.Equal(t, "response.output_item.done", events[1].Type)
	require.Equal(t, 6, events[2].SequenceNumber)
	require.Equal(t, token, events[2].Response.Output[0].EncryptedContent)
	failed := attachClineResponsesReasoningEvents([]apicompat.ResponsesStreamEvent{{Type: "response.failed", SequenceNumber: 4, Response: &apicompat.ResponsesResponse{Status: "failed"}}}, token)
	require.Len(t, failed, 1)
	require.Empty(t, failed[0].Response.Output)
}
