package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/cline"
	"github.com/gin-gonic/gin"
)

// This is an explicitly requested administrative inference test, not metadata
// discovery. It requires an exact configured model and never falls back to a
// default/paid model or tries a native Responses/Anthropic/compaction endpoint.
func (s *AccountTestService) testClineAccountConnection(c *gin.Context, account *Account, publicModel, prompt, mode string) error {
	if mode != "" && mode != AccountTestModeDefault && mode != AccountTestModeGrokText {
		return s.sendErrorAndEnd(c, "Cline supports a text Chat Completions connection test only")
	}
	publicModel = strings.TrimSpace(publicModel)
	if !account.IsClineModelSupported(publicModel) {
		return s.sendErrorAndEnd(c, "Select an explicitly configured Cline model permitted by this account mode; Free API and paid fallback are disabled")
	}
	if s.openaiGatewayService == nil {
		return s.sendErrorAndEnd(c, "Cline guarded gateway is unavailable")
	}
	upstreamModel := account.GetMappedModel(publicModel)
	payload := createOpenAIChatCompletionsTestPayload(upstreamModel, prompt)
	payload["max_tokens"] = 64
	body, err := json.Marshal(payload)
	if err != nil {
		return s.sendErrorAndEnd(c, "Failed to build Cline test request")
	}
	if err := account.ValidateClineOutboundBody(body); err != nil {
		return s.sendErrorAndEnd(c, err.Error())
	}
	target, err := s.openaiGatewayService.openAIChatCompletionsTargetURL(account)
	if err != nil {
		return s.sendErrorAndEnd(c, "Invalid Cline upstream URL")
	}
	s.sendEvent(c, TestEvent{Type: "test_start", Model: publicModel})
	resp, err := s.openaiGatewayService.sendCCUpstreamRequest(c.Request.Context(), c, account, target, body, true, account.GetOpenAIProtocolAPIKey(), "", "")
	if err != nil {
		return s.sendErrorAndEnd(c, "Cline connection request failed")
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		payload, readErr := io.ReadAll(io.LimitReader(resp.Body, cline.MaxBodyBytes+1))
		if readErr == nil && len(payload) <= cline.MaxBodyBytes && s.openaiGatewayService.rateLimitService != nil {
			ctx, cancel := context.WithTimeout(context.WithoutCancel(c.Request.Context()), clineLimitPersistTimeout)
			defer cancel()
			s.openaiGatewayService.rateLimitService.handleClineScopedUpstreamError(ctx, account, resp.StatusCode, resp.Header, payload, upstreamModel)
		}
		// Do not expose untrusted upstream diagnostics or credentials in test SSE.
		return s.sendErrorAndEnd(c, fmt.Sprintf("Cline upstream returned HTTP %d", resp.StatusCode))
	}
	return s.processOpenAIChatCompletionsStream(c, resp.Body)
}
