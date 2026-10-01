package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// SetClineRateLimitIfLater is a database CAS. It neither rewrites a stale full
// Extra snapshot nor permits an older response to shorten a known cooldown.
// The credentials predicate also rejects responses from a rotated key/mode.
func (r *accountRepository) SetClineRateLimitIfLater(ctx context.Context, account *service.Account, scope string, until time.Time, reason string, authoritative bool) error {
	if account == nil || !account.IsCline() || !strings.HasPrefix(scope, "cline:") {
		return fmt.Errorf("invalid Cline limit scope")
	}
	now := time.Now()
	if !until.After(now) || until.Sub(now) > 36*24*time.Hour {
		return fmt.Errorf("invalid Cline limit reset")
	}
	credentials, err := json.Marshal(account.Credentials)
	if err != nil {
		return err
	}
	key := service.ClineRateLimitScope(account, scope)
	payload, err := json.Marshal(map[string]any{"rate_limited_at": now.UTC().Format(time.RFC3339), "rate_limit_reset_at": until.UTC().Format(time.RFC3339), "reset_unix": until.Unix(), "reason": reason, "reset_authoritative": authoritative})
	if err != nil {
		return err
	}
	query := `UPDATE accounts SET extra=jsonb_set(
 jsonb_set(COALESCE(extra,'{}'::jsonb),'{model_rate_limits}',
 CASE WHEN jsonb_typeof(extra->'model_rate_limits')='object' THEN extra->'model_rate_limits' ELSE '{}'::jsonb END,true),
 ARRAY['model_rate_limits',$1]::text[],
 CASE WHEN CASE WHEN jsonb_typeof(extra#>ARRAY['model_rate_limits',$1,'reset_unix'])='number'
 THEN (extra#>>ARRAY['model_rate_limits',$1,'reset_unix'])::numeric >= $3 ELSE false END
 THEN extra#>ARRAY['model_rate_limits',$1] ELSE $2::jsonb END,true),updated_at=NOW()
 WHERE id=$4 AND platform='cline' AND credentials=$5::jsonb AND deleted_at IS NULL`
	result, err := clientFromContext(ctx, r.client).ExecContext(ctx, query, key, string(payload), until.Unix(), account.ID, string(credentials))
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil || affected == 0 {
		return err
	}
	r.syncSchedulerAccountSnapshot(ctx, account.ID)
	return nil
}

func (r *accountRepository) SaveClineStateIfUnchanged(ctx context.Context, account *service.Account, state *service.ClineState) (bool, error) {
	if account == nil || !account.IsCline() || state == nil {
		return false, fmt.Errorf("invalid Cline state")
	}
	credentials, err := json.Marshal(account.Credentials)
	if err != nil {
		return false, err
	}
	payload, err := json.Marshal(state)
	if err != nil {
		return false, err
	}
	result, err := clientFromContext(ctx, r.client).ExecContext(ctx, `UPDATE accounts SET extra=jsonb_set(COALESCE(extra,'{}'::jsonb),'{cline_state}',$1::jsonb,true),updated_at=NOW() WHERE id=$2 AND platform='cline' AND credentials=$3::jsonb AND deleted_at IS NULL`, string(payload), account.ID, string(credentials))
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil || affected == 0 {
		return false, err
	}
	r.syncSchedulerAccountSnapshot(ctx, account.ID)
	return true, nil
}
