package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/pkg/cline"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

// The account update, shared-subject limit and scheduler outbox insert are one
// SQL statement. An outbox failure rolls back the observation instead of leaving
// workers with conflicting scheduling state. No stale full Extra is written.
func (r *accountRepository) SetClineRateLimitIfLater(ctx context.Context, account *service.Account, scope string, until time.Time, reason string, authoritative bool) error {
	if account == nil || !account.IsCline() || !cline.ValidScope(scope) {
		return fmt.Errorf("invalid cline limit scope")
	}
	now := time.Now().UTC()
	if !until.After(now) || until.Sub(now) > 36*24*time.Hour || len(reason) > 100 {
		return fmt.Errorf("invalid cline limit reset")
	}
	credentials, err := json.Marshal(account.Credentials)
	if err != nil {
		return err
	}
	key := service.ClineRateLimitScope(account, scope)
	payload, err := json.Marshal(map[string]any{
		"rate_limited_at": now.Format(time.RFC3339), "rate_limit_reset_at": until.UTC().Format(time.RFC3339),
		"reset_unix": until.Unix(), "reason": reason, "reset_authoritative": authoritative,
	})
	if err != nil {
		return err
	}
	query := `WITH changed AS (
 UPDATE accounts SET extra=jsonb_set(
 jsonb_set(COALESCE(extra,'{}'::jsonb),'{model_rate_limits}',
 CASE WHEN jsonb_typeof(extra->'model_rate_limits')='object' THEN extra->'model_rate_limits' ELSE '{}'::jsonb END,true),
 ARRAY['model_rate_limits',$1]::text[],
 CASE WHEN CASE WHEN jsonb_typeof(extra#>ARRAY['model_rate_limits',$1,'reset_unix'])='number'
 THEN (extra#>>ARRAY['model_rate_limits',$1,'reset_unix'])::numeric >= $3 ELSE false END
 THEN extra#>ARRAY['model_rate_limits',$1] ELSE $2::jsonb END,true),updated_at=NOW()
 WHERE id=$4 AND platform='cline' AND credentials=$5::jsonb AND deleted_at IS NULL
 RETURNING id, extra
 ), shared AS (
 INSERT INTO cline_shared_limits(subject_hash,account_mode,scope,reset_at,reason,reset_authoritative)
 SELECT extra#>>'{cline_state,identity}', $6, $7, $8, $9, $10 FROM changed
 WHERE extra#>>'{cline_state,identity}' ~ '^[0-9a-f]{64}$'
 AND extra#>>'{cline_state,credential_fingerprint}'=$11
 AND extra#>>'{cline_state,identity}'=$13 AND $13::text<>''
 ON CONFLICT(subject_hash,account_mode,scope) DO UPDATE SET
 reset_at=GREATEST(cline_shared_limits.reset_at,EXCLUDED.reset_at),
 reason=CASE WHEN EXCLUDED.reset_at>=cline_shared_limits.reset_at THEN EXCLUDED.reason ELSE cline_shared_limits.reason END,
 reset_authoritative=CASE WHEN EXCLUDED.reset_at>cline_shared_limits.reset_at THEN EXCLUDED.reset_authoritative
 WHEN EXCLUDED.reset_at=cline_shared_limits.reset_at THEN cline_shared_limits.reset_authoritative OR EXCLUDED.reset_authoritative
 ELSE cline_shared_limits.reset_authoritative END,observed_at=NOW()
 RETURNING subject_hash
 ) INSERT INTO scheduler_outbox(event_type,account_id) SELECT $12,id FROM changed`
	result, err := clientFromContext(ctx, r.client).ExecContext(ctx, query, key, string(payload), until.Unix(), account.ID, string(credentials), account.GetClineMode(), scope, until.UTC(), reason, authoritative, service.ClineCredentialFingerprint(account), service.SchedulerOutboxEventAccountChanged, clineCapturedSubject(account))
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil || affected == 0 {
		return err
	}
	r.syncCommittedClineSnapshot(ctx, account.ID)
	return nil
}

