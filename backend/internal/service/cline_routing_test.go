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
	"slices"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/cline"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai_compat"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func clineRoutingAccount(t *testing.T, mode, auth string) *Account {
	t.Helper()
	a := clineTestAccount(mode)
	a.Name, a.Status, a.Schedulable, a.Concurrency = "Cline fixture", StatusActive, true, 1
	a.Credentials["cline_auth_type"] = auth
	if mode == cline.ModePayG {
		a.Credentials["model_mapping"] = map[string]any{"public-model": "vendor/model"}
	}
	require.NoError(t, NormalizeClineCredentials(a.Platform, a.Type, a.Credentials))
	// Native-protocol probe data must never override the Cline transport contract.
	a.Extra = map[string]any{
		openai_compat.ExtraKeyResponsesMode: "force_responses",
		"openai_responses_supported":        true,
		"openai_passthrough":                true,
	}
	return a
}

func TestClineRoutingRegistrationAndCapabilities(t *testing.T) {
	a := clineRoutingAccount(t, cline.ModePass, cline.AuthAPIKey)
	require.Equal(t, PlatformCline, NormalizeOpenAICompatiblePlatform(PlatformCline))
	require.True(t, isConcreteRequestPlatform(PlatformCline))
	require.Contains(t, matchingPlatforms(PlatformComposite), PlatformCline)
	require.Contains(t, schedulerSnapshotPlatforms(), PlatformCline)
	require.False(t, a.IsCNProvider())
	require.False(t, IsMultiProtocolAPIKeyProvider(PlatformCline))
	require.False(t, a.IsOpenAIPassthroughEnabled())
	require.False(t, a.IsHeaderOverrideEligible())
	require.True(t, shouldForwardOpenAIResponsesViaRawChatCompletions(a))
	require.Empty(t, (&Group{Platform: PlatformCline}).ResolveMessagesDispatchModel("claude-sonnet-4-5"))
	require.Empty(t, defaultModelsListCandidateIDs(PlatformCline))
	require.Equal(t, []string{"public-model"}, a.ClineModelIDs())
	for _, capability := range []OpenAIEndpointCapability{"", OpenAIEndpointCapabilityChatCompletions} {
		require.True(t, a.SupportsOpenAIEndpointCapability(capability))
	}
	for _, capability := range []OpenAIEndpointCapability{OpenAIEndpointCapabilityResponses, OpenAIEndpointCapabilityResponsesCompact, OpenAIEndpointCapabilityEmbeddings, OpenAIEndpointCapabilityAlphaSearch, OpenAIEndpointCapabilityLive, OpenAIEndpointCapabilityGrokMediaGeneration, OpenAIEndpointCapabilitySeedance} {
		require.False(t, a.SupportsOpenAIEndpointCapability(capability), string(capability))
	}
	a.Type = AccountTypeOAuth
	require.False(t, a.SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilityChatCompletions))
	require.False(t, shouldForwardOpenAIResponsesViaRawChatCompletions(a))
	for _, groupID := range []int64{0, 19} {
		count := 0
		for _, bucket := range schedulerCanonicalBuckets(groupID) {
			if bucket.Platform == PlatformCline {
				count++
				require.Contains(t, []string{SchedulerModeSingle, SchedulerModeForced}, bucket.Mode)
			}
		}
		require.Equal(t, 2, count)
	}
}

func invokeClineProtocol(s *OpenAIGatewayService, c *gin.Context, a *Account, body []byte, protocol string) (*OpenAIForwardResult, error) {
	switch protocol {
	case "responses":
		return s.Forward(context.Background(), c, a, body)
	case "messages":
		return s.ForwardAsAnthropic(context.Background(), c, a, body, "", "")
	default:
		return s.ForwardAsChatCompletions(context.Background(), c, a, body, "", "")
	}
}

func clineProtocolRequest(protocol string, stream bool) ([]byte, string) {
	if protocol == "responses" {
		return []byte(fmt.Sprintf(`{"model":"public-model","input":"hello","stream":%t}`, stream)), "/v1/responses"
	}
	path := "/v1/chat/completions"
	if protocol == "messages" {
		path = "/v1/messages"
	}
	return []byte(fmt.Sprintf(`{"model":"public-model","messages":[{"role":"user","content":"hello"}],"max_tokens":32,"stream":%t}`, stream)), path
}

