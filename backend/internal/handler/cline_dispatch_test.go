package handler

import (
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestClineStandaloneAndCompositeDispatch(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, groupPlatform := range []string{service.PlatformCline, service.PlatformComposite} {
		t.Run(groupPlatform, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest("POST", "/v1/messages", nil)
			c.Request = c.Request.WithContext(service.WithResolvedTargetPlatform(c.Request.Context(), service.PlatformCline))
			key := &service.APIKey{Group: &service.Group{Platform: groupPlatform, AllowMessagesDispatch: false}}
			require.True(t, allowOpenAICompatibleMessagesDispatch(c, key))
			require.Empty(t, resolveOpenAIMessagesDispatchMappedModel(c, key, "claude-sonnet-4-5"))
			require.True(t, openAICompatibleTextTargetAllowed(c, key, "public-model"))
			require.Equal(t, service.PlatformCline, openAICompatibleRequestPlatform(c.Request.Context(), key))
			service.SetActualOpenAIUpstreamEndpoint(c, "/v1/chat/completions")
			require.Equal(t, "/v1/chat/completions", GetUpstreamEndpoint(c, service.PlatformCline))
		})
	}
	require.Empty(t, defaultModelIDsForPlatform(service.PlatformCline))
	require.False(t, isResponsesWebSocketCompositePlatform(service.PlatformCline))
	require.False(t, allowOpenAICompatibleMessagesDispatch(nil, &service.APIKey{Group: &service.Group{Platform: service.PlatformOpenAI}}))
}
