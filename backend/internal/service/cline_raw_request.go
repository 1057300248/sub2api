package service

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
)

// normalizeClineRawChatBody keeps canonical Chat fields authoritative without
// converting vendor extensions through a typed DTO. Repeated decoded top-level
// names are rejected, rather than letting first-key and last-key parsers disagree.
// Nested extension values are copied as RawMessage, without numeric coercion.
func normalizeClineRawChatBody(account *Account, body []byte) ([]byte, error) {
	if account == nil || (!account.IsCline() && !isLegacyClineAccount(account)) {
		return body, nil
	}
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 || trimmed[0] != '{' || !json.Valid(trimmed) {
		return nil, fmt.Errorf("cline Chat request must be a valid JSON object")
	}
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	if _, err := decoder.Token(); err != nil {
		return nil, fmt.Errorf("invalid Cline Chat request object")
	}
	fields := jsonStructFieldNames(apicompat.ChatCompletionsRequest{})
	seen := make(map[string]struct{})
	out := make([]byte, 0, len(trimmed))
	out = append(out, '{')
	changed, kept := false, false
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return nil, fmt.Errorf("invalid Cline Chat request field")
		}
		key, ok := token.(string)
		if !ok {
			return nil, fmt.Errorf("invalid Cline Chat request field")
		}
		if _, duplicate := seen[key]; duplicate {
			return nil, fmt.Errorf("duplicate top-level field in Cline Chat request")
		}
		seen[key] = struct{}{}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return nil, fmt.Errorf("invalid Cline Chat request value")
		}
		alias := false
		for _, field := range fields {
			if key != field && strings.EqualFold(key, field) {
				alias = true
				break
			}
		}
		if alias {
			changed = true
			continue
		}
		encodedKey, err := json.Marshal(key)
		if err != nil {
			return nil, fmt.Errorf("encode Cline Chat request field")
		}
		if kept {
			out = append(out, ',')
		}
		out = append(out, encodedKey...)
		out = append(out, ':')
		out = append(out, value...)
		kept = true
	}
	if _, err := decoder.Token(); err != nil {
		return nil, fmt.Errorf("invalid Cline Chat request object")
	}
	if !changed {
		return body, nil
	}
	return append(out, '}'), nil
}
