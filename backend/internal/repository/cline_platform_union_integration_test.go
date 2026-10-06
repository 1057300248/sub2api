//go:build integration

package repository

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"io/fs"
	"net/url"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/Wei-Shaw/sub2api/migrations"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

// Every schema is reconstructed from the complete migration set at the named
// source baseline, not from production data. The two historical CHECK bodies
// are verified against their released checksums before executing them.
func TestClinePostgresPlatformUnionUpgrade(t *testing.T) {
	ctx := context.Background()
	pg, err := tcpostgres.Run(ctx, "postgres:18.6-alpine", tcpostgres.WithDatabase("union_admin"), tcpostgres.WithUsername("fixture"), tcpostgres.WithPassword("fixture-only"), tcpostgres.BasicWaitStrategies())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, pg.Terminate(context.Background())) })
	dsn, err := pg.ConnectionString(ctx, "sslmode=disable", "TimeZone=UTC")
	require.NoError(t, err)
	admin, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, admin.Close()) })
	files, err := fs.Glob(migrations.FS, "*.sql")
	require.NoError(t, err)
	for _, baseline := range []string{"fresh", "production_95bf4b1f", "custom_de6fc6df", "upstream_bc83ff9c"} {
		t.Run(baseline, func(t *testing.T) {
			name := "union_" + baseline
			_, err := admin.ExecContext(ctx, "CREATE DATABASE "+name)
			require.NoError(t, err)
			u, err := url.Parse(dsn)
			require.NoError(t, err)
			u.Path = "/" + name
			db, err := sql.Open("postgres", u.String())
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, db.Close()) })
			var version string
			require.NoError(t, db.QueryRowContext(ctx, "SHOW server_version").Scan(&version))
			require.True(t, strings.HasPrefix(version, "18.6"), version)
			oldFS := fstest.MapFS{}
			for _, file := range files {
				if file == "267_cline_typesafe_platform_union.sql" || file == "268_cline_header_settings_guard.sql" || file == "269_cline_advanced_settings_guard.sql" || file == "270_cline_attribution_headers.sql" {
					continue
				}
				clineMigration := strings.HasPrefix(file, "263_cline_") || strings.HasPrefix(file, "264_cline_") || strings.HasPrefix(file, "265_cline_") || strings.HasPrefix(file, "266_cline_")
				upstreamNew := file == "237_add_api_key_concurrency_limit.sql" || file == "241_add_payment_order_bonus_amount.sql" || file == "241_add_typesafe_platform.sql"
				if baseline == "custom_de6fc6df" && upstreamNew {
					continue
				}
				if baseline == "upstream_bc83ff9c" && clineMigration {
					continue
				}
				if baseline == "production_95bf4b1f" && (upstreamNew || clineMigration || file == "261_pelican_group_test_costs.sql" || file == "262_openai_oauth_reauth_engine.sql") {
					continue
				}
				body, err := fs.ReadFile(migrations.FS, file)
				require.NoError(t, err)
				text := string(body)
				switch file {
				case "241_add_typesafe_platform.sql":
					text = strings.ReplaceAll(text, "'opencode_go', 'cline', 'typesafe'", "'opencode_go', 'typesafe'")
					require.Equal(t, "b6559525bf8d0b5d7c617e7415944f0d1fae8c408e9131ace84139795f66dcd2", fmt.Sprintf("%x", sha256.Sum256([]byte(strings.TrimSpace(text)))))
				case "263_cline_platform.sql":
					text = strings.ReplaceAll(text, "'opencode_go','cline','typesafe'", "'opencode_go','cline'")
					require.Equal(t, "8bc186261b4cf61488888bf2f6e03dd2196db0cc466037afe9fa905a0bd6cbf7", fmt.Sprintf("%x", sha256.Sum256([]byte(strings.TrimSpace(text)))))
				}
				oldFS[file] = &fstest.MapFile{Data: []byte(text)}
			}
			if baseline == "fresh" {
				require.NoError(t, ApplyMigrations(ctx, db))
			} else {
				require.NoError(t, applyMigrationsFS(ctx, db, oldFS))
			}
			platform := "deepseek"
			if baseline == "custom_de6fc6df" {
				platform = "cline"
			}
			if baseline == "upstream_bc83ff9c" {
				platform = "typesafe"
			}
			var userID, groupID, accountID, keyID int64
			require.NoError(t, db.QueryRowContext(ctx, "INSERT INTO users(email,password_hash,balance) VALUES('union-fixture@example.invalid','fixture-only',37.25) RETURNING id").Scan(&userID))
			require.NoError(t, db.QueryRowContext(ctx, "INSERT INTO groups(name,platform) VALUES('union-fixture','composite') RETURNING id").Scan(&groupID))
			require.NoError(t, db.QueryRowContext(ctx, `INSERT INTO accounts(name,platform,type,credentials,schedulable) VALUES('legacy-fixture','deepseek','apikey','{"base_url":"https://api.cline.bot/api/v1","api_key":"fixture-only"}',false) RETURNING id`).Scan(&accountID))
			_, err = db.ExecContext(ctx, "INSERT INTO account_groups(account_id,group_id) VALUES($1,$2)", accountID, groupID)
			require.NoError(t, err)
			require.NoError(t, db.QueryRowContext(ctx, "INSERT INTO api_keys(user_id,key,name,group_id) VALUES($1,'fixture-not-a-real-key','fixture',$2) RETURNING id", userID, groupID).Scan(&keyID))
			_, err = db.ExecContext(ctx, "INSERT INTO user_platform_quotas(user_id,platform,daily_limit_usd,daily_usage_usd) VALUES($1,$2,20,3.5)", userID, platform)
			require.NoError(t, err)
			_, err = db.ExecContext(ctx, "INSERT INTO composite_model_routes(group_id,public_model,target_platform,upstream_model) VALUES($1,'existing',$2,'vendor/existing')", groupID, platform)
			require.NoError(t, err)
			var historicalChecksum string
			historicalFile := ""
			if baseline == "custom_de6fc6df" {
				historicalFile = "263_cline_platform.sql"
			}
			if baseline == "upstream_bc83ff9c" {
				historicalFile = "241_add_typesafe_platform.sql"
			}
			if historicalFile != "" {
				require.NoError(t, db.QueryRowContext(ctx, "SELECT checksum FROM schema_migrations WHERE filename=$1", historicalFile).Scan(&historicalChecksum))
			}
			for iteration := 0; iteration < 2; iteration++ {
				require.NoError(t, ApplyMigrations(ctx, db), "repeat startup %d", iteration)
			}
			var balance, used float64
			require.NoError(t, db.QueryRowContext(ctx, "SELECT balance FROM users WHERE id=$1", userID).Scan(&balance))
			assert.Equal(t, 37.25, balance)
			require.NoError(t, db.QueryRowContext(ctx, "SELECT daily_usage_usd FROM user_platform_quotas WHERE user_id=$1 AND platform=$2", userID, platform).Scan(&used))
			assert.Equal(t, 3.5, used)
			var preserved bool
			require.NoError(t, db.QueryRowContext(ctx, `SELECT a.platform='deepseek' AND NOT a.schedulable AND a.credentials->>'api_key'='fixture-only' AND k.group_id=ag.group_id FROM accounts a JOIN account_groups ag ON ag.account_id=a.id JOIN api_keys k ON k.id=$2 WHERE a.id=$1`, accountID, keyID).Scan(&preserved))
			assert.True(t, preserved, "no platform migration, credential change, scheduling enablement or key rebinding")
			for _, added := range []string{"cline", "typesafe"} {
				_, err = db.ExecContext(ctx, "INSERT INTO composite_model_routes(group_id,public_model,target_platform,upstream_model) VALUES($1,$2,$2,'vendor/new')", groupID, added)
				require.NoError(t, err)
				if added != platform {
					_, err = db.ExecContext(ctx, "INSERT INTO user_platform_quotas(user_id,platform) VALUES($1,$2)", userID, added)
					require.NoError(t, err)
				}
			}
			// New migrations must not enter a historical fixture before its upgrade:
			// otherwise 268 can overwrite 270 after 270 was incorrectly marked applied.
			_, err = db.ExecContext(ctx, `INSERT INTO accounts(name,platform,type,credentials) VALUES('attribution-upgrade-fixture','cline','apikey','{"api_key":"fixture-only","account_mode":"pass","cline_auth_type":"account_token","model_mapping":{"model":"cline-pass/model"},"header_override_enabled":true,"header_overrides":{"HTTP-Referer":"https://gateway.example.invalid","X-Title":"Upgrade fixture"}}'::jsonb)`)
			require.NoError(t, err, "latest attribution guard survives fresh and historical upgrades")
			var monitorCheck string
			require.NoError(t, db.QueryRowContext(ctx, "SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conname='channel_monitors_provider_check'").Scan(&monitorCheck))
			assert.Contains(t, monitorCheck, "'cline'")
			assert.NotContains(t, monitorCheck, "'typesafe'", "TypeSafe is not a monitor protocol")
			if historicalFile != "" {
				var after string
				require.NoError(t, db.QueryRowContext(ctx, "SELECT checksum FROM schema_migrations WHERE filename=$1", historicalFile).Scan(&after))
				assert.Equal(t, historicalChecksum, after, "historical migration evidence is not overwritten")
				_, err = db.ExecContext(ctx, "UPDATE schema_migrations SET checksum=$1 WHERE filename=$2", strings.Repeat("0", 64), historicalFile)
				require.NoError(t, err)
				require.ErrorContains(t, ApplyMigrations(ctx, db), "checksum mismatch", "unknown edits must still fail closed")
			}
		})
	}
}
