//go:build integration

package clinemigration

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/cline"
	"github.com/stretchr/testify/require"
)

func migrationDB(t *testing.T) (*sql.DB, Spec) {
	t.Helper()
	dsn := os.Getenv("CLINE_MIGRATION_TEST_DSN")
	if dsn == "" {
		t.Skip("isolated migration PostgreSQL DSN not configured")
	}
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for {
		err = db.PingContext(ctx)
		if err == nil {
			break
		}
		if ctx.Err() != nil {
			t.Fatal("isolated PostgreSQL unavailable")
		}
		time.Sleep(100 * time.Millisecond)
	}
	schema := fmt.Sprintf("cline_migration_test_%d", time.Now().UnixNano())
	_, err = db.ExecContext(ctx, "CREATE SCHEMA "+schema)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, "SET search_path TO "+schema)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, err := db.ExecContext(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
		require.NoError(t, err)
		require.NoError(t, db.Close())
	})
	_, err = db.ExecContext(ctx, `CREATE TABLE accounts(id BIGINT PRIMARY KEY,name TEXT,platform TEXT,type TEXT,credentials JSONB,extra JSONB,schedulable BOOLEAN,rate_limit_reset_at TIMESTAMPTZ,updated_at TIMESTAMPTZ DEFAULT NOW(),last_used_at TIMESTAMPTZ,deleted_at TIMESTAMPTZ,rate_multiplier NUMERIC);
 CREATE TABLE groups(id BIGINT PRIMARY KEY,name TEXT,platform TEXT,deleted_at TIMESTAMPTZ);
 CREATE TABLE account_groups(account_id BIGINT REFERENCES accounts(id),group_id BIGINT REFERENCES groups(id),priority INTEGER,allowed_models JSONB,created_at TIMESTAMPTZ,PRIMARY KEY(account_id,group_id));
 CREATE TABLE api_keys(id BIGINT PRIMARY KEY,group_id BIGINT,key TEXT,allowed_models JSONB);
 CREATE TABLE user_allowed_groups(user_id BIGINT,group_id BIGINT,PRIMARY KEY(user_id,group_id));
 CREATE TABLE scheduler_outbox(id BIGSERIAL,event_type TEXT,account_id BIGINT);
 CREATE TABLE usage_logs(id BIGINT PRIMARY KEY,account_id BIGINT,cost NUMERIC);`)
	require.NoError(t, err)
	for _, name := range []string{"265_cline_credential_write_guard.sql", "266_cline_offline_migration_journal.sql"} {
		body, err := os.ReadFile("../../migrations/" + name)
		require.NoError(t, err)
		_, err = db.ExecContext(ctx, string(body))
		require.NoError(t, err)
	}
	_, err = db.ExecContext(ctx, `INSERT INTO accounts(id,name,platform,type,credentials,extra,schedulable,rate_multiplier) VALUES(1,'legacy','deepseek','apikey','{"api_key":"synthetic-legacy-key","base_url":"https://api.cline.bot/api/v1","model_mapping":{"alias":"vendor/model"}}','{"unrelated":"preserve"}',false,1.35);
 INSERT INTO groups VALUES(1,'legacy','deepseek',NULL),(2,'target','cline',NULL);
 INSERT INTO account_groups VALUES(1,1,17,'["alias"]','2026-10-01T00:00:00Z');
 INSERT INTO api_keys VALUES(91,1,'synthetic-customer-key','["alias"]');
 INSERT INTO user_allowed_groups VALUES(7,1);
 INSERT INTO usage_logs VALUES(21,1,12.34);`)
	require.NoError(t, err)
	return db, Spec{AccountID: 1, Mode: cline.ModePass, AuthType: cline.AuthAPIKey, ModelMapping: map[string]string{"alias": "cline-pass/model"}, GroupMoves: []GroupMove{{From: 1, To: 2}}}
}

func TestClineMigrationPostgresApplyRollbackAndIdempotency(t *testing.T) {
	db, spec := migrationDB(t)
	ctx := context.Background()
	plan, err := Preview(ctx, db, spec)
	require.NoError(t, err)
	body, err := json.Marshal(plan)
	require.NoError(t, err)
	require.NotContains(t, string(body), "synthetic-legacy-key")
	require.NotContains(t, string(body), "synthetic-customer-key")
	for i := 0; i < 2; i++ {
		require.NoError(t, Apply(ctx, db, *plan, plan.Approval, MaintenanceAcknowledgement))
	}
	var platform string
	var schedulable bool
	var rate string
	require.NoError(t, db.QueryRowContext(ctx, "SELECT platform,schedulable,rate_multiplier::text FROM accounts WHERE id=1").Scan(&platform, &schedulable, &rate))
	require.Equal(t, "cline", platform)
	require.False(t, schedulable)
	require.Equal(t, "1.35", rate)
	var group int64
	var priority int
	var models []byte
	require.NoError(t, db.QueryRowContext(ctx, "SELECT group_id,priority,allowed_models FROM account_groups WHERE account_id=1").Scan(&group, &priority, &models))
	require.Equal(t, int64(2), group)
	require.Equal(t, 17, priority)
	require.JSONEq(t, `["alias"]`, string(models))
	for i := 0; i < 2; i++ {
		require.NoError(t, Rollback(ctx, db, *plan, plan.Approval, MaintenanceAcknowledgement))
	}
	require.NoError(t, db.QueryRowContext(ctx, "SELECT platform FROM accounts WHERE id=1").Scan(&platform))
	require.Equal(t, "deepseek", platform)
	require.NoError(t, db.QueryRowContext(ctx, "SELECT group_id FROM account_groups WHERE account_id=1").Scan(&group))
	require.Equal(t, int64(1), group)
	require.NoError(t, db.QueryRowContext(ctx, "SELECT group_id FROM api_keys WHERE id=91").Scan(&group))
	require.Equal(t, int64(1), group)
	var cost string
	require.NoError(t, db.QueryRowContext(ctx, "SELECT cost::text FROM usage_logs WHERE id=21").Scan(&cost))
	require.Equal(t, "12.34", cost)
	var count int
	require.NoError(t, db.QueryRowContext(ctx, "SELECT COUNT(*) FROM user_allowed_groups WHERE user_id=7 AND group_id=1").Scan(&count))
	require.Equal(t, 1, count)
}

