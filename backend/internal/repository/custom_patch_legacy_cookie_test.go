package repository

import (
	"context"
	"testing"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/DATA-DOG/go-sqlmock"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestCustomPatchLegacyCookieJarUsesLockedDatabaseValue(t *testing.T) {
	for _, tt := range []struct {
		name string
		raw  string
		keep bool
	}{
		{"preserve latest database value", `{"codex_cookie_jar":{"cookies":"synthetic-latest"}}`, true},
		{"reject client injection", `{}`, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			t.Cleanup(func() { _ = db.Close() })
			client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
			t.Cleanup(func() { _ = client.Close() })
			mock.ExpectQuery(`(?s)SELECT.*FOR NO KEY UPDATE`).
				WithArgs(int64(296), service.PlatformOpenAI, service.AccountTypeOAuth, `{"access_token":"test"}`, nil).
				WillReturnRows(sqlmock.NewRows([]string{"identity_unchanged", "ollama_group_unchanged", "ollama_proxy_unchanged", "enabled", "rate_sync_enabled", "snapshot", "ollama_session", "ollama_auto", "ollama_snapshot", "opencode_group_unchanged", "opencode_auto", "opencode_snapshot", "current_extra"}).
					AddRow(true, false, true, nil, nil, nil, nil, nil, nil, false, nil, nil, []byte(tt.raw)))
			account := &service.Account{
				ID: 296, Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth,
				Credentials: map[string]any{"access_token": "test"},
				Extra:       map[string]any{"codex_cookie_jar": map[string]any{"cookies": "stale-or-spoofed"}, "public_setting": true},
			}
			extra, err := lockAndMergeAccountProbeExtra(context.Background(), client, account, nil, nil)
			require.NoError(t, err)
			if tt.keep {
				require.Equal(t, map[string]any{"cookies": "synthetic-latest"}, extra["codex_cookie_jar"])
			} else {
				require.NotContains(t, extra, "codex_cookie_jar")
			}
			require.Equal(t, true, extra["public_setting"])
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}
