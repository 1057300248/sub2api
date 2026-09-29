package dto

import (
	"encoding/json"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestCustomPatchLegacyCookieJarRedactedFromAccountDTO(t *testing.T) {
	source := &service.Account{ID: 295, Extra: map[string]any{
		"codex_cookie_jar": map[string]any{"cookies": []string{"__cflb=synthetic-private-cookie"}},
		"public_setting":   true,
	}}
	for _, mapped := range []*Account{AccountFromServiceShallow(source), AccountFromService(source)} {
		require.NotContains(t, mapped.Extra, "codex_cookie_jar")
		require.Equal(t, true, mapped.Extra["public_setting"])
		encoded, err := json.Marshal(mapped)
		require.NoError(t, err)
		require.NotContains(t, string(encoded), "synthetic-private-cookie")
	}
	require.Contains(t, source.Extra, "codex_cookie_jar", "API redaction must not delete stored data")
}
