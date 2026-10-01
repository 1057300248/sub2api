package cline

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
)

var ErrJSONBodyTooLarge = errors.New("cline JSON response exceeds configured limit")
var ErrInvalidJSONBody = errors.New("cline upstream returned an invalid JSON response")
var ErrExpectedSSE = errors.New("cline streaming request returned a non-stream response")

// GuardJSONBody checks a bounded complete JSON response before exposing bytes.
// expectSSE is true when a streaming request unexpectedly returned JSON; even
// non-error JSON must then fail instead of masquerading as an empty SSE success.
func GuardJSONBody(body io.ReadCloser, maxBytes int64, expectSSE bool, onError func([]byte)) io.ReadCloser {
	if maxBytes <= 0 {
		maxBytes = 16 << 20
	}
	return &guardedJSONBody{source: body, maxBytes: maxBytes, expectSSE: expectSSE, onError: onError}
}

type guardedJSONBody struct {
	source    io.ReadCloser
	maxBytes  int64
	expectSSE bool
	onError   func([]byte)
	loaded    bool
	pending   []byte
	terminal  error
}

func (b *guardedJSONBody) Close() error { return b.source.Close() }
func (b *guardedJSONBody) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if !b.loaded {
		b.loaded = true
		// Avoid maxBytes+1 overflow for a caller-provided upper bound.
		limited := &io.LimitedReader{R: b.source, N: b.maxBytes}
		payload, err := io.ReadAll(limited)
		if err == nil && limited.N == 0 {
			var extra [1]byte
			n, probeErr := io.ReadFull(b.source, extra[:])
			if n > 0 {
				err = ErrJSONBodyTooLarge
			} else if probeErr != nil && !errors.Is(probeErr, io.EOF) {
				err = probeErr
			}
		}
		if err != nil {
			b.terminal = err
		} else if HasGenerationError(payload) {
			b.terminal = ErrStreamFailure
			if b.onError != nil {
				b.onError(payload)
			}
		} else if !json.Valid(payload) || bytes.TrimSpace(payload)[0] != '{' {
			b.terminal = ErrInvalidJSONBody
		} else if b.expectSSE {
			b.terminal = ErrExpectedSSE
		} else {
			b.pending, b.terminal = payload, io.EOF
		}
	}
	if len(b.pending) > 0 {
		n := copy(p, b.pending)
		b.pending = b.pending[n:]
		return n, nil
	}
	return 0, b.terminal
}
