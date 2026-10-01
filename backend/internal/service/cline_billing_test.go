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

	"github.com/Wei-Shaw/sub2api/internal/pkg/cline"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestClinePartialFailurePreservesObservedUsageAndBillingIdentity(t *testing.T) {
	runClineFailureBillingRegression(t, false)
}

func TestClineTerminalFailureUsageIsNotLostOrDoubleCounted(t *testing.T) {
	runClineFailureBillingRegression(t, true)
}

// Exercise actual forwarding and monetary command code, not a manufactured
// forward result. Prices are deliberately distinct synthetic fixtures, not
// provider reference prices. This catches a wrong billing source without
// depending on the changing catalog or its unknown-model fallback.
func runClineFailureBillingRegression(t *testing.T, terminalUsage bool) {
	t.Helper()
	for _, protocol := range []string{"chat", "responses", "messages"} {
		for _, source := range []string{BillingModelSourceRequested, BillingModelSourceUpstream} {
			t.Run(protocol+"/"+source, func(t *testing.T) {
				a := clineRoutingAccount(t, cline.ModePass, cline.AuthAPIKey)
				a.Credentials["model_mapping"] = map[string]any{"gpt-5.1": "cline-pass/model"}
				body, path := clineProtocolRequest(protocol, true)
				body = bytes.ReplaceAll(body, []byte("public-model"), []byte("gpt-5.1"))
				partial := `{"id":"chatcmpl-cline-billing","model":"cline-pass/model","choices":[{"index":0,"delta":{"content":"partial answer"}}],"usage":{"prompt_tokens":8,"completion_tokens":4,"total_tokens":12}}`
				failure := `{"error":{"message":"generation failed"}}`
				if terminalUsage {
					partial = `{"id":"chatcmpl-cline-billing","model":"cline-pass/model","choices":[{"index":0,"delta":{"content":"partial answer"}}]}`
					failure = `{"error":{"message":"generation failed"},"usage":{"prompt_tokens":8,"completion_tokens":4,"total_tokens":12}}`
				}
				payload := "data: " + partial + "\n\ndata: " + failure + "\n\ndata: [DONE]\n\n"
				upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}, "X-Request-Id": []string{"cline-billing-stable"}}, Body: io.NopCloser(strings.NewReader(payload))}}
				gateway := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream, accountRepo: &clineAdmissionTestRepository{}}
				rec := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(rec)
				c.Request = httptest.NewRequest("POST", path, bytes.NewReader(body))
				result, err := invokeClineProtocol(gateway, c, a, body, protocol)
				require.Error(t, err)
				require.NotNil(t, result, "known partial usage must remain available")
				require.Equal(t, 8, result.Usage.InputTokens)
				require.Equal(t, 4, result.Usage.OutputTokens)
				require.Equal(t, "gpt-5.1", result.Model)
				require.Equal(t, "cline-pass/model", result.UpstreamModel)
				require.Contains(t, rec.Body.String(), "partial answer")
				for _, terminal := range []string{"[DONE]", "response.completed", "message_stop"} {
					require.NotContains(t, rec.Body.String(), terminal)
				}

				usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
				billingRepo := &openAIRecordUsageBillingRepoStub{result: &UsageBillingApplyResult{Applied: true}}
				userRepo := &openAIRecordUsageUserRepoStub{}
				subRepo := &openAIRecordUsageSubRepoStub{}
				billing := newOpenAIRecordUsageServiceWithBillingRepoForTest(usageRepo, billingRepo, userRepo, subRepo, nil)
				pricing := make(map[string]*LiteLLMModelPricing)
				pricing["gpt-5.1"] = &LiteLLMModelPricing{InputCostPerToken: 2e-6, OutputCostPerToken: 5e-6}
				pricing["cline-pass/model"] = &LiteLLMModelPricing{InputCostPerToken: 7e-6, OutputCostPerToken: 13e-6}
				billing.billingService = NewBillingService(billing.cfg, &PricingService{pricingData: pricing})
				input := &OpenAIRecordUsageInput{
					Result:  result,
					APIKey:  &APIKey{ID: 9101},
					User:    &User{ID: 9102},
					Account: a,
					ChannelUsageFields: ChannelUsageFields{
						OriginalModel:      "gpt-5.1",
						ChannelMappedModel: "gpt-5.1",
						BillingModelSource: source,
					},
				}
				// The existing Messages bridge carries the account-mapped
				// BillingModel explicitly. Requested billing must override it;
				// upstream billing must retain it. Other bridges currently carry
				// the original billing model. Do not rewrite those contracts here.
				expectedTotal := 8*2e-6 + 4*5e-6
				if protocol == "messages" {
					require.Equal(t, "cline-pass/model", result.BillingModel)
					if source == BillingModelSourceUpstream {
						expectedTotal = 8*7e-6 + 4*13e-6
					}
				}
				expectedActual := expectedTotal * 1.1
				require.NoError(t, billing.RecordUsage(context.Background(), input))
				require.NotNil(t, billingRepo.lastCmd)
				require.NotNil(t, usageRepo.lastLog)
				requestID := billingRepo.lastCmd.RequestID
				require.Equal(t, "cline-billing-stable", requestID)
				require.Equal(t, requestID, usageRepo.lastLog.RequestID)
				require.Equal(t, "gpt-5.1", usageRepo.lastLog.Model)
				require.InDelta(t, expectedTotal, usageRepo.lastLog.TotalCost, 1e-12)
				require.InDelta(t, expectedActual, usageRepo.lastLog.ActualCost, 1e-12)
				require.InDelta(t, expectedActual, billingRepo.lastCmd.BalanceCost, 1e-12)
				require.Equal(t, 8, usageRepo.lastLog.InputTokens)
				require.Equal(t, 4, usageRepo.lastLog.OutputTokens)

				// Replaying the identical command is reported as a duplicate by
				// the ledger fake. Neither fallback balance nor subscription
				// mutation may run, and the command identity/amount must survive.
				billingRepo.result = &UsageBillingApplyResult{Applied: false}
				usageRepo.inserted = false
				require.NoError(t, billing.RecordUsage(context.Background(), input))
				require.Equal(t, requestID, billingRepo.lastCmd.RequestID)
				require.InDelta(t, expectedActual, billingRepo.lastCmd.BalanceCost, 1e-12)
				require.Equal(t, 2, billingRepo.calls)
				require.Zero(t, userRepo.deductCalls)
				require.Zero(t, subRepo.incrementCalls)
			})
		}
	}
}
