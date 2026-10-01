package cline

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
)

var ErrSSEFrameTooLarge = errors.New("cline SSE event exceeds configured limit")
var ErrInvalidSSEFrame = errors.New("cline upstream returned an invalid SSE event")

// GuardSSEBody validates a complete event before exposing it to line-oriented
// gateway readers. Both lines and accumulated events are bounded by maxLine.
// CR, LF and CRLF are accepted; multiline JSON data is compacted to one data
// line for the shared Chat/Responses/Messages readers. No requests are replayed.
func GuardSSEBody(body io.ReadCloser, maxLine int, onError func([]byte)) io.ReadCloser {
	if maxLine <= 0 {
		maxLine = 1 << 20
	}
	return &guardedSSEBody{source: body, reader: bufio.NewReaderSize(body, 16<<10), maxLine: maxLine, onError: onError, firstLine: true}
}

type guardedSSEBody struct {
	source    io.ReadCloser
	reader    *bufio.Reader
	pending   []byte
	terminal  error
	maxLine   int
	onError   func([]byte)
	firstLine bool
	skipLF    bool
	sawDone   bool
}

func (b *guardedSSEBody) Close() error { return b.source.Close() }
func (b *guardedSSEBody) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if len(b.pending) == 0 && b.terminal == nil {
		b.pending, b.terminal = b.readEvent()
	}
	if len(b.pending) > 0 {
		n := copy(p, b.pending)
		b.pending = b.pending[n:]
		return n, nil
	}
	return 0, b.terminal
}

// A CR finishes a line immediately. Consume an optional following LF on the
// next read rather than waiting for another byte before releasing a CR event.
func (b *guardedSSEBody) readLine() ([]byte, error) {
	var line []byte
	for {
		ch, err := b.reader.ReadByte()
		if err != nil {
			return line, err
		}
		if b.skipLF {
			b.skipLF = false
			if ch == '\n' {
				continue
			}
		}
		if ch == '\r' || ch == '\n' {
			b.skipLF = ch == '\r'
			return line, nil
		}
		if len(line) >= b.maxLine {
			return nil, ErrSSEFrameTooLarge
		}
		line = append(line, ch)
	}
}

func (b *guardedSSEBody) readEvent() ([]byte, error) {
	var raw, metadata, data []byte
	var eventType string
	dataLines, size := 0, 0
	for {
		line, readErr := b.readLine()
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return nil, readErr
		}
		size += len(line) + 1
		if size > b.maxLine {
			return nil, ErrSSEFrameTooLarge
		}
		if b.firstLine {
			b.firstLine = false
			line = bytes.TrimPrefix(line, []byte{0xef, 0xbb, 0xbf})
		}
		if len(line) > 0 {
			raw = append(raw, line...)
			raw = append(raw, '\n')
			field, value, _ := bytes.Cut(line, []byte(":"))
			value = bytes.TrimPrefix(value, []byte(" "))
			switch string(field) {
			case "data":
				if dataLines > 0 {
					data = append(data, '\n')
				}
				data = append(data, value...)
				dataLines++
			case "event":
				eventType = string(value)
			}
			if string(field) != "data" {
				metadata = append(metadata, line...)
				metadata = append(metadata, '\n')
			}
		}
		if len(line) != 0 && readErr == nil {
			continue
		}

		// Only an explicit named error turns arbitrary event data into an error
		// envelope. A null payload is still a named failure, not success.
		payload := data
		namedError := eventType == "error" || eventType == "response.failed"
		if namedError && !HasGenerationError(payload) {
			encoded := json.RawMessage(payload)
			if !json.Valid(encoded) {
				quoted, err := json.Marshal(string(payload))
				if err != nil {
					return nil, ErrInvalidSSEFrame
				}
				encoded = quoted
			}
			wrapped, err := json.Marshal(struct {
				Type  string          `json:"type"`
				Error json.RawMessage `json:"error"`
			}{Type: "error", Error: encoded})
			if err != nil {
				return nil, ErrInvalidSSEFrame
			}
			payload = wrapped
		}
		if namedError || HasGenerationError(payload) {
			if b.onError != nil {
				b.onError(payload)
			}
			return failureUsageEvent(data), ErrStreamFailure
		}
		if readErr != nil {
			if !b.sawDone || dataLines > 0 || eventType != "" {
				return nil, io.ErrUnexpectedEOF
			}
			return raw, io.EOF
		}
		if dataLines > 0 {
			trimmed := bytes.TrimSpace(data)
			if len(trimmed) > 0 && !bytes.Equal(trimmed, []byte("[DONE]")) && (!json.Valid(trimmed) || trimmed[0] != '{') {
				return nil, ErrInvalidSSEFrame
			}
			if bytes.Equal(trimmed, []byte("[DONE]")) {
				b.sawDone = true
			}
			if dataLines > 1 && len(trimmed) > 0 {
				var compact bytes.Buffer
				if bytes.Equal(trimmed, []byte("[DONE]")) {
					if _, err := compact.Write(trimmed); err != nil {
						return nil, err
					}
				} else if err := json.Compact(&compact, data); err != nil {
					return nil, ErrInvalidSSEFrame
				}
				raw = append(metadata, "data: "...)
				raw = append(raw, compact.Bytes()...)
				raw = append(raw, '\n')
			}
		}
		if b.sawDone {
			return append(raw, '\n'), io.EOF
		}
		return append(raw, '\n'), nil
	}
}
