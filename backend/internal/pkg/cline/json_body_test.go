package cline

import (
	"errors"
	"io"
	"strings"
	"testing"
)

func TestClineJSON200FailureAndBounds(t *testing.T) {
	cases := []struct {
		name, body string
		limit      int64
		stream     bool
		want       error
		callbacks  int
	}{
		{"error", `{"error":{"code":429,"message":"quota"}}`, 4096, false, ErrStreamFailure, 1},
		{"malformed_sibling", `{"error":"quota","choices":{}}`, 4096, false, ErrStreamFailure, 1},
		{"stream_json_error", `{"error":"quota"}`, 4096, true, ErrStreamFailure, 1},
		{"stream_json_success", `{"choices":[]}`, 4096, true, ErrExpectedSSE, 0},
		{"too_large", strings.Repeat("x", 65), 64, false, ErrJSONBodyTooLarge, 0},
		{"invalid", `<html>error</html>`, 4096, false, ErrInvalidJSONBody, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			reader := GuardJSONBody(io.NopCloser(strings.NewReader(tc.body)), tc.limit, tc.stream, func([]byte) { calls++ })
			closeClineTestBody(t, reader)
			output, err := io.ReadAll(reader)
			if len(output) != 0 || !errors.Is(err, tc.want) || calls != tc.callbacks {
				t.Fatalf("output=%q err=%v callbacks=%d", output, err, calls)
			}
			if n, next := reader.Read(make([]byte, 1)); n != 0 || !errors.Is(next, tc.want) || calls != tc.callbacks {
				t.Fatalf("failure not sticky: n=%d err=%v callbacks=%d", n, next, calls)
			}
		})
	}
}

func TestClineJSONSuccessPreservesBytesAndClose(t *testing.T) {
	input := ` {"choices":[{"message":{"content":"error is normal text"}}]} `
	for _, limit := range []int64{int64(len(input)), int64(len(input) + 1), 0} {
		source := &clineCloseTestBody{Reader: strings.NewReader(input)}
		reader := GuardJSONBody(source, limit, false, func([]byte) { t.Error("unexpected error callback") })
		if n, err := reader.Read(nil); n != 0 || err != nil {
			t.Fatalf("zero-length read: %d %v", n, err)
		}
		var got strings.Builder
		one := make([]byte, 1)
		for {
			n, err := reader.Read(one)
			if _, writeErr := got.Write(one[:n]); writeErr != nil {
				t.Fatal(writeErr)
			}
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
		}
		if got.String() != input {
			t.Fatalf("response changed: %q", got.String())
		}
		if err := reader.Close(); err != nil || source.closeCalls != 1 {
			t.Fatalf("close: calls=%d error=%v", source.closeCalls, err)
		}
	}
}

func TestClineJSONClosePropagatesSourceError(t *testing.T) {
	want := errors.New("close failed")
	source := &clineCloseTestBody{Reader: strings.NewReader("{}"), closeErr: want}
	reader := GuardJSONBody(source, 0, false, nil)
	if err := reader.Close(); !errors.Is(err, want) || source.closeCalls != 1 {
		t.Fatalf("close: calls=%d error=%v", source.closeCalls, err)
	}
}