func TestClineThreeProtocolHTTPForwarding(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, protocol := range []string{"chat", "responses", "messages"} {
		for _, mode := range []string{cline.ModePass, cline.ModePayG} {
			for _, auth := range []string{cline.AuthAPIKey, cline.AuthAccountToken} {
				for _, stream := range []bool{false, true} {
					t.Run(fmt.Sprintf("%s/%s/%s/stream=%t", protocol, mode, auth, stream), func(t *testing.T) {
						a := clineRoutingAccount(t, mode, auth)
						model := a.GetMappedModel("public-model")
						payload := fmt.Sprintf(`{"id":"chatcmpl-cline","object":"chat.completion","model":%q,"choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":7,"completion_tokens":3,"total_tokens":10}}`, model)
						media := "application/json"
						if stream {
							media = "text/event-stream"
							payload = fmt.Sprintf("data: {\"id\":\"chatcmpl-cline\",\"object\":\"chat.completion.chunk\",\"model\":%q,\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"}}]}\n\ndata: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":7,\"completion_tokens\":3,\"total_tokens\":10}}\n\ndata: [DONE]\n\n", model)
						}
						upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{media}}, Body: io.NopCloser(strings.NewReader(payload))}}
						svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream}
						body, path := clineProtocolRequest(protocol, stream)
						rec := httptest.NewRecorder()
						c, _ := gin.CreateTestContext(rec)
						c.Request = httptest.NewRequest("POST", path, bytes.NewReader(body))
						result, err := invokeClineProtocol(svc, c, a, body, protocol)
						require.NoError(t, err)
						require.NotNil(t, result)
						require.NotNil(t, upstream.lastReq)
						require.Equal(t, cline.BaseURL+"/chat/completions", upstream.lastReq.URL.String())
						require.Equal(t, "Bearer test-key-not-real", upstream.lastReq.Header.Get("Authorization"))
						require.Equal(t, model, gjson.GetBytes(upstream.lastBody, "model").String())
						require.True(t, gjson.GetBytes(upstream.lastBody, "messages").IsArray())
						require.False(t, gjson.GetBytes(upstream.lastBody, "input").Exists())
						require.Equal(t, "/v1/chat/completions", GetActualOpenAIUpstreamEndpoint(c))
						require.Equal(t, 7, result.Usage.InputTokens)
						require.Equal(t, 3, result.Usage.OutputTokens)
						require.Contains(t, rec.Body.String(), "ok")
						require.Equal(t, stream, result.Stream)
					})
				}
			}
		}
	}
}

func TestClineThreeProtocolFailuresNeverFinalizeSuccessfully(t *testing.T) {
	gin.SetMode(gin.TestMode)
	quota := `{"error":{"code":429,"message":"Weekly ClinePass limit. Try again in 2h"}}`
	for _, protocol := range []string{"chat", "responses", "messages"} {
		for _, scenario := range []string{"json_error", "stream_json_error", "stream_quota", "partial_quota", "truncated"} {
			t.Run(protocol+"/"+scenario, func(t *testing.T) {
				streaming := scenario != "json_error"
				body, path := clineProtocolRequest(protocol, streaming)
				media, payload := "application/json", quota
				if scenario == "stream_quota" || scenario == "partial_quota" || scenario == "truncated" {
					media = "text/event-stream"
					payload = "data: " + quota + "\n\ndata: [DONE]\n\n"
					if scenario != "stream_quota" {
						partial := "data: {\"id\":\"chatcmpl-cline\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"partial\"}}]}\n\n"
						if scenario == "truncated" {
							payload = partial
						} else {
							payload = partial + payload
						}
					}
				}
				repo := &clineGuardLimitRepository{}
				upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{media}}, Body: io.NopCloser(strings.NewReader(payload))}}
				svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream, rateLimitService: &RateLimitService{accountRepo: repo}}
				rec := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(rec)
				c.Request = httptest.NewRequest("POST", path, bytes.NewReader(body))
				_, err := invokeClineProtocol(svc, c, clineRoutingAccount(t, cline.ModePass, cline.AuthAPIKey), body, protocol)
				require.Error(t, err)
				for _, terminal := range []string{"[DONE]", "response.completed", "message_stop"} {
					require.NotContains(t, rec.Body.String(), terminal)
				}
				if scenario == "truncated" {
					require.Empty(t, repo.observations)
				} else {
					require.Len(t, repo.observations, 1)
					require.Equal(t, cline.ScopePass+"weekly", repo.observations[0].scope)
				}
				if strings.HasPrefix(scenario, "partial") {
					require.Contains(t, rec.Body.String(), "partial")
				}
			})
		}
	}
}

func TestClineThreeProtocolRejectsUnentitledModelsBeforeNetwork(t *testing.T) {
	for _, mode := range []string{cline.ModeFree, cline.ModeUnknown} {
		for _, protocol := range []string{"chat", "responses", "messages"} {
			t.Run(mode+"/"+protocol, func(t *testing.T) {
				a := clineRoutingAccount(t, mode, cline.AuthAPIKey)
				upstream := &httpUpstreamRecorder{}
				svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream}
				body, path := clineProtocolRequest(protocol, false)
				c, _ := gin.CreateTestContext(httptest.NewRecorder())
				c.Request = httptest.NewRequest("POST", path, bytes.NewReader(body))
				_, err := invokeClineProtocol(svc, c, a, body, protocol)
				require.Error(t, err)
				require.Nil(t, upstream.lastReq)
				require.Empty(t, a.ClineModelIDs())
			})
		}
	}
}

func TestClineTokenCountNeverCallsNativeUpstream(t *testing.T) {
	upstream := &httpUpstreamRecorder{}
	svc := &OpenAIGatewayService{httpUpstream: upstream}
	body := []byte(`{"model":"public-model","messages":[{"role":"user","content":"hello"}]}`)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest("POST", "/v1/messages/count_tokens", bytes.NewReader(body))
	require.NoError(t, svc.ForwardCountTokensAsAnthropic(context.Background(), c, clineRoutingAccount(t, cline.ModePass, cline.AuthAPIKey), body, ""))
	require.Nil(t, upstream.lastReq)
	var response struct {
		InputTokens int `json:"input_tokens"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &response))
	require.Positive(t, response.InputTokens)
	require.True(t, slices.Contains(AllowedQuotaPlatforms, PlatformCline))
}
