-- Additive Cline metadata/limit coordination; no legacy accounts are converted.
CREATE TABLE IF NOT EXISTS cline_metadata_leases (
    account_id BIGINT PRIMARY KEY REFERENCES accounts(id) ON DELETE CASCADE,
    credential_fingerprint TEXT NOT NULL CHECK (credential_fingerprint ~ '^[0-9a-f]{64}$'),
    refresh_after TIMESTAMPTZ NOT NULL
);

-- Only hashes of server-verified upstream active-account IDs are stored here.
-- Never key an organization quota by the human user's email or user ID.
CREATE TABLE IF NOT EXISTS cline_shared_limits (
    subject_hash TEXT NOT NULL CHECK (subject_hash ~ '^[0-9a-f]{64}$'),
    account_mode TEXT NOT NULL CHECK (account_mode IN ('pass', 'free', 'payg', 'unknown')),
    scope TEXT NOT NULL CHECK (length(scope) <= 300 AND scope LIKE 'cline:%'),
    reset_at TIMESTAMPTZ NOT NULL,
    reason TEXT NOT NULL,
    reset_authoritative BOOLEAN NOT NULL DEFAULT FALSE,
    observed_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (subject_hash, account_mode, scope)
);
CREATE INDEX IF NOT EXISTS cline_shared_limits_expiry_idx ON cline_shared_limits(reset_at);
