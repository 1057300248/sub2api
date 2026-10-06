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

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/Wei-Shaw/sub2api/internal/pkg/cline"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestClineOAuthFormattingMetadataAndRotation(t *testing.T) {
	for _, key := range []string{"oauth.fixture", "workos:oauth.fixture"} {
		for _, code := range []int{200, 401} {
			a, repo, transport, gateway := clineMetadataFixture(t)
			a.Credentials["cline_auth_type"] = cline.AuthAccountToken
			a.Credentials["api_key"] = key
			fingerprint := ClineCredentialFingerprint(a)
			transport.statuses = map[string]int{cline.ProfileURL: code}
			view, err := gateway.RefreshClineMetadata(context.Background(), a)
			require.NoError(t, err)
			wantStatus := "valid"
			if code == 401 {
				wantStatus = "invalid"
			}
			require.Equal(t, wantStatus, view.CredentialStatus)
			for _, req := range transport.requests {
				if req.URL.String() == cline.ProfileURL || req.URL.String() == cline.UsageURL {
					require.Equal(t, "Bearer workos:oauth.fixture", req.Header.Get("Authorization"))
				} else {
					require.Empty(t, req.Header.Get("Authorization"))
				}
			}
			require.Equal(t, key, a.GetCredential("api_key"))
			require.Equal(t, key, repo.captured.GetCredential("api_key"))
			require.Equal(t, fingerprint, repo.stored.CredentialFingerprint)
			a.Extra[ClineStateExtraKey] = repo.stored
			a.Credentials["api_key"] = "rotated.fixture"
			require.Nil(t, a.GetClineState(), "old credential evidence must not survive rotation")
			encoded, err := json.Marshal(view)
			require.NoError(t, err)
			require.NotContains(t, string(encoded), "oauth.fixture")
		}
	}
}

func TestClineExplicitStreamContract(t *testing.T) {
	a := clineRoutingAccount(t, cline.ModePass, cline.AuthAccountToken)
	for _, stream := range []bool{true, false} {
		body, key, err := clineOutboundContract(a, []byte(`{"model":"cline-pass/model","stream":true,"stream_options":{"include_usage":true},"providerOptions":{"gateway":{"only":["deepseek"]}},"big":9007199254740993}`), stream, "ignored")
		require.NoError(t, err)
		require.True(t, gjson.GetBytes(body, "stream").Exists())
		require.Equal(t, stream, gjson.GetBytes(body, "stream").Bool())
		require.Equal(t, "9007199254740993", gjson.GetBytes(body, "big").Raw)
		require.Equal(t, "deepseek", gjson.GetBytes(body, "providerOptions.gateway.only.0").String())
		require.Equal(t, "workos:test-key-not-real", key)
		if !stream {
			require.False(t, gjson.GetBytes(body, "stream_options").Exists())
		}
	}
	a.Platform = PlatformOpenAI
	a.Credentials["base_url"] = "https://api.openai.com/v1"
	raw := []byte(`{"model":"not-cline"}`)
	body, key, err := clineOutboundContract(a, raw, false, "unchanged")
	require.NoError(t, err)
	require.Equal(t, raw, body)
	require.Equal(t, "unchanged", key)
}

func TestClineForcedUnsupportedToolNeverCallsUpstream(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, protocol := range []string{"responses", "messages"} {
		for _, choice := range []string{`{"type":"web_search_preview"}`, `"required"`} {
			body := []byte(`{"model":"public-model","input":"hello","tools":[{"type":"web_search_preview"}],"tool_choice":` + choice + `}`)
			path := "/v1/responses"
			if protocol == "messages" {
				path = "/v1/messages"
				body = []byte(`{"model":"public-model","max_tokens":32,"messages":[{"role":"user","content":"hello"}],"tools":[{"type":"web_search_20250305","name":"web_search"}],"tool_choice":{"type":"tool","name":"web_search"}}`)
			}
			a := clineRoutingAccount(t, cline.ModePass, cline.AuthAPIKey)
			upstream := &httpUpstreamRecorder{}
			svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream, accountRepo: &clineAdmissionTestRepository{}}
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest("POST", path, bytes.NewReader(body))
			_, err := invokeClineProtocol(svc, c, a, body, protocol)
			require.Error(t, err)
			require.Equal(t, http.StatusBadRequest, rec.Code)
			require.Nil(t, upstream.lastReq, "unsupported forced tool must fail before network/admission")
		}
	}
}

