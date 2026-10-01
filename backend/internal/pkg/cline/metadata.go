package cline

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"unicode"
)

const ProfileURL = BaseURL + "/users/me"

// ParseSubjectHash accepts the documented /users/me contract. The active
// account, not the human user, owns the upstream billing/quota namespace.
// Missing account identity remains unknown; email/name are never retained.
func ParseSubjectHash(body []byte) (string, error) {
	if len(body) > MaxBodyBytes || HasGenerationError(body) {
		return "", errors.New("cline profile is too large")
	}
	var p struct {
		ID              string `json:"id"`
		ActiveAccountID string `json:"active_account_id"`
	}
	if json.Unmarshal(body, &p) != nil || !validSubjectID(p.ID) || !validSubjectID(p.ActiveAccountID) {
		return "", errors.New("unrecognized cline account identity")
	}
	sum := sha256.Sum256([]byte(BaseURL + "\x00account\x00" + p.ActiveAccountID))
	return hex.EncodeToString(sum[:]), nil
}

func validSubjectID(value string) bool {
	return value != "" && len(value) <= 256 && strings.IndexFunc(value, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsControl(r)
	}) < 0
}

func ValidSubjectHash(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, c := range value {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// ValidScope prevents provider text or a caller from manufacturing arbitrary
// quota-table keys. Free scopes are stored for observation, not API entitlement.
func ValidScope(scope string) bool {
	switch scope {
	case ScopeThrottle, ScopePayG, ScopePass + "five_hour", ScopePass + "weekly", ScopePass + "monthly", ScopePass + "unknown", ScopePass + "entitlement", ScopeFree + "unknown":
		return true
	default:
		return strings.HasPrefix(scope, ScopeFree) && ValidModelID(strings.TrimPrefix(scope, ScopeFree))
	}
}