func TestClineMigrationPostgresRejectsDriftAndUnsafeRollback(t *testing.T) {
	db, spec := migrationDB(t)
	ctx := context.Background()
	plan, err := Preview(ctx, db, spec)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, "UPDATE api_keys SET allowed_models='[\"changed\"]' WHERE id=91")
	require.NoError(t, err)
	require.ErrorIs(t, Apply(ctx, db, *plan, plan.Approval, MaintenanceAcknowledgement), ErrConflict)
	plan, err = Preview(ctx, db, spec)
	require.NoError(t, err)
	require.NoError(t, Apply(ctx, db, *plan, plan.Approval, MaintenanceAcknowledgement))
	_, err = db.ExecContext(ctx, `UPDATE accounts SET credentials=credentials||'{"api_key":"manually-rotated"}'::jsonb WHERE id=1`)
	require.NoError(t, err)
	require.ErrorIs(t, Rollback(ctx, db, *plan, plan.Approval, MaintenanceAcknowledgement), ErrConflict)
	var key string
	require.NoError(t, db.QueryRowContext(ctx, "SELECT credentials->>'api_key' FROM accounts WHERE id=1").Scan(&key))
	require.Equal(t, "manually-rotated", key)
}

func TestClineMigrationPostgresFailureIsAtomic(t *testing.T) {
	db, spec := migrationDB(t)
	ctx := context.Background()
	plan, err := Preview(ctx, db, spec)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, "ALTER TABLE scheduler_outbox ADD CONSTRAINT reject_outbox CHECK (false) NOT VALID")
	require.NoError(t, err)
	require.Error(t, Apply(ctx, db, *plan, plan.Approval, MaintenanceAcknowledgement))
	var platform string
	require.NoError(t, db.QueryRowContext(ctx, "SELECT platform FROM accounts WHERE id=1").Scan(&platform))
	require.Equal(t, "deepseek", platform)
	var group, count int
	require.NoError(t, db.QueryRowContext(ctx, "SELECT group_id FROM account_groups WHERE account_id=1").Scan(&group))
	require.Equal(t, 1, group)
	require.NoError(t, db.QueryRowContext(ctx, "SELECT COUNT(*) FROM cline_migration_journal").Scan(&count))
	require.Zero(t, count)
}

func TestClineMigrationPostgresRequiresQuiescenceAndKeepsCooldown(t *testing.T) {
	db, spec := migrationDB(t)
	ctx := context.Background()
	until := time.Now().UTC().Add(3 * time.Hour).Truncate(time.Second)
	_, err := db.ExecContext(ctx, `UPDATE accounts SET extra=extra||jsonb_build_object('model_rate_limits',jsonb_build_object('legacy',jsonb_build_object('rate_limit_reset_at',$1::text))) WHERE id=1`, until.Format(time.RFC3339))
	require.NoError(t, err)
	plan, err := Preview(ctx, db, spec)
	require.NoError(t, err)
	other, err := sql.Open("postgres", os.Getenv("CLINE_MIGRATION_TEST_DSN"))
	require.NoError(t, err)
	require.NoError(t, other.PingContext(ctx))
	require.ErrorIs(t, Apply(ctx, db, *plan, plan.Approval, MaintenanceAcknowledgement), ErrMaintenance)
	require.NoError(t, other.Close())
	// The closed peer backend may need a moment to process its Terminate packet.
	require.Eventually(t, func() bool {
		var n int
		err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM pg_stat_activity WHERE datname=current_database() AND pid<>pg_backend_pid() AND backend_type='client backend'").Scan(&n)
		return err == nil && n == 0
	}, time.Second, 10*time.Millisecond)
	require.NoError(t, Apply(ctx, db, *plan, plan.Approval, MaintenanceAcknowledgement))
	require.NoError(t, Rollback(ctx, db, *plan, plan.Approval, MaintenanceAcknowledgement))
	var reset time.Time
	require.NoError(t, db.QueryRowContext(ctx, "SELECT rate_limit_reset_at FROM accounts WHERE id=1").Scan(&reset))
	require.True(t, reset.Equal(until))
	require.NotContains(t, strings.ToLower(plan.Warning), "automatic")
}
