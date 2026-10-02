package cline

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func wrappedRateLimitFixture(t *testing.T, inner string) []byte {
	t.Helper()
	body, err := json.Marshal(map[string]any{"error": map[string]any{"status": 502, "message": "Failed to create stream: inference request failed: failed to generate stream from Vercel: failed to invoke model 'vendor/model' with streaming: request failed with status 429: " + inner}})
	require.NoError(t, err)
	return body
}

func TestClineWrappedRateLimitClassification(t *testing.T) {
	inner := `{"error":{"message":"Rate limit exceeded for vendor/model: this team's limit is exhausted. Try again in 45s."}}`
	body := wrappedRateLimitFixture(t, inner)
	got, ok := WrappedRateLimitBody(body)
	require.True(t, ok)
	assert.JSONEq(t, inner, string(got))
	status, confirmed := GenerationErrorStatus(body)
	require.True(t, confirmed)
	assert.Equal(t, http.StatusTooManyRequests, status)
	now := time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC)
	for _, outer := range []int{429, 500, 502, 503} {
		limit, handled := Classify(outer, http.Header{"Retry-After": []string{"90"}}, body, "vendor/model", now)
		require.True(t, handled)
		assert.Equal(t, ScopeThrottle, limit.Scope)
		assert.Equal(t, "throttle", limit.Kind)
		require.NotNil(t, limit.ResetAt)
		assert.Equal(t, now.Add(90*time.Second), limit.Until())
	}
	for _, outer := range []int{200, 400, 401, 403} {
		_, handled := Classify(outer, nil, body, "vendor/model", now)
		assert.False(t, handled)
	}
	assert.Contains(t, string(body), `"status":502`, "the original diagnostic envelope stays unchanged")
}

func TestClineWrappedRateLimitRejectsAmbiguousAndGeneratedData(t *testing.T) {
	inner := `{"error":{"code":"rate_limit_exceeded","message":"Busy"}}`
	valid := wrappedRateLimitFixture(t, inner)
	var envelope map[string]any
	require.NoError(t, json.Unmarshal(valid, &envelope))
	for _, tc := range []struct {
		name string
		body []byte
	}{
		{"ordinary_502", []byte(`{"error":{"status":502,"message":"upstream unavailable, saw 429 earlier"}}`)},
		{"wrong_chain", []byte(`{"error":{"message":"request failed with status 429: {\"error\":{\"code\":\"rate_limit_exceeded\"}}"}}`)},
		{"inner_not_error", wrappedRateLimitFixture(t, `{"choices":[{"message":{"content":"rate_limit_exceeded"}}]}`)},
		{"inner_unknown", wrappedRateLimitFixture(t, `{"error":{"message":"The model wrote 429"}}`)},
		{"trailing_prose", wrappedRateLimitFixture(t, inner+" unexpected suffix")},
		{"oversize", append(valid, []byte(strings.Repeat(" ", MaxBodyBytes))...)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, ok := WrappedRateLimitBody(tc.body)
			assert.False(t, ok)
		})
	}
	errFields, ok := envelope["error"].(map[string]any)
	require.True(t, ok)
	message := errFields["message"]
	for _, field := range []string{"content", "reasoning_content", "tool_calls"} {
		body, err := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{field: message}}}})
		require.NoError(t, err)
		_, ok := WrappedRateLimitBody(body)
		assert.False(t, ok)
		_, confirmed := GenerationErrorStatus(body)
		assert.False(t, confirmed)
	}
}
