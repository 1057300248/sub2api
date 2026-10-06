package cline

import (
	"errors"
	"strings"
	"unicode"
)

// BearerToken formats only the explicitly selected credential kind. It never
// guesses from a JWT payload, contacts an auth endpoint, or refreshes a token.
// Keep saved credentials untouched so in-flight credential CAS remains valid.
func BearerToken(kind, value string) (string, error) {
	value = strings.TrimSpace(value)
	bad := errors.New("invalid Cline credential")
	if value == "" || len(value) > 8192 || strings.IndexFunc(value, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 {
		return "", bad
	}
	switch kind {
	case "", AuthAPIKey:
		return value, nil
	case AuthAccountToken:
		token := strings.TrimPrefix(value, "workos:")
		if token == "" || strings.HasPrefix(token, "workos:") || len(token)+len("workos:") > 8192 {
			return "", bad
		}
		return "workos:" + token, nil
	default:
		return "", bad
	}
}
