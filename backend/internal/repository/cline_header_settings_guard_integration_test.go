//go:build integration

package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestClinePostgresHeaderGuardDatabaseContract(t *testing.T) {
	ctx := context.Background()
	repo, a := clinePostgresAccount(t)
	makeHeaders := func(count, size int, character string) map[string]any {
		headers := make(map[string]any, count)
		for i := 0; i < count; i++ {
			headers[fmt.Sprintf("X-Metadata-%02d", i)] = strings.Repeat(character, size)
		}
		return headers
	}
	cases := []struct {
		name string
		raw  any
		ok   bool
	}{
		{"empty", map[string]any{}, true},
		{"ordinary", map[string]any{"USER-AGENT": "fixture/2", "X-Metadata-Trace": "中文"}, true},
		{"sixteen", makeHeaders(16, 0, "x"), true},
		{"seventeen", makeHeaders(17, 0, "x"), false},
		{"max_name", map[string]any{"x-metadata-" + strings.Repeat("a", 117): "ok"}, true},
		{"long_name", map[string]any{"x-metadata-" + strings.Repeat("a", 118): "ok"}, false},
		{"max_value", makeHeaders(1, 2048, "x"), true},
		{"long_value", makeHeaders(1, 2049, "x"), false},
		{"unicode_bytes", makeHeaders(1, 683, "中"), false},
		{"combined_ok", makeHeaders(4, 2000, "x"), true},
		{"combined_long", makeHeaders(4, 2048, "x"), false},
		{"escaped_ok", makeHeaders(2, 1000, "<"), true},
		{"escaped_long", makeHeaders(4, 1000, "<"), false},
		{"escaped_unicode", makeHeaders(4, 670, "\u2028"), true},
		{"unicode_name", map[string]any{"X-Metadata-中": "ok"}, false},
		{"name_space", map[string]any{"User Agent": "ok"}, false},
		{"protected", map[string]any{"Authorization": "fixture-secret-marker"}, false},
		{"alias", map[string]any{"User-Agent": "a", "user-agent": "b"}, false},
		{"control", map[string]any{"X-Metadata-Trace": "bad\tvalue"}, false},
		{"del", map[string]any{"X-Metadata-Trace": "bad\x7fvalue"}, false},
		{"array", []string{"x"}, false},
		{"null", nil, false},
		{"numeric_value", map[string]any{"User-Agent": 7}, false},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			credentials := maps.Clone(a.Credentials)
			// Even disabled imported defaults must obey the contract.
			credentials["header_override_enabled"] = false
			credentials["header_overrides"] = test.raw
			goErr := service.NormalizeClineCredentials(a.Platform, a.Type, maps.Clone(credentials))
			body, err := json.Marshal(credentials)
			require.NoError(t, err)
			_, sqlErr := integrationDB.ExecContext(ctx, "UPDATE accounts SET credentials=$1::jsonb WHERE id=$2", string(body), a.ID)
			if test.ok {
				require.NoError(t, goErr)
				require.NoError(t, sqlErr)
				return
			}
			require.Error(t, goErr)
			require.ErrorContains(t, sqlErr, "invalid Cline header configuration")
			require.NotContains(t, sqlErr.Error(), "fixture-secret-marker")
			// Raw inserts cannot bypass the update guard either.
			_, err = integrationDB.ExecContext(ctx, "INSERT INTO accounts(name,platform,type,credentials) VALUES('invalid-header-fixture','cline','apikey',$1::jsonb)", string(body))
			require.ErrorContains(t, err, "invalid Cline header configuration")
		})
	}
	_, b := clinePostgresAccount(t)
	_, err := integrationDB.ExecContext(ctx, "UPDATE accounts SET platform='deepseek' WHERE id=$1", b.ID)
	require.NoError(t, err)
	nativeBefore, err := repo.GetByID(ctx, a.ID)
	require.NoError(t, err)
	otherBefore, err := repo.GetByID(ctx, b.ID)
	require.NoError(t, err)
	bad := map[string]any{"header_overrides": map[string]any{"Authorization": "other-platform-default"}}
	_, err = repo.BulkUpdate(ctx, []int64{b.ID, a.ID}, service.AccountBulkUpdate{Credentials: bad})
	require.Error(t, err)
	for _, before := range []*service.Account{nativeBefore, otherBefore} {
		after, err := repo.GetByID(ctx, before.ID)
		require.NoError(t, err)
		require.Equal(t, before.Credentials, after.Credentials, "mixed update must be atomic")
	}
	// Non-Cline defaults retain upstream policy, even for the same header name.
	_, err = repo.BulkUpdate(ctx, []int64{b.ID}, service.AccountBulkUpdate{Credentials: bad})
	require.NoError(t, err)
}
