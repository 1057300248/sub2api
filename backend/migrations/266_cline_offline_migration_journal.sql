-- Private rollback journal: contains existing account credentials, like accounts.
-- Never expose this table through account DTOs, logs, public exports or HTTP APIs.
CREATE TABLE IF NOT EXISTS cline_migration_journal (
    plan_id TEXT PRIMARY KEY CHECK (plan_id ~ '^[0-9a-f]{64}$'),
    account_id BIGINT NOT NULL REFERENCES accounts(id) ON DELETE RESTRICT,
    plan JSONB NOT NULL,
    before_state JSONB NOT NULL,
    after_digest TEXT NOT NULL CHECK (after_digest ~ '^[0-9a-f]{64}$'),
    applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    rolled_back_at TIMESTAMPTZ
);
CREATE UNIQUE INDEX IF NOT EXISTS cline_migration_active_account_idx
ON cline_migration_journal(account_id) WHERE rolled_back_at IS NULL;
