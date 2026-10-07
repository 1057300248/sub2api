package service

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/cline"
)

// Compatibility accounts do not have a verified Cline subject. Keep limits
// account-local, preserve the administrator's status and never synthesize a
// shared identity or switch to paid inference after subscription exhaustion.
func (s *RateLimitService) handleLegacyCline429(ctx context.Context, account *Account, headers http.Header, body []byte) bool {
	if !isLegacyClineAccount(account) {
		return false
	}
	limit, handled := cline.Classify(http.StatusTooManyRequests, headers, body, "", time.Now())
	if !handled {
		return false
	}
	until := limit.Until()
	s.notifyAccountSchedulingBlocked(account, until, "cline_429")
	if err := setClineRateLimited(ctx, s.accountRepo, account.ID, until); err != nil {
		slog.WarnContext(ctx, "cline.legacy_limit_persist_failed", "account_id", account.ID, "kind", limit.Kind, "window", limit.Window, "reset_at", until)
		return true
	}
	slog.InfoContext(ctx, "cline.legacy_rate_limit", "account_id", account.ID, "kind", limit.Kind, "window", limit.Window, "reset_at", until, "reset_observed", limit.ResetAt != nil)
	return true
}
