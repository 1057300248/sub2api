package cline

import (
	"strings"
	"testing"
)

func TestBearerToken(t *testing.T) {
	for _, tc := range []struct {
		kind, value, want string
		invalid           bool
	}{
		{AuthAccountToken, "raw.fixture", "workos:raw.fixture", false},
		{AuthAccountToken, "workos:raw.fixture", "workos:raw.fixture", false},
		{AuthAPIKey, "api.fixture", "api.fixture", false},
		{AuthAPIKey, "workos:existing-key", "workos:existing-key", false},
		{AuthAccountToken, "workos:", "", true},
		{AuthAccountToken, "workos:workos:double", "", true},
		{AuthAccountToken, "Bearer fixture", "", true},
		{AuthAPIKey, "a\r\nInjected:b", "", true},
		{AuthAPIKey, "", "", true},
		{"oauth_guess", "fixture", "", true},
		{AuthAccountToken, strings.Repeat("x", 8192), "", true},
	} {
		got, err := BearerToken(tc.kind, tc.value)
		if (err != nil) != tc.invalid || got != tc.want {
			t.Errorf("kind=%s invalid=%v: format mismatch", tc.kind, tc.invalid)
		}
		if err != nil && strings.Contains(err.Error(), tc.value) && tc.value != "" {
			t.Error("error must not expose credential")
		}
	}
}
