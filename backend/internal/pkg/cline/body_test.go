package cline

import (
	"errors"
	"io"
	"strings"
	"testing"
)

func TestClineSSEFailurePreventsSuccessfulTerminal(t *testing.T) {
	input := "data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\ndata: {\"choices\":[{\"finish_reason\":\"error\"}]}\n\ndata: [DONE]\n\n"
	calls := 0
	r := GuardSSEBody(io.NopCloser(strings.NewReader(input)), 1024, func(body []byte) { calls++ })
	closeClineTestBody(t, r)
	output, err := io.ReadAll(r)
	if !errors.Is(err, ErrStreamFailure) || calls != 1 || !strings.Contains(string(output), "partial") || strings.Contains(string(output), "[DONE]") {
		t.Fatalf("output=%s error=%v callbacks=%d", output, err, calls)
	}
}
func TestClineSSEPassesNormalDataAndBoundsMemory(t *testing.T) {
	input := "data: {\"choices\":[{\"delta\":{\"content\":\"error is normal text\"}}]}\n\ndata: [DONE]\n\n"
	r := GuardSSEBody(io.NopCloser(strings.NewReader(input)), 1024, nil)
	closeClineTestBody(t, r)
	output, err := io.ReadAll(r)
	if err != nil || string(output) != input {
		t.Fatalf("%s %v", output, err)
	}
	r = GuardSSEBody(io.NopCloser(strings.NewReader(strings.Repeat("x", 100))), 32, nil)
	closeClineTestBody(t, r)
	if _, err = io.ReadAll(r); err == nil {
		t.Fatal("unbounded line accepted")
	}
}

// Capture each body by value so reassigning the reader does not leak it.
func closeClineTestBody(t *testing.T, body io.Closer) {
	t.Helper()
	t.Cleanup(func() {
		if err := body.Close(); err != nil {
			t.Errorf("close SSE body: %v", err)
		}
	})
}

type clineCloseTestBody struct {
	io.Reader
	closeErr   error
	closeCalls int
}

func (b *clineCloseTestBody) Close() error {
	b.closeCalls++
	return b.closeErr
}

func TestClineSSEClosePropagatesSourceError(t *testing.T) {
	want := errors.New("source close failed")
	source := &clineCloseTestBody{Reader: strings.NewReader(""), closeErr: want}
	reader := GuardSSEBody(source, 0, nil)
	if err := reader.Close(); !errors.Is(err, want) {
		t.Fatalf("close error = %v, want %v", err, want)
	}
	if source.closeCalls != 1 {
		t.Fatalf("close calls = %d, want 1", source.closeCalls)
	}
}

func TestClineSSEFailureIsStickyAcrossSmallReads(t *testing.T) {
	input := "data: {\"choices\":[{\"finish_reason\":\"error\"}]}\n\ndata: [DONE]\n\n"
	calls := 0
	reader := GuardSSEBody(io.NopCloser(strings.NewReader(input)), 1024, func([]byte) { calls++ })
	closeClineTestBody(t, reader)
	buf := make([]byte, 1)
	for attempt := 0; attempt < 3; attempt++ {
		if n, err := reader.Read(buf); n != 0 || !errors.Is(err, ErrStreamFailure) {
			t.Fatalf("read %d: n=%d err=%v", attempt, n, err)
		}
	}
	if calls != 1 {
		t.Fatalf("error callbacks = %d, want 1", calls)
	}
}
