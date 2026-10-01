//go:build unit

package service

import (
	"bytes"
	"context"
	"github.com/Wei-Shaw/sub2api/internal/pkg/cline"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Exercise real forwarding and monetary command code with synthetic transports.
func TestClinePartialFailurePreservesObservedUsageAndBillingIdentity(t *testing.T) {
	for _, protocol := range []string{"chat", "responses", "messages"} {
		t.Run(protocol, func(t *testing.T) {
			a := clineRoutingAccount(t, cline.ModePass, cline.AuthAPIKey)
			a.Credentials["model_mapping"] = map[string]any{"gpt-5.1": "cline-pass/model"}
			body, path := clineProtocolRequest(protocol, true)
			body = bytes.ReplaceAll(body, []byte("public-model"), []byte("gpt-5.1"))
			partial := `{"id":"chatcmpl-cline-billing","model":"cline-pass/model","choices":[{"index":0,"delta":{"content":"partial answer"}}],"usage":{"prompt_tokens":8,"completion_tokens":4,"total_tokens":12}}`
			payload := "data: " + partial + "\n\ndata: {\"error\":{\"message\":\"generation failed\"}}\n\ndata: [DONE]\n\n"
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
			require.Contains(t, rec.Body.String(), "partial answer")
			for _, terminal := range []string{"[DONE]", "response.completed", "message_stop"} {
				require.NotContains(t, rec.Body.String(), terminal)
			}
			usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
			billingRepo := &openAIRecordUsageBillingRepoStub{result: &UsageBillingApplyResult{Applied: true}}
			userRepo := &openAIRecordUsageUserRepoStub{}
			subRepo := &openAIRecordUsageSubRepoStub{}
			billing := newOpenAIRecordUsageServiceWithBillingRepoForTest(usageRepo, billingRepo, userRepo, subRepo, nil)
			input := &OpenAIRecordUsageInput{Result: result, APIKey: &APIKey{ID: 9101}, User: &User{ID: 9102}, Account: a}
			require.NoError(t, billing.RecordUsage(context.Background(), input))
			require.NotNil(t, billingRepo.lastCmd)
			requestID := billingRepo.lastCmd.RequestID
			require.NotEmpty(t, requestID)
			require.Equal(t, "gpt-5.1", result.Model)
			expected := expectedOpenAICost(t, billing, "gpt-5.1", result.Usage, 1.1)
			require.Positive(t, expected.ActualCost)
			require.InDelta(t, expected.ActualCost, usageRepo.lastLog.ActualCost, 1e-12)
			require.Equal(t, 8, usageRepo.lastLog.InputTokens)
			require.Equal(t, 4, usageRepo.lastLog.OutputTokens)
			billingRepo.result = &UsageBillingApplyResult{Applied: false}
			usageRepo.inserted = false
			require.NoError(t, billing.RecordUsage(context.Background(), input))
			require.Equal(t, requestID, billingRepo.lastCmd.RequestID)
			require.Equal(t, 2, billingRepo.calls)
			require.Zero(t, userRepo.deductCalls)
			require.Zero(t, subRepo.incrementCalls)
		})
	}
}

func TestClineTerminalFailureUsageIsNotLostOrDoubleCounted(t *testing.T) {
	for _, protocol := range []string{"chat", "responses", "messages"} {
		t.Run(protocol, func(t *testing.T) {
			a := clineRoutingAccount(t, cline.ModePass, cline.AuthAPIKey)
			a.Credentials["model_mapping"] = map[string]any{"gpt-5.1": "cline-pass/model"}
			body, path := clineProtocolRequest(protocol, true)
			body = bytes.ReplaceAll(body, []byte("public-model"), []byte("gpt-5.1"))
			partial := `{"id":"chatcmpl-cline-billing","model":"cline-pass/model","choices":[{"index":0,"delta":{"content":"partial answer"}}]}`
			payload := "data: " + partial + "\n\ndata: {\"error\":{\"message\":\"generation failed\"},\"usage\":{\"prompt_tokens\":8,\"completion_tokens\":4,\"total_tokens\":12}}\n\ndata: [DONE]\n\n"
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
			require.Contains(t, rec.Body.String(), "partial answer")
			for _, terminal := range []string{"[DONE]", "response.completed", "message_stop"} {
				require.NotContains(t, rec.Body.String(), terminal)
			}
			usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
			billingRepo := &openAIRecordUsageBillingRepoStub{result: &UsageBillingApplyResult{Applied: true}}
			userRepo := &openAIRecordUsageUserRepoStub{}
			subRepo := &openAIRecordUsageSubRepoStub{}
			billing := newOpenAIRecordUsageServiceWithBillingRepoForTest(usageRepo, billingRepo, userRepo, subRepo, nil)
			input := &OpenAIRecordUsageInput{Result: result, APIKey: &APIKey{ID: 9101}, User: &User{ID: 9102}, Account: a}
			require.NoError(t, billing.RecordUsage(context.Background(), input))
			require.NotNil(t, billingRepo.lastCmd)
			requestID := billingRepo.lastCmd.RequestID
			require.NotEmpty(t, requestID)
			require.Equal(t, "gpt-5.1", result.Model)
			expected := expectedOpenAICost(t, billing, "gpt-5.1", result.Usage, 1.1)
			require.Positive(t, expected.ActualCost)
			require.InDelta(t, expected.ActualCost, usageRepo.lastLog.ActualCost, 1e-12)
			require.Equal(t, 8, usageRepo.lastLog.InputTokens)
			require.Equal(t, 4, usageRepo.lastLog.OutputTokens)
			billingRepo.result = &UsageBillingApplyResult{Applied: false}
			usageRepo.inserted = false
			require.NoError(t, billing.RecordUsage(context.Background(), input))
			require.Equal(t, requestID, billingRepo.lastCmd.RequestID)
			require.Equal(t, 2, billingRepo.calls)
			require.Zero(t, userRepo.deductCalls)
			require.Zero(t, subRepo.incrementCalls)
		})
	}
}
