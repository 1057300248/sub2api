package cline

import (
	"errors"
	"io"
	"strings"
	"testing"
)

func TestClineJSONEnvelopePreservesCompletionAndUsage(t *testing.T) {
	completion := `{"id":"fixture","object":"chat.completion","model":"cline-pass/model","choices":[{"index":0,"message":{"role":"assistant","content":null,"reasoning_content":"not an error","tool_calls":[{"id":"call1","type":"function","function":{"name":"lookup","arguments":"{\"id\":9007199254740993}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":8,"completion_tokens":4,"total_tokens":12,"prompt_tokens_details":{"cached_tokens":2},"completion_tokens_details":{"reasoning_tokens":3}},"future_field":9007199254740993}`
	for _, wrapped := range []bool{false, true} {
		body := completion
		if wrapped {
			body = `{"success":true,"data":` + completion + `}`
		}
		reader := GuardJSONBody(io.NopCloser(strings.NewReader(body)), int64(len(body)), false, func([]byte) { t.Fatal("successful completion classified as error") })
		closeClineTestBody(t, reader)
		got, err := io.ReadAll(reader)
		if err != nil || string(got) != completion {
			t.Fatalf("wrapped=%v got=%s err=%v", wrapped, got, err)
		}
	}
}

func TestClineJSONEnvelopeRejectsInvalidAndNestedErrors(t *testing.T) {
	for _, body := range []string{
		`{"success":false}`, `{"success":true}`, `{"success":null,"data":{}}`, `{"success":"true","data":{}}`,
		`{"success":true,"data":null}`, `{"success":true,"data":[]}`, `{"success":true,"data":{"choices":{}}}`,
		`{"success":true,"data":{"choices":[]}}`, `{"choices":null}`, `{"choices":[null]}`, `{"choices":[{"message":null}]}`,
		`{"success":true,"choices":[],"data":{"choices":[{"message":{"content":"x"}}]}}`,
		`{"success":true,"data":{"success":true,"data":{"choices":[]}}}`,
	} {
		r := GuardJSONBody(io.NopCloser(strings.NewReader(body)), 4096, false, nil)
		closeClineTestBody(t, r)
		got, err := io.ReadAll(r)
		if len(got) != 0 || err == nil {
			t.Fatalf("invalid JSON accepted: %s got=%q err=%v", body, got, err)
		}
	}
	calls := 0
	r := GuardJSONBody(io.NopCloser(strings.NewReader(`{"success":true,"data":{"error":{"code":429,"message":"Busy"}}}`)), 4096, false, func(body []byte) {
		calls++
		if !HasGenerationError(body) {
			t.Error("lost nested failure")
		}
	})
	closeClineTestBody(t, r)
	got, err := io.ReadAll(r)
	if len(got) != 0 || !errors.Is(err, ErrStreamFailure) || calls != 1 {
		t.Fatalf("got=%q err=%v calls=%d", got, err, calls)
	}
	if _, err = r.Read(make([]byte, 1)); !errors.Is(err, ErrStreamFailure) || calls != 1 {
		t.Fatal("failure callback repeated")
	}
	r = GuardJSONBody(io.NopCloser(strings.NewReader(`{"success":true,"data":{"choices":[{"message":{"content":"x"}}]}}`)), 4096, true, nil)
	closeClineTestBody(t, r)
	if _, err = io.ReadAll(r); !errors.Is(err, ErrExpectedSSE) {
		t.Fatal("streaming JSON success accepted", err)
	}
}
