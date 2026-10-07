package repository

import (
	"context"
	"fmt"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// Keyset pages bound memory, connections and upstream work. Do not use the
// general schedulable query: its cooldown filters would prevent recovery.
func (r *accountRepository) ListClineMetadataCandidates(ctx context.Context, after int64, limit int) ([]service.Account, error) {
	if limit < 1 || limit > 100 || after < 0 {
		return nil, fmt.Errorf("invalid Cline metadata page")
	}
	rows, err := clientFromContext(ctx, r.client).QueryContext(ctx, `SELECT id FROM accounts
 WHERE id>$1 AND platform='cline' AND type='apikey' AND status='active' AND schedulable
 AND credentials->>'account_mode'='pass' AND deleted_at IS NULL
 AND NOT(COALESCE(auto_pause_on_expired,false) AND expires_at IS NOT NULL AND expires_at<=NOW())
 ORDER BY id LIMIT $2`, after, limit)
	if err != nil {
		return nil, err
	}
	ids := make([]int64, 0, limit)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	closeErr := rows.Close()
	if err != nil {
		return nil, err
	}
	if closeErr != nil {
		return nil, closeErr
	}
	if len(ids) == 0 {
		return []service.Account{}, nil
	}
	accounts, err := r.GetByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	byID := make(map[int64]*service.Account, len(accounts))
	for _, a := range accounts {
		if a != nil {
			byID[a.ID] = a
		}
	}
	out := make([]service.Account, 0, len(ids))
	for _, id := range ids {
		if a := byID[id]; a != nil {
			out = append(out, *a)
		}
	}
	return out, nil
}
