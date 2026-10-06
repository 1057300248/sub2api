package service

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

var ErrClineHeaderOverrides = infraerrors.BadRequest("INVALID_CLINE_HEADER_OVERRIDES", "header_overrides must contain unique permitted HTTP header names and bounded string values")

// Request-scoped diagnostic/client headers only. Credentials, tenant/routing
// selectors, hop-by-hop/framing and negotiated content headers remain gateway
// owned. A positive list avoids future provider authentication aliases silently
// becoming client-controlled. No values are logged or persisted on an account.
func permittedClineOverrideHeader(name string) bool {
	switch strings.ToLower(name) {
	case "user-agent", "accept-language", "x-request-id", "x-client-name", "x-client-version":
		return true
	default:
		return strings.HasPrefix(strings.ToLower(name), "x-metadata-")
	}
}

func clineHeaderNameValid(name string) bool {
	if len(name) == 0 || len(name) > 128 {
		return false
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("!#$%&'*+-.^_`|~", rune(c)) {
			continue
		}
		return false
	}
	return true
}

// extractClineHeaderOverrides consumes a gateway-only top-level extension.
// Token decoding rejects duplicate decoded keys (including escaped names) before
// map decoding could collapse them. The returned body never contains overrides.
func extractClineHeaderOverrides(account *Account, body []byte) ([]byte, http.Header, error) {
	if account == nil || (!account.IsCline() && !isLegacyClineAccount(account)) {
		return body, nil, nil
	}
	if !json.Valid(body) {
		return nil, nil, ErrClineHeaderOverrides
	}
	d := json.NewDecoder(bytes.NewReader(body))
	token, err := d.Token()
	if err != nil || token != json.Delim('{') {
		return nil, nil, ErrClineHeaderOverrides
	}
	var raw json.RawMessage
	found := false
	for d.More() {
		token, err = d.Token()
		if err != nil {
			return nil, nil, ErrClineHeaderOverrides
		}
		name, ok := token.(string)
		if !ok {
			return nil, nil, ErrClineHeaderOverrides
		}
		var value json.RawMessage
		if d.Decode(&value) != nil {
			return nil, nil, ErrClineHeaderOverrides
		}
		if strings.EqualFold(name, "header_overrides") {
			if found || name != "header_overrides" {
				return nil, nil, ErrClineHeaderOverrides
			}
			found, raw = true, value
		}
	}
	if !found {
		return body, nil, nil
	}
	if len(raw) > 16384 {
		return nil, nil, ErrClineHeaderOverrides
	}
	d = json.NewDecoder(bytes.NewReader(raw))
	token, err = d.Token()
	if err != nil || token != json.Delim('{') {
		return nil, nil, ErrClineHeaderOverrides
	}
	headers := make(http.Header)
	total := 0
	for d.More() {
		token, err = d.Token()
		if err != nil {
			return nil, nil, ErrClineHeaderOverrides
		}
		name, ok := token.(string)
		if !ok || !clineHeaderNameValid(name) || !permittedClineOverrideHeader(name) {
			return nil, nil, ErrClineHeaderOverrides
		}
		canonical := http.CanonicalHeaderKey(name)
		if _, duplicate := headers[canonical]; duplicate || len(headers) >= 16 {
			return nil, nil, ErrClineHeaderOverrides
		}
		var value any
		if d.Decode(&value) != nil {
			return nil, nil, ErrClineHeaderOverrides
		}
		text, ok := value.(string)
		if !ok || len(text) > 2048 {
			return nil, nil, ErrClineHeaderOverrides
		}
		for _, c := range text {
			if c < 32 || c == 127 {
				return nil, nil, ErrClineHeaderOverrides
			}
		}
		total += len(name) + len(text)
		if total > 8192 {
			return nil, nil, ErrClineHeaderOverrides
		}
		headers.Set(canonical, text)
	}
	if _, err = d.Token(); err != nil {
		return nil, nil, ErrClineHeaderOverrides
	}
	if err = d.Decode(new(any)); err != io.EOF {
		return nil, nil, ErrClineHeaderOverrides
	}
	// RawMessage retains numbers and nested vendor extensions without coercion.
	var fields map[string]json.RawMessage
	if json.Unmarshal(body, &fields) != nil {
		return nil, nil, ErrClineHeaderOverrides
	}
	delete(fields, "header_overrides")
	clean, err := json.Marshal(fields)
	if err != nil {
		return nil, nil, ErrClineHeaderOverrides
	}
	return clean, headers, nil
}