// SaveClineStateIfUnchanged rejects credential rotation and out-of-order refresh
// results. Learning a subject also projects already-observed local cooldowns to
// that subject: refreshing a second key cannot erase a first key's known limit.
func (r *accountRepository) SaveClineStateIfUnchanged(ctx context.Context, account *service.Account, state *service.ClineState) (bool, error) {
	if account == nil || !account.IsCline() || state == nil || state.FetchedAt == nil || state.CredentialFingerprint != service.ClineCredentialFingerprint(account) || (state.Identity != "" && !cline.ValidSubjectHash(state.Identity)) {
		return false, fmt.Errorf("invalid cline state")
	}
	credentials, err := json.Marshal(account.Credentials)
	if err != nil {
		return false, err
	}
	copyState := *state
	copyState.ObservationUnixMS = state.FetchedAt.UnixMilli()
	payload, err := json.Marshal(&copyState)
	if err != nil {
		return false, err
	}
	query := `WITH previous AS (SELECT id,extra#>>'{cline_state,identity}' AS previous_identity FROM accounts WHERE id=$2 FOR UPDATE), changed AS (
 UPDATE accounts SET extra=jsonb_set(COALESCE(extra,'{}'::jsonb),'{cline_state}',$1::jsonb,true),updated_at=NOW()
 FROM previous WHERE accounts.id=$2 AND accounts.id=previous.id AND platform='cline' AND credentials=$3::jsonb AND deleted_at IS NULL
 AND CASE WHEN jsonb_typeof(extra#>'{cline_state,observation_unix_ms}')='number'
 THEN (extra#>>'{cline_state,observation_unix_ms}')::numeric <= $4 ELSE true END
 RETURNING accounts.id,extra,previous.previous_identity
 ), observed AS (
 SELECT $5::text AS subject_hash,$6::text AS account_mode,'cline:'||substr(e.key,length($7::text)+1) AS scope,
 CASE WHEN jsonb_typeof(e.value->'reset_unix')='number' THEN (e.value->>'reset_unix')::numeric ELSE 0 END AS reset_unix,
 COALESCE(e.value->>'reason','observed_limit') AS reason, e.value->'reset_authoritative'='true'::jsonb AS authoritative
 FROM changed CROSS JOIN LATERAL jsonb_each(CASE WHEN jsonb_typeof(extra->'model_rate_limits')='object' THEN extra->'model_rate_limits' ELSE '{}'::jsonb END) e
 WHERE $5::text ~ '^[0-9a-f]{64}$' AND (previous_identity IS NULL OR previous_identity='' OR previous_identity=$5) AND left(e.key,length($7::text))=$7
 ), shared AS (
 INSERT INTO cline_shared_limits(subject_hash,account_mode,scope,reset_at,reason,reset_authoritative)
 SELECT subject_hash,account_mode,scope,to_timestamp(reset_unix::double precision),left(reason,100),COALESCE(authoritative,false)
 FROM observed WHERE reset_unix>extract(epoch FROM NOW()) AND reset_unix<extract(epoch FROM NOW()+interval '36 days') AND length(scope)<=300 ORDER BY subject_hash,account_mode,scope
 ON CONFLICT(subject_hash,account_mode,scope) DO UPDATE SET
 reset_at=GREATEST(cline_shared_limits.reset_at,EXCLUDED.reset_at),observed_at=NOW(),
 reset_authoritative=CASE WHEN EXCLUDED.reset_at>cline_shared_limits.reset_at THEN EXCLUDED.reset_authoritative
 WHEN EXCLUDED.reset_at=cline_shared_limits.reset_at THEN cline_shared_limits.reset_authoritative OR EXCLUDED.reset_authoritative ELSE cline_shared_limits.reset_authoritative END
 RETURNING subject_hash
 ) INSERT INTO scheduler_outbox(event_type,account_id) SELECT $8,id FROM changed`
	prefix := "cline:" + service.ClineCredentialFingerprint(account) + ":"
	result, err := clientFromContext(ctx, r.client).ExecContext(ctx, query, string(payload), account.ID, string(credentials), copyState.ObservationUnixMS, state.Identity, account.GetClineMode(), prefix, service.SchedulerOutboxEventAccountChanged)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil || affected == 0 {
		return false, err
	}
	r.syncCommittedClineSnapshot(ctx, account.ID)
	return true, nil
}

func (r *accountRepository) syncCommittedClineSnapshot(ctx context.Context, id int64) {
	// The transactional outbox will be visible only after its owner commits.
	// Never publish an uncommitted snapshot from an enclosing transaction.
	if dbent.TxFromContext(ctx) == nil {
		r.syncSchedulerAccountSnapshot(ctx, id)
	}
}

func clineCapturedSubject(account *service.Account) string {
	if state := account.GetClineState(); state != nil && cline.ValidSubjectHash(state.Identity) {
		return state.Identity
	}
	return ""
}
