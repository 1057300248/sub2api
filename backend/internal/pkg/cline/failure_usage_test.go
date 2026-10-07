package cline

import (
	"errors"
	"io"
	"strings"
	"testing"
)

func TestClineTerminalFailurePreservesOnlyValidatedUsage(t *testing.T) {
	payload := `{"error":{"code":429,"message":"secret error body"},"usage":{"prompt_tokens":8,"completion_tokens":4,"total_tokens":12,"prompt_tokens_details":{"cached_tokens":2},"completion_tokens_details":{"reasoning_tokens":3},"private":"not-copy"},"choices":[{"delta":{"content":"not-copy"}}]}`
	r := GuardSSEBody(io.NopCloser(strings.NewReader("data: "+payload+"\n\ndata: [DONE]\n\n")), 4096, nil)
	closeClineTestBody(t, r)
	output, err := io.ReadAll(r)
	if !errors.Is(err, ErrStreamFailure) || !strings.Contains(string(output), `"prompt_tokens":8`) || !strings.Contains(string(output), `"choices":[]`) {
		t.Fatalf("usage/error: %q %v", output, err)
	}
	for _, leak := range []string{"secret error body", "not-copy", "[DONE]", "\"error\""} {
		if strings.Contains(string(output), leak) {
			t.Fatal("copied unapproved field", leak)
		}
	}
	for _, usage := range []string{`null`, `{}`, `{"prompt_tokens":-1,"completion_tokens":4}`, `{"prompt_tokens":8.5,"completion_tokens":4}`, `{"prompt_tokens":8,"completion_tokens":4,"total_tokens":99}`, `{"prompt_tokens":8,"completion_tokens":4,"prompt_tokens_details":{"cached_tokens":99}}`} {
		if output := failureUsageEvent([]byte(`{"usage":` + usage + `}`)); output != nil {
			t.Fatalf("invalid usage accepted: %q", output)
		}
	}
}