func TestClineLoweredChoicePreservesDeclaredFunction(t *testing.T) {
	a := clineRoutingAccount(t, cline.ModePass, cline.AuthAPIKey)
	chat := &apicompat.ChatCompletionsRequest{Tools: []apicompat.ChatTool{{Type: "function", Function: &apicompat.ChatFunction{Name: "lookup"}}}, ToolChoice: json.RawMessage(`{"type":"function","function":{"name":"lookup"}}`)}
	require.NoError(t, validateClineLoweredToolChoice(a, json.RawMessage(`{"type":"function","name":"lookup"}`), chat))
	require.NoError(t, validateClineLoweredToolChoice(a, json.RawMessage(`{"type":"tool","name":"lookup"}`), chat))
	require.Error(t, validateClineLoweredToolChoice(a, json.RawMessage(`{"type":"file_search"}`), chat))
	require.NoError(t, validateClineLoweredToolChoice(a, json.RawMessage(`"auto"`), nil))
	require.NoError(t, validateClineLoweredToolChoice(a, json.RawMessage(`"none"`), nil))
}

func TestClineAttributionHeadersRemainBoundedAndProtected(t *testing.T) {
	a := clineRoutingAccount(t, cline.ModePass, cline.AuthAPIKey)
	clean, h, err := extractClineHeaderOverrides(a, []byte(`{"model":"cline-pass/model","header_overrides":{"HTTP-Referer":"https://gateway.example.invalid","X-Title":"My Gateway"}}`))
	require.NoError(t, err)
	require.Equal(t, "My Gateway", h.Get("X-Title"))
	require.False(t, gjson.GetBytes(clean, "header_overrides").Exists())
	for _, raw := range []string{`{"X-Title":"one","x-title":"two"}`, `{"HTTP-Referer":"x\r\nInjected: y"}`, `{"Authorization":"Bearer stolen"}`, `{"X-Cline-Account-Id":"other"}`} {
		_, _, err = extractClineHeaderOverrides(a, []byte(`{"header_overrides":`+raw+`}`))
		require.Error(t, err)
	}
}

func TestClineThreeProtocolReasoningAndProviderEvidence(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, protocol := range []string{"chat", "responses", "messages"} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%t", protocol, stream), func(t *testing.T) {
				payload := `{"id":"fixture","model":"cline-pass/model","provider":"DeepSeek","choices":[{"index":0,"message":{"role":"assistant","content":"answer","reasoning_details":[{"type":"reasoning.encrypted","index":0,"data":"opaque+/=","signature":"fixture-signature","counter":9007199254740993}]},"finish_reason":"stop"}],"usage":{"prompt_tokens":7,"completion_tokens":3,"total_tokens":10}}`
				media := "application/json"
				if stream {
					media = "text/event-stream"
					payload = "data: " + `{"id":"fixture","model":"cline-pass/model","provider":"DeepSeek","choices":[{"index":0,"delta":{"content":"answer"}}]}` + "\n\ndata: " + `{"choices":[{"index":0,"delta":{"reasoning_details":[{"type":"reasoning.encrypted","index":0,"data":"opaque+/=","signature":"fixture-signature","counter":9007199254740993}]},"finish_reason":"stop"}],"usage":{"prompt_tokens":7,"completion_tokens":3,"total_tokens":10}}` + "\n\ndata: [DONE]\n\n"
				}
				upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{media}}, Body: io.NopCloser(strings.NewReader(payload))}}
				svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream, accountRepo: &clineAdmissionTestRepository{}}
				body, path := clineProtocolRequest(protocol, stream)
				rec := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(rec)
				c.Request = httptest.NewRequest("POST", path, bytes.NewReader(body))
				result, err := invokeClineProtocol(svc, c, clineRoutingAccount(t, cline.ModePass, cline.AuthAPIKey), body, protocol)
				require.NoError(t, err)
				require.Equal(t, 7, result.Usage.InputTokens)
				require.Equal(t, 3, result.Usage.OutputTokens)
				for _, value := range []string{"reasoning_details", "opaque+/=", "fixture-signature", "9007199254740993", "DeepSeek"} {
					require.Contains(t, rec.Body.String(), value)
				}
				if protocol != "chat" {
					require.Contains(t, rec.Body.String(), "upstream_response")
				}
			})
		}
	}
}

func TestClineMetadataCapabilityFailureDoesNotInvalidateCredentials(t *testing.T) {
	for _, code := range []int{404, 500} {
		a, _, transport, gateway := clineMetadataFixture(t)
		transport.statuses = map[string]int{cline.ModelCatalogURL: code}
		view, err := gateway.RefreshClineMetadata(context.Background(), a)
		require.NoError(t, err)
		require.Equal(t, "valid", view.CredentialStatus)
		require.NotNil(t, view.Catalog)
		require.Equal(t, "unavailable", view.Catalog.CapabilitiesStatus)
		for _, req := range transport.requests {
			if req.URL.String() == cline.ModelCatalogURL {
				require.Empty(t, req.Header.Get("Authorization"))
				require.Empty(t, req.Header.Get("X-Title"))
				require.Empty(t, req.Header.Get("HTTP-Referer"))
			}
		}
	}
}
