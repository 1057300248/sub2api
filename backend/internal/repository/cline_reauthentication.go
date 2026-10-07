package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// Update only the current credential-bound status, not the request's stale Extra.
// The same statement writes the outbox. Rotation and newer observations make a
// late 401 a no-op, while an in-flight older refresh cannot clear this status.
func (r *accountRepository) MarkClineReauthenticationRequired(ctx context.Context, account *service.Account, at time.Time) (bool, error) {
	if !account.IsClineAccountToken() || at.IsZero() {
		return false, fmt.Errorf("invalid Cline reauthentication observation")
	}
	credentials, err := json.Marshal(account.Credentials)
	if err != nil {
		return false, err
	}
	fp := service.ClineCredentialFingerprint(account)
	state, err := json.Marshal(map[string]any{
		"credential_status": "reauth_required", "credential_fingerprint": fp,
		"fetched_at": at.UTC(), "observation_unix_ms": at.UnixMilli(), "persisted": true,
		"error": "reauth_required", "next_refresh_at": at.Add(30 * time.Second).UTC(),
		"mode": account.GetClineMode(), "auth_type": "account_token", "transport": "chat_completions",
	})
	if err != nil {
		return false, err
	}
	result, err := clientFromContext(ctx, r.client).ExecContext(ctx, `WITH changed AS (
 UPDATE accounts SET extra=jsonb_set(COALESCE(extra,'{}'::jsonb),'{cline_state}',
 CASE WHEN extra#>>'{cline_state,credential_fingerprint}'=$4 THEN COALESCE(extra->'cline_state','{}'::jsonb) ELSE '{}'::jsonb END || $3::jsonb,true),updated_at=NOW()
 WHERE id=$1 AND platform='cline' AND credentials=$2::jsonb AND deleted_at IS NULL
 AND CASE WHEN jsonb_typeof(extra#>'{cline_state,observation_unix_ms}')='number'
 THEN (extra#>>'{cline_state,observation_unix_ms}')::numeric<=$5 ELSE true END
 RETURNING id
 ) INSERT INTO scheduler_outbox(event_type,account_id) SELECT $6,id FROM changed`, account.ID, string(credentials), string(state), fp, at.UnixMilli(), service.SchedulerOutboxEventAccountChanged)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	if err == nil && n > 0 {
		r.syncCommittedClineSnapshot(ctx, account.ID)
	}
	return n > 0, err
}
