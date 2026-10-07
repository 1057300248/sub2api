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
		Success         *bool  `json:"success"`
		Data            *struct {
			ID              string `json:"id"`
			ActiveAccountID string `json:"active_account_id"`
			Organizations   []struct {
				Active         bool   `json:"active"`
				OrganizationID string `json:"organizationId"`
			} `json:"organizations"`
		} `json:"data"`
	}
	if json.Unmarshal(body, &p) != nil {
		return "", errors.New("unrecognized cline account identity")
	}

	// Older profile responses exposed the active account at the top level.
	subject := ""
	if p.ID != "" || p.ActiveAccountID != "" {
		if !validSubjectID(p.ID) || !validSubjectID(p.ActiveAccountID) {
			return "", errors.New("unrecognized cline account identity")
		}
		subject = p.ActiveAccountID
	} else {
		// The current API wraps /users/me in {success,data}. Personal accounts
		// use data.id; an active organization is the billing subject when one
		// is selected. Ambiguous active organizations fail closed.
		if p.Data == nil || p.Success == nil || !*p.Success || !validSubjectID(p.Data.ID) {
			return "", errors.New("unrecognized cline account identity")
		}
		subject = p.Data.ID
		if p.Data.ActiveAccountID != "" {
			if !validSubjectID(p.Data.ActiveAccountID) {
				return "", errors.New("unrecognized cline account identity")
			}
			subject = p.Data.ActiveAccountID
		} else {
			activeOrganization := ""
			for _, organization := range p.Data.Organizations {
				if !organization.Active {
					continue
				}
				if activeOrganization != "" || !validSubjectID(organization.OrganizationID) {
					return "", errors.New("unrecognized cline account identity")
				}
				activeOrganization = organization.OrganizationID
			}
			if activeOrganization != "" {
				subject = activeOrganization
			}
		}
	}
	if !validSubjectID(subject) {
		return "", errors.New("unrecognized cline account identity")
	}
	sum := sha256.Sum256([]byte(BaseURL + "\x00account\x00" + subject))
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
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
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
