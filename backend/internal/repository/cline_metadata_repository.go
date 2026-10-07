package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/cline"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

func (r *accountRepository) ClaimClineMetadataRefresh(ctx context.Context, account *service.Account, now time.Time) (bool, error) {
	if !account.IsCline() {
		return false, service.ErrClineMetadataUnavailable
	}
	credentials, err := json.Marshal(account.Credentials)
	if err != nil {
		return false, err
	}
	result, err := clientFromContext(ctx, r.client).ExecContext(ctx, `INSERT INTO cline_metadata_leases(account_id,credential_fingerprint,refresh_after)
 SELECT id,$1,$2 FROM accounts WHERE id=$3 AND platform='cline' AND credentials=$4::jsonb AND deleted_at IS NULL
 ON CONFLICT(account_id) DO UPDATE SET credential_fingerprint=EXCLUDED.credential_fingerprint,refresh_after=EXCLUDED.refresh_after
 WHERE cline_metadata_leases.refresh_after<=$5 OR cline_metadata_leases.credential_fingerprint<>EXCLUDED.credential_fingerprint`,
		service.ClineCredentialFingerprint(account), now.Add(30*time.Second), account.ID, string(credentials), now)
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	return rows > 0, err
}

// CheckClineAdmission reads the durable source, not a possibly stale Redis
// snapshot. A database/cache outage cannot silently bypass an observed cooldown.
// It is called only at the final Cline send boundary, after explicit model checks.
func (r *accountRepository) CheckClineAdmission(ctx context.Context, account *service.Account, upstreamModel string) error {
	if !account.IsCline() {
		return service.ErrClineAdmissionUnavailable
	}
	credentials, err := json.Marshal(account.Credentials)
	if err != nil {
		return err
	}
	client := clientFromContext(ctx, r.client)
	rows, err := client.QueryContext(ctx, `SELECT platform='cline' AND type='apikey' AND credentials=$1::jsonb,
 status='active' AND schedulable AND NOT(COALESCE(auto_pause_on_expired,false) AND expires_at IS NOT NULL AND expires_at<=NOW()),
 COALESCE(extra,'{}'::jsonb),GREATEST(rate_limit_reset_at,overload_until,temp_unschedulable_until)
 FROM accounts WHERE id=$2 AND deleted_at IS NULL`, string(credentials), account.ID)
	if err != nil {
		return err
	}
	var matches, active bool
	var extra []byte
	var blockedUntil sql.NullTime
	if !rows.Next() {
		err = rows.Err()
		_ = rows.Close()
		if err != nil {
			return err
		}
		return service.ErrClineMetadataChanged
	}
	err = rows.Scan(&matches, &active, &extra, &blockedUntil)
	closeErr := rows.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if !matches || !active {
		return service.ErrClineMetadataChanged
	}
	now := time.Now()
	if blockedUntil.Valid && blockedUntil.Time.After(now) {
		return service.ErrClineObservedCooldown
	}
	current := &service.Account{ID: account.ID, Platform: account.Platform, Type: account.Type, Credentials: account.Credentials}
	if json.Unmarshal(extra, &current.Extra) != nil {
		return service.ErrClineAdmissionUnavailable
	}
	if current.GetModelRateLimitRemainingTime(upstreamModel) > 0 {
		return service.ErrClineObservedCooldown
	}
	if err := service.ClineQuotaAdmission(current, now); err != nil {
		return err
	}
	if err := service.ValidateClineLocalQuotaSettings(current.Platform, current.Extra); err != nil {
		return service.ErrClineAdmissionUnavailable
	}
	if current.IsQuotaExceeded() {
		return service.ErrClineLocalQuotaExceeded
	}
	state := current.GetClineState()
	if state == nil || !cline.ValidSubjectHash(state.Identity) {
		if cline.IsOfficialBase(current.GetClineBaseURL()) {
			return service.ErrClineMetadataRequired
		}
		return nil
	}
	rows, err = client.QueryContext(ctx, `SELECT scope,reset_at FROM cline_shared_limits WHERE subject_hash=$1 AND account_mode=$2 AND scope=ANY($3)`, state.Identity, current.GetClineMode(), pq.Array(cline.RateLimitKeys(current.GetClineMode(), upstreamModel)))
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var scope string
		var until time.Time
		if err := rows.Scan(&scope, &until); err != nil {
			return err
		}
		if until.After(now) {
			return service.ErrClineObservedCooldown
		}
		if current.GetClineMode() == cline.ModePass && strings.HasPrefix(scope, cline.ScopePass) && cline.IsOfficialBase(current.GetClineBaseURL()) && !service.ClinePassWindowRecovered(state, scope, until, now) {
			return service.ErrClineMetadataRequired
		}
	}
	return rows.Err()
}
