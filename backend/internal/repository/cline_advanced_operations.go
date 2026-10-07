package repository

import (
	"context"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// Administrator local-budget reset is not a provider recovery signal. State,
// scoped limits, credentials, status, and all scheduler blocks remain untouched.
// The outbox and counter mutation commit or roll back together, including in an
// outer transaction. No model call or quota-metadata request is made.
func (r *accountRepository) ResetClineLocalQuota(ctx context.Context, id int64) error {
	result, err := clientFromContext(ctx, r.client).ExecContext(ctx, `WITH changed AS (
 UPDATE accounts SET extra=(COALESCE(extra,'{}'::jsonb)
 || '{"quota_used":0,"quota_daily_used":0,"quota_weekly_used":0}'::jsonb)
 - 'quota_daily_start' - 'quota_weekly_start' - 'quota_daily_reset_at' - 'quota_weekly_reset_at', updated_at=NOW()
 WHERE id=$1 AND platform='cline' AND deleted_at IS NULL RETURNING id
 ) INSERT INTO scheduler_outbox(event_type,account_id) SELECT $2,id FROM changed`, id, service.SchedulerOutboxEventAccountChanged)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		return service.ErrAccountNotFound
	}
	r.syncCommittedClineSnapshot(ctx, id)
	return nil
}

// Returns false for non-Cline rows so the caller retains the upstream behavior.
// In particular, this must never call the generic clear-model-limits operation.
func (r *accountRepository) ClearClineTemporaryPause(ctx context.Context, id int64) (bool, error) {
	result, err := clientFromContext(ctx, r.client).ExecContext(ctx, `WITH changed AS (
 UPDATE accounts SET temp_unschedulable_until=NULL,temp_unschedulable_reason=NULL,updated_at=NOW()
 WHERE id=$1 AND platform='cline' AND deleted_at IS NULL RETURNING id
 ) INSERT INTO scheduler_outbox(event_type,account_id) SELECT $2,id FROM changed`, id, service.SchedulerOutboxEventAccountChanged)
	if err != nil {
		return false, err
	}
	count, err := result.RowsAffected()
	if err != nil || count == 0 {
		return false, err
	}
	r.syncCommittedClineSnapshot(ctx, id)
	return true, nil
}
