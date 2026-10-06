//go:build unit

package service

import (
	"bytes"
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
	"github.com/tidwall/sjson"
)

func TestClineHeaderOverrideValidation(t *testing.T) {
	a := clineRoutingAccount(t, cline.ModePass, cline.AuthAPIKey)
	for _, body := range []string{
		`{"header_overrides":null}`, `{"header_overrides":[]}`, `{"header_overrides":{"User-Agent":null}}`,
		`{"header_overrides":{"User-Agent":5}}`, `{"header_overrides":{"User-Agent":["a"]}}`,
		`{"header_overrides":{"User-Agent":"a","user-agent":"b"}}`,
		`{"header_overrides":{"User-Agent":"a","User-\u0041gent":"b"}}`,
		`{"header_overrides":{},"header_overrides":{}}`, `{"HEADER_OVERRIDES":{}}`,
		`{"header_overrides":{"User-Agent":"x\r\nInjected: 1"}}`,
		`{"header_overrides":{"X-Metadata-Test":"\u0000"}}`,
		`{"header_overrides":{"X-Metadata-Test:":"value"}}`,
		`{"header_overrides":{"X-Metadata-Test ":"value"}}`,
		`{"header_overrides":{"X-Metadata-Test":"` + strings.Repeat("a", 2049) + `"}}`,
	} {
		_, _, err := extractClineHeaderOverrides(a, []byte(body))
		require.ErrorIs(t, err, ErrClineHeaderOverrides, body[:min(90, len(body))])
	}
	for _, name := range []string{"Authorization", "authorization", "Host", "Cookie", "X-Api-Key", "Proxy-Authorization", "Content-Length", "Content-Type", "Accept", "Transfer-Encoding", "Connection", "Upgrade", "TE", "Trailer", "Forwarded", "X-Forwarded-For", "X-Real-IP", "OpenAI-Organization", "X-Cline-Account-Id", "X-Unknown-Tenant"} {
		_, _, err := extractClineHeaderOverrides(a, []byte(fmt.Sprintf(`{"header_overrides":{%q:"fixture"}}`, name)))
		require.ErrorIs(t, err, ErrClineHeaderOverrides, name)
	}
	_, _, err := extractClineHeaderOverrides(a, []byte(`{"header_overrides":{`+strings.TrimSuffix(strings.Repeat(`"X-Metadata-A":"x",`, 17), ",")+`}}`))
	require.ErrorIs(t, err, ErrClineHeaderOverrides)
}

func TestClineHeaderOverridesRemainRequestScopedAndPreserveVendorPrecision(t *testing.T) {
	a := clineRoutingAccount(t, cline.ModePass, cline.AuthAPIKey)
	body := []byte(`{"model":"cline-pass/model","providerOptions":{"gateway":{"only":["deepseek"]}},"integer":9007199254740993,"header_overrides":{"uSeR-aGeNt":"client/2","X-Request-ID":"r1","X-Metadata-Trace":"fixture"}}`)
	clean, headers, err := extractClineHeaderOverrides(a, body)
	require.NoError(t, err)
	require.Equal(t, "client/2", headers.Get("User-Agent"))
	require.Equal(t, "9007199254740993", gjson.GetBytes(clean, "integer").Raw)
	require.Equal(t, "deepseek", gjson.GetBytes(clean, "providerOptions.gateway.only.0").String())
	require.False(t, gjson.GetBytes(clean, "header_overrides").Exists())
	require.NotContains(t, a.Credentials, "header_overrides")
	_, again, err := extractClineHeaderOverrides(a, []byte(`{"model":"cline-pass/model"}`))
	require.NoError(t, err)
	require.Nil(t, again)
	// Unrelated providers retain their own existing request contract.
	a.Platform, a.Credentials["base_url"] = PlatformDeepseek, "https://api.deepseek.com"
	unchanged, overrides, err := extractClineHeaderOverrides(a, body)
	require.NoError(t, err)
	require.Equal(t, body, unchanged)
	require.Nil(t, overrides)
}

func TestClineHeaderOverridesValidateOriginalBeforeProtocolLowering(t *testing.T) {
	a := clineRoutingAccount(t, cline.ModePass, cline.AuthAPIKey)
	for _, shape := range []any{apicompat.ResponsesRequest{}, apicompat.AnthropicRequest{}} {
		_, err := mergeClineCustomRequestParameters(a, []byte(`{"header_overrides":{},"header_overrides":{"User-Agent":"x"}}`), []byte(`{"model":"cline-pass/model"}`), shape)
		require.ErrorIs(t, err, ErrClineHeaderOverrides)
	}
}

func TestClineThreeProtocolRequestHeaderOverrides(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, platform := range []string{PlatformCline, PlatformOpenAI, PlatformDeepseek} {
		for _, protocol := range []string{"chat", "responses", "messages"} {
			for _, stream := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/stream=%v", platform, protocol, stream), func(t *testing.T) {
					a := clineRoutingAccount(t, cline.ModePass, cline.AuthAPIKey)
					a.Platform = platform
					if platform != PlatformCline {
						a.Extra["openai_responses_mode"] = "force_chat_completions"
					}
					payload, media := `{"id":"fixture","model":"cline-pass/model","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}}`, "application/json"
					if stream {
						payload, media = "data: {\"id\":\"fixture\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"}}]}\n\ndata: {\"choices\":[],\"usage\":{\"prompt_tokens\":3,\"completion_tokens\":2,\"total_tokens\":5}}\n\ndata: [DONE]\n\n", "text/event-stream"
					}
					upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{media}}, Body: io.NopCloser(strings.NewReader(payload))}}
					svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream, accountRepo: &clineAdmissionTestRepository{}}
					body, path := clineProtocolRequest(protocol, stream)
					body, err := sjson.SetBytes(body, "header_overrides", map[string]string{"User-Agent": "cline-fixture/2", "X-Request-ID": "request-fixture"})
					require.NoError(t, err)
					c, _ := gin.CreateTestContext(httptest.NewRecorder())
					c.Request = httptest.NewRequest("POST", path, bytes.NewReader(body))
					_, err = invokeClineProtocol(svc, c, a, body, protocol)
					require.NoError(t, err)
					require.Equal(t, "cline-fixture/2", upstream.lastReq.Header.Get("User-Agent"))
					require.Equal(t, "request-fixture", upstream.lastReq.Header.Get("X-Request-ID"))
					require.Equal(t, "Bearer test-key-not-real", upstream.lastReq.Header.Get("Authorization"))
					require.False(t, gjson.GetBytes(upstream.lastBody, "header_overrides").Exists())
				})
			}
		}
	}
}
