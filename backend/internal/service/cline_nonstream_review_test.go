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

	"github.com/Wei-Shaw/sub2api/internal/pkg/cline"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestClineNonstreamEnvelopeProtocolsAndBilling(t *testing.T) {
	for _, protocol := range []string{"chat", "responses", "messages"} {
		for _, wrapped := range []bool{false, true} {
			for _, source := range []string{BillingModelSourceRequested, BillingModelSourceUpstream} {
				t.Run(fmt.Sprintf("%s/wrapped=%v/%s", protocol, wrapped, source), func(t *testing.T) {
					a := clineRoutingAccount(t, cline.ModePass, cline.AuthAPIKey)
					a.Credentials["model_mapping"] = map[string]any{"gpt-5.1": "cline-pass/model"}
					payload := `{"id":"review-completion","object":"chat.completion","created":1,"model":"cline-pass/model","choices":[{"index":0,"message":{"role":"assistant","content":"review answer"},"finish_reason":"stop"}],"usage":{"prompt_tokens":8,"completion_tokens":4,"total_tokens":12}}`
					if wrapped {
						payload = `{"success":true,"data":` + payload + `}`
					}
					upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}, "X-Request-Id": []string{"review-nonstream"}}, Body: io.NopCloser(strings.NewReader(payload))}}
					gateway := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream, accountRepo: &clineAdmissionTestRepository{}}
					body, path := clineProtocolRequest(protocol, false)
					body = bytes.ReplaceAll(body, []byte("public-model"), []byte("gpt-5.1"))
					rec := httptest.NewRecorder()
					c, _ := gin.CreateTestContext(rec)
					c.Request = httptest.NewRequest("POST", path, bytes.NewReader(body))
					result, err := invokeClineProtocol(gateway, c, a, body, protocol)
					require.NoError(t, err)
					require.NotNil(t, result)
					require.Equal(t, 200, rec.Code)
					require.False(t, result.Stream)
					require.Equal(t, 8, result.Usage.InputTokens)
					require.Equal(t, 4, result.Usage.OutputTokens)
					require.Equal(t, "gpt-5.1", result.Model)
					require.Equal(t, "cline-pass/model", result.BillingModel)
					var response map[string]any
					require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &response))
					require.NotContains(t, response, "success")
					require.NotContains(t, response, "data")
					require.Contains(t, rec.Body.String(), "review answer")
					key := "choices"
					if protocol == "responses" {
						key = "output"
					}
					if protocol == "messages" {
						key = "content"
					}
					require.NotEmpty(t, response[key])
					logRepo := &openAIRecordUsageLogRepoStub{inserted: true}
					ledger := &openAIRecordUsageBillingRepoStub{result: &UsageBillingApplyResult{Applied: true}}
					users := &openAIRecordUsageUserRepoStub{}
					subs := &openAIRecordUsageSubRepoStub{}
					billing := newOpenAIRecordUsageServiceWithBillingRepoForTest(logRepo, ledger, users, subs, nil)
					pricing := map[string]*LiteLLMModelPricing{"gpt-5.1": {InputCostPerToken: 2e-6, OutputCostPerToken: 5e-6}, "cline-pass/model": {InputCostPerToken: 7e-6, OutputCostPerToken: 13e-6}}
					billing.billingService = NewBillingService(billing.cfg, &PricingService{pricingData: pricing})
					groupID := int64(91)
					input := &OpenAIRecordUsageInput{Result: result, APIKey: &APIKey{ID: 9101, GroupID: &groupID, Group: &Group{ID: groupID, Platform: PlatformCline, RateMultiplier: 1.1, Hydrated: true}}, User: &User{ID: 9102}, Account: a, ChannelUsageFields: ChannelUsageFields{OriginalModel: "gpt-5.1", ChannelMappedModel: "gpt-5.1", BillingModelSource: source}}
					expected := 8*2e-6 + 4*5e-6
					if source == BillingModelSourceUpstream {
						expected = 8*7e-6 + 4*13e-6
					}
					require.NoError(t, billing.RecordUsage(context.Background(), input))
					require.NotNil(t, logRepo.lastLog)
					require.NotNil(t, ledger.lastCmd)
					require.InDelta(t, expected, logRepo.lastLog.TotalCost, 1e-12)
					require.InDelta(t, expected*1.1, ledger.lastCmd.BalanceCost, 1e-12)
					require.Equal(t, "review-nonstream", ledger.lastCmd.RequestID)
					ledger.result = &UsageBillingApplyResult{Applied: false}
					logRepo.inserted = false
					require.NoError(t, billing.RecordUsage(context.Background(), input))
					require.Zero(t, users.deductCalls)
					require.Zero(t, subs.incrementCalls)
				})
			}
		}
	}
}

func TestClineNonstreamMalformedEnvelopeNeverReturnsEmptySuccess(t *testing.T) {
	for _, protocol := range []string{"chat", "responses", "messages"} {
		for i, payload := range []string{`{"success":false}`, `{"success":true}`, `{"success":true,"data":{"choices":{}}}`, `{"success":true,"data":{"error":{"message":"failure"}}}`} {
			t.Run(fmt.Sprintf("%s/%d", protocol, i), func(t *testing.T) {
				upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(payload))}}
				gateway := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream, accountRepo: &clineAdmissionTestRepository{}}
				body, path := clineProtocolRequest(protocol, false)
				rec := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(rec)
				c.Request = httptest.NewRequest("POST", path, bytes.NewReader(body))
				result, err := invokeClineProtocol(gateway, c, clineRoutingAccount(t, cline.ModePass, cline.AuthAPIKey), body, protocol)
				require.Error(t, err)
				require.Nil(t, result)
				require.Equal(t, 502, rec.Code)
				require.NotContains(t, rec.Body.String(), "response.completed")
			})
		}
	}
}
