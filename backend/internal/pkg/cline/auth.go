package cline

import (
	"errors"
	"strings"
	"unicode"
)

// WireCredential normalizes only the wire representation. Stored credentials and
// fingerprints stay untouched: adding the WorkOS routing prefix must not erase a
// historical cooldown. API keys never receive an account-token prefix.
func WireCredential(raw, kind string) (string, error) {
	key := strings.TrimSpace(raw)
	if key == "" || len(key) > 8192 || strings.IndexFunc(key, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 {
		return "", errors.New("invalid Cline credential")
	}
	if kind != "" && kind != AuthAPIKey && kind != AuthAccountToken {
		return "", errors.New("invalid Cline credential kind")
	}
	if kind != AuthAccountToken {
		return key, nil
	}
	if len(key) >= 7 && strings.EqualFold(key[:7], "workos:") {
		key = key[7:]
	}
	if key == "" || strings.Contains(key, ":") || len(key)+7 > 8192 {
		return "", errors.New("invalid Cline account token")
	}
	return "workos:" + key, nil
}
