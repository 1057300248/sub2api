//go:build unit

package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClineSuccessfulResponseSurvivesLateDeadline(t *testing.T) {
	responses := map[string]string{
		"chat_json":      `{"choices":[{"message":{"content":"ok"}}]}`,
		"responses_json": `{"object":"response","status":"completed","output":[]}`,
		"messages_json":  `{"type":"message","content":[],"stop_reason":"end_turn"}`,
		"chat_sse":       "data: {\"choices\":[]}\n\ndata: [DONE]\n\n",
		"responses_sse":  "event: response.completed\ndata: {\"type\":\"response.completed\"}\n\ndata: [DONE]\n\n",
		"messages_sse":   "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n",
	}
	for name, body := range responses {
		t.Run(name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/fixture", nil)
			_, err := c.Writer.WriteString(body)
			require.NoError(t, err)
			require.True(t, c.Writer.Written())
			// Expiry is introduced after successful response commit. No sleeps or
			// scheduler race is needed to exercise the post-Forward boundary.
			ctx, cancel := context.WithDeadline(c.Request.Context(), time.Now().Add(-time.Second))
			defer cancel()
			c.Request = c.Request.WithContext(ctx)
			result := &service.OpenAIForwardResult{Usage: service.OpenAIUsage{InputTokens: 8, OutputTokens: 4}}
			submissions, errorsWritten := 0, 0
			finished := finishClineForward(c, &service.Account{Platform: service.PlatformCline}, result, nil,
				func(*service.OpenAIForwardResult) { submissions++ },
				func() { errorsWritten++; _, _ = c.Writer.WriteString("unexpected deadline error") })
			assert.False(t, finished, "a successful Forward belongs to the ordinary success/settlement path")
			assert.Zero(t, submissions, "termination must not duplicate success settlement")
			assert.Zero(t, errorsWritten)
			assert.Empty(t, c.GetString("cline_forward_stop_reason"))
			assert.Equal(t, http.StatusOK, rec.Code)
			assert.Equal(t, body, rec.Body.String())
		})
	}
}
