package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCustomPatchLegacyCookieJarPrivacy(t *testing.T) {
	const key = "codex_cookie_jar"
	jar := map[string]any{"cookies": []string{"__cflb=synthetic-legacy-secret"}}
	extra := map[string]any{key: jar, "public_setting": true}
	for _, candidate := range []string{key, " codex_cookie_jar "} {
		require.True(t, IsOpenAICodexTicketExtraKey(candidate))
		require.True(t, IsOpenAICodexTicketPrivateExtraKey(candidate))
	}
	redacted := RedactOpenAICodexTicketExtra(extra)
	require.NotContains(t, redacted, key)
	require.Equal(t, true, redacted["public_setting"])
	account := ticketTestAccount(293)
	account.Extra = extra
	readable := string(accountReadableSnapshotJSON(account))
	require.NotContains(t, readable, key)
	require.NotContains(t, readable, "synthetic-legacy-secret")
	require.Contains(t, readable, "public_setting")
	require.Equal(t, jar, extra[key], "redaction must not mutate persisted data")
}

func TestCustomPatchLegacyCookieJarWriteProtection(t *testing.T) {
	const key = "codex_cookie_jar"
	stored := map[string]any{"cookies": []string{"__cflb=synthetic-stored"}}
	for _, edit := range []map[string]any{
		{"custom": true},
		{"custom": true, key: map[string]any{"cookies": "spoofed"}},
	} {
		merged := MergeOpenAICodexTicketExtra(edit, map[string]any{key: stored})
		require.Equal(t, stored, merged[key])
		require.Equal(t, true, merged["custom"])
		require.NotContains(t, MergeOpenAICodexTicketExtra(edit, nil), key)
	}
	created, err := buildAccountForCreate(&CreateAccountInput{Platform: PlatformOpenAI, Type: AccountTypeOAuth}, map[string]any{key: stored, "custom": true})
	require.NoError(t, err)
	require.NotContains(t, created.Extra, key)
	require.Equal(t, true, created.Extra["custom"])
	account := ticketTestAccount(294)
	account.Extra = map[string]any{key: stored}
	repo := &upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{account.ID: account}}
	svc := &adminServiceImpl{accountRepo: repo}
	updated, err := svc.UpdateAccount(context.Background(), account.ID, &UpdateAccountInput{Extra: map[string]any{"custom": true, key: "spoofed"}})
	require.NoError(t, err)
	require.Equal(t, stored, updated.Extra[key])
	require.NoError(t, svc.UpdateAccountExtra(context.Background(), account.ID, map[string]any{key: "spoofed-again"}))
	require.Equal(t, stored, repo.accounts[account.ID].Extra[key])
}
