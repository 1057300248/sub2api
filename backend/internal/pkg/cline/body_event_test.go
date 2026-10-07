package cline

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestClineSSECompleteEventFailures(t *testing.T) {
	cases := map[string]string{
		"multiline":         "data: {\n" + `data: "error":{"message":"quota"}}` + "\n\n",
		"named_before_data": "event: error\ndata: {\"message\":\"quota\"}\n\n",
		"named_after_data":  "data: {\"message\":\"quota\"}\nevent: error\n\n",
		"named_plain_text":  "event: error\ndata: upstream broke\n\n",
		"named_empty":       "event: error\n\n",
		"typed":             "data: {\"type\":\"error\",\"message\":\"quota\"}\n\n",
		"malformed_sibling": "data: {\"error\":{\"message\":\"quota\"},\"choices\":{}}\n\n",
		"incomplete_error":  "data: {\"error\":\"quota\"}",
	}
	for name, frame := range cases {
		t.Run(name, func(t *testing.T) {
			calls := 0
			reader := GuardSSEBody(io.NopCloser(strings.NewReader(frame+"data: [DONE]\n\n")), 4096, func(payload []byte) {
				calls++
				if !HasGenerationError(payload) {
					t.Errorf("callback lacks confirmed envelope: %s", payload)
				}
			})
			// A truncated error has no following sentinel; it must still fail.
			if name == "incomplete_error" {
				closeClineTestBody(t, reader)
				reader = GuardSSEBody(io.NopCloser(strings.NewReader(frame)), 4096, func([]byte) { calls++ })
			}
			closeClineTestBody(t, reader)
			got, err := io.ReadAll(reader)
			if !errors.Is(err, ErrStreamFailure) || len(got) != 0 || calls != 1 {
				t.Fatalf("output=%q err=%v callbacks=%d", got, err, calls)
			}
			for i := 0; i < 3; i++ {
				n, again := reader.Read(make([]byte, 1))
				if n != 0 || !errors.Is(again, ErrStreamFailure) || calls != 1 {
					t.Fatalf("failure not sticky: n=%d err=%v calls=%d", n, again, calls)
				}
			}
		})
	}
}

func TestClineSSEEventBoundsAndTruncation(t *testing.T) {
	cases := []struct {
		name, input string
		limit       int
		want        error
	}{
		{"aggregate", strings.Repeat(": heartbeat\n", 20) + "\n", 64, ErrSSEFrameTooLarge},
		{"line", "data: " + strings.Repeat("x", 100), 64, ErrSSEFrameTooLarge},
		{"truncated_success", "data: {\"choices\":[]}\n", 1024, io.ErrUnexpectedEOF},
		{"truncated_done", "data: [DONE]\n", 1024, io.ErrUnexpectedEOF},
		{"invalid_json", "data: {broken}\n\ndata: [DONE]\n\n", 1024, ErrInvalidSSEFrame},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reader := GuardSSEBody(io.NopCloser(strings.NewReader(tc.input)), tc.limit, func([]byte) { t.Error("unconfirmed quota callback") })
			closeClineTestBody(t, reader)
			output, err := io.ReadAll(reader)
			if !errors.Is(err, tc.want) || len(output) != 0 {
				t.Fatalf("output=%q err=%v want=%v", output, err, tc.want)
			}
		})
	}
}

func TestClineSSEMultilineAndLineEndings(t *testing.T) {
	for _, ending := range []string{"\n", "\r\n", "\r"} {
		t.Run(strings.ReplaceAll(strings.ReplaceAll(ending, "\r", "CR"), "\n", "LF"), func(t *testing.T) {
			input := "\xef\xbb\xbf: ping\n\n" + "event: message\ndata: {\ndata: \"choices\":[{\"delta\":{\"content\":\"error is normal text\"}}]}\n\ndata: [DONE]\n\n"
			input = strings.ReplaceAll(input, "\n", ending)
			reader := GuardSSEBody(io.NopCloser(strings.NewReader(input)), 4096, func([]byte) { t.Error("normal text classified as failure") })
			closeClineTestBody(t, reader)
			output, err := io.ReadAll(reader)
			if err != nil {
				t.Fatal(err)
			}
			scanner := bufio.NewScanner(bytes.NewReader(output))
			dataLines := 0
			for scanner.Scan() {
				if strings.HasPrefix(scanner.Text(), "data:") {
					dataLines++
				}
			}
			if err := scanner.Err(); err != nil {
				t.Fatal(err)
			}
			if dataLines != 2 || !bytes.Contains(output, []byte("error is normal text")) || !bytes.HasSuffix(output, []byte("data: [DONE]\n\n")) {
				t.Fatalf("multiline event not readable by shared line scanner: %q", output)
			}
		})
	}
}

func TestClineGenerationErrorIgnoresGeneratedFields(t *testing.T) {
	for _, payload := range []string{
		`{"error":null,"choices":[{"delta":{"content":"ClinePass limit reached","error":"fiction"}}]}`,
		`{"choices":[{"message":{"content":"{\"error\":\"quota\"}"},"finish_reason":"stop"}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"function":{"arguments":"{\"type\":\"error\"}"}}]}}]}`,
	} {
		if HasGenerationError([]byte(payload)) {
			t.Fatalf("generated text treated as provider error: %s", payload)
		}
	}
}

func TestClineSSEEmptyDataHeartbeatAndMissingDone(t *testing.T) {
	input := "data:\n\ndata: \ndata:\n\ndata: [DONE]\n\n"
	r := GuardSSEBody(io.NopCloser(strings.NewReader(input)), 1024, nil)
	closeClineTestBody(t, r)
	if output, err := io.ReadAll(r); err != nil || string(output) != input {
		t.Fatalf("heartbeat changed: %q %v", output, err)
	}
	for _, input := range []string{"", ": heartbeat\n\n", "data: {\"choices\":[]}\n\n", "{\"error\":{\"code\":429}}"} {
		r := GuardSSEBody(io.NopCloser(strings.NewReader(input)), 1024, nil)
		closeClineTestBody(t, r)
		if _, err := io.ReadAll(r); !errors.Is(err, io.ErrUnexpectedEOF) {
			t.Fatalf("missing DONE accepted for %q: %v", input, err)
		}
	}
	calls := 0
	r = GuardSSEBody(io.NopCloser(strings.NewReader("event: error\ndata: null\n\n")), 1024, func(payload []byte) {
		calls++
		if !HasGenerationError(payload) {
			t.Error("named null error lost its envelope")
		}
	})
	closeClineTestBody(t, r)
	if output, err := io.ReadAll(r); len(output) != 0 || !errors.Is(err, ErrStreamFailure) || calls != 1 {
		t.Fatalf("named null error accepted: %q %v calls=%d", output, err, calls)
	}
}
