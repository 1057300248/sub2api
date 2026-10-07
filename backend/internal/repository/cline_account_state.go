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
	copyState.QuotaBlocks = service.ClineQuotaBlocksForState(state)
	copyState.ObservationUnixMS = state.FetchedAt.UnixMilli()
	payload, err := json.Marshal(&copyState)
	if err != nil {
		return false, err
	}
	// Metadata exhaustion and successful per-window recovery share the same
	// transaction/outbox as the credential-bound observation. Unknown resets
	// use a non-authoritative retry boundary only; the UI keeps reset_at null.
	quotaLimits := make([]map[string]any, 0, len(copyState.QuotaBlocks))
	if account.GetClineMode() == cline.ModePass {
		for _, b := range copyState.QuotaBlocks {
			until, authoritative := state.FetchedAt.Add(5*time.Minute), false
			if b.ResetAt != nil {
				until, authoritative = *b.ResetAt, true
			}
			quotaLimits = append(quotaLimits, map[string]any{"scope": cline.ScopePass + b.Type, "reset_unix": until.Unix(), "authoritative": authoritative})
		}
	}
	quotaJSON, err := json.Marshal(quotaLimits)
	if err != nil {
		return false, err
	}
	healthy := make([]string, 0, 3)
	if state.CredentialStatus == "valid" && state.IdentityVerifiedAt != nil && state.IdentityVerifiedAt.Equal(*state.FetchedAt) && state.LastSuccessAt != nil && state.LastSuccessAt.Equal(*state.FetchedAt) {
		for _, name := range []string{"five_hour", "weekly", "monthly"} {
			if service.ClinePassWindowRecovered(state, cline.ScopePass+name, *state.FetchedAt, *state.FetchedAt) {
				healthy = append(healthy, cline.ScopePass+name)
			}
		}
	}
	healthyJSON, err := json.Marshal(healthy)
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
 ), quota_observed AS (
 SELECT $5::text AS subject_hash,$6::text AS account_mode,q->>'scope' AS scope,
 (q->>'reset_unix')::numeric AS reset_unix,'quota_metadata'::text AS reason,(q->>'authoritative')::boolean AS authoritative
 FROM changed CROSS JOIN LATERAL jsonb_array_elements($9::jsonb) q
 WHERE $5::text ~ '^[0-9a-f]{64}$' AND $6::text='pass'
 ), candidates AS (
 SELECT * FROM observed WHERE reset_unix>extract(epoch FROM NOW()) AND reset_unix<extract(epoch FROM NOW()+interval '36 days') AND length(scope)<=300
 UNION ALL SELECT * FROM quota_observed WHERE reset_unix<extract(epoch FROM NOW()+interval '36 days')
 ), aggregate_limits AS (
 SELECT subject_hash,account_mode,scope,MAX(reset_unix) AS reset_unix,
 CASE WHEN bool_or(reason<>'quota_metadata') THEN MAX(reason) FILTER(WHERE reason<>'quota_metadata') ELSE 'quota_metadata' END AS reason,
 bool_or(COALESCE(authoritative,false)) AS authoritative
 FROM candidates GROUP BY subject_hash,account_mode,scope
 ), cleared AS (
 DELETE FROM cline_shared_limits l USING changed
 WHERE l.subject_hash=$5 AND l.account_mode=$6 AND l.reason='quota_metadata'
 AND l.observed_at<=to_timestamp($4::double precision/1000)
 AND l.scope IN (SELECT jsonb_array_elements_text($10::jsonb))
 AND NOT EXISTS(SELECT 1 FROM aggregate_limits a WHERE a.scope=l.scope)
 RETURNING l.scope
 ), shared AS (
 INSERT INTO cline_shared_limits(subject_hash,account_mode,scope,reset_at,reason,reset_authoritative)
 SELECT subject_hash,account_mode,scope,to_timestamp(reset_unix::double precision),left(reason,100),COALESCE(authoritative,false)
 FROM aggregate_limits ORDER BY subject_hash,account_mode,scope
 ON CONFLICT(subject_hash,account_mode,scope) DO UPDATE SET
 reset_at=GREATEST(cline_shared_limits.reset_at,EXCLUDED.reset_at),observed_at=NOW(),
 reason=CASE WHEN cline_shared_limits.reason<>'quota_metadata' THEN cline_shared_limits.reason ELSE EXCLUDED.reason END,
 reset_authoritative=CASE WHEN EXCLUDED.reset_at>cline_shared_limits.reset_at THEN EXCLUDED.reset_authoritative
 WHEN EXCLUDED.reset_at=cline_shared_limits.reset_at THEN cline_shared_limits.reset_authoritative OR EXCLUDED.reset_authoritative ELSE cline_shared_limits.reset_authoritative END
 RETURNING subject_hash
 ), retry_lease AS (
 UPDATE cline_metadata_leases l SET refresh_after=GREATEST(l.refresh_after,$11::timestamptz)
 FROM changed WHERE l.account_id=changed.id AND l.credential_fingerprint=$12 AND $11::timestamptz IS NOT NULL
 RETURNING l.account_id
 ) INSERT INTO scheduler_outbox(event_type,account_id) SELECT $8,id FROM changed`
	prefix := "cline:" + service.ClineCredentialFingerprint(account) + ":"
	result, err := clientFromContext(ctx, r.client).ExecContext(ctx, query, string(payload), account.ID, string(credentials), copyState.ObservationUnixMS, state.Identity, account.GetClineMode(), prefix, service.SchedulerOutboxEventAccountChanged, string(quotaJSON), string(healthyJSON), state.MetadataRetryAt, state.CredentialFingerprint)
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
