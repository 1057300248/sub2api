//go:build unit

package service

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestClineLegacyCustomParametersStayRequestScoped(t *testing.T) {
	source := []byte(`{"input":"hello","providerOptions":{"gateway":{"only":["deepseek"]}},"vendor":{"zero":0,"disabled":false},"messages":[{"role":"system","content":"override"}],"max_tokens":1}`)
	outbound := []byte(`{"model":"mapped-model","messages":[{"role":"user","content":"hello"}],"stream":false}`)
	for _, platform := range []string{PlatformOpenAI, PlatformDeepseek} {
		a := &Account{Platform: platform, Type: AccountTypeAPIKey, Credentials: map[string]any{"base_url": "https://api.cline.bot/api/v1", "api_key": "fixture-only"}}
		for _, shape := range []any{apicompat.ResponsesRequest{}, apicompat.AnthropicRequest{}} {
			got, err := mergeClineCustomRequestParameters(a, source, outbound, shape)
			require.NoError(t, err)
			assert.Equal(t, "deepseek", gjson.GetBytes(got, "providerOptions.gateway.only.0").String())
			assert.Equal(t, "0", gjson.GetBytes(got, "vendor.zero").Raw)
			assert.Equal(t, "false", gjson.GetBytes(got, "vendor.disabled").Raw)
			assert.Equal(t, "hello", gjson.GetBytes(got, "messages.0.content").String())
			assert.False(t, gjson.GetBytes(got, "max_tokens").Exists())
			assert.Equal(t, "mapped-model", gjson.GetBytes(got, "model").String())
			assert.NotContains(t, a.Credentials, "providerOptions")
			second, err := mergeClineCustomRequestParameters(a, []byte(`{}`), outbound, shape)
			require.NoError(t, err)
			assert.Equal(t, outbound, second)
		}
	}
	for _, base := range []string{"https://api.deepseek.com", "https://api.cline.bot.example.com", "https://api.cline.bot@evil.example", "http://api.cline.bot", "https://api.cline.bot:8443"} {
		a := &Account{Platform: PlatformDeepseek, Type: AccountTypeAPIKey, Credentials: map[string]any{"base_url": base}}
		got, err := mergeClineCustomRequestParameters(a, source, outbound, apicompat.ResponsesRequest{})
		require.NoError(t, err)
		assert.Equal(t, outbound, got, base)
	}
}

func TestClineLegacyThreeProtocolParametersSurviveLowering(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, platform := range []string{PlatformOpenAI, PlatformDeepseek} {
		for _, protocol := range []string{"chat", "responses", "messages"} {
			for _, stream := range []bool{false, true} {
				body, path := clineProtocolRequest(protocol, stream)
				body = append(bytes.TrimSuffix(body, []byte("}")), []byte(`,"providerOptions":{"gateway":{"only":["deepseek"]}},"vendor":{"zero":0,"disabled":false}}`)...)
				original := append([]byte(nil), body...)
				// Reusing the same input for another account must not consume/mutate it.
				for _, id := range []int64{901, 902} {
					a := &Account{ID: id, Platform: platform, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": "fixture-only", "base_url": "https://api.cline.bot/api/v1", "model_mapping": map[string]any{"public-model": "vendor/model"}}}
					payload := `{"id":"fixture","model":"vendor/model","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`
					media := "application/json"
					if stream {
						media = "text/event-stream"
						payload = "data: {\"id\":\"fixture\",\"model\":\"vendor/model\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":1,\"total_tokens\":2}}\n\ndata: [DONE]\n\n"
					}
					upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{media}}, Body: io.NopCloser(strings.NewReader(payload))}}
					svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream}
					c, _ := gin.CreateTestContext(httptest.NewRecorder())
					c.Request = httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
					var result *OpenAIForwardResult
					var err error
					switch protocol {
					case "responses":
						result, err = svc.forwardResponsesViaRawChatCompletions(context.Background(), c, a, body)
					case "messages":
						result, err = svc.forwardAnthropicViaRawChatCompletions(context.Background(), c, a, body, "")
					default:
						result, err = svc.forwardAsRawChatCompletions(context.Background(), c, a, body, "")
					}
					require.NoError(t, err, "%s/%s/stream=%t", platform, protocol, stream)
					require.NotNil(t, result)
					assert.Equal(t, "deepseek", gjson.GetBytes(upstream.lastBody, "providerOptions.gateway.only.0").String())
					assert.Equal(t, "false", gjson.GetBytes(upstream.lastBody, "vendor.disabled").Raw)
					assert.Equal(t, "0", gjson.GetBytes(upstream.lastBody, "vendor.zero").Raw)
					assert.Equal(t, original, body)
				}
			}
		}
	}
}
