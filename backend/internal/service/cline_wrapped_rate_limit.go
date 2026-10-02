package service

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/cline"
)

func isClineWrappedRateLimit(account *Account, status int, body []byte) bool {
	if account == nil || (!account.IsCline() && !isLegacyClineAccount(account)) ||
		(status != http.StatusTooManyRequests && status < http.StatusInternalServerError) {
		return false
	}
	_, ok := cline.WrappedRateLimitBody(body)
	return ok
}

// Legacy accounts have no verified shared subject. Keep this cooldown on the
// observed account only; never infer a pool-wide team identity from error text.
func (s *RateLimitService) handleLegacyClineWrappedRateLimit(ctx context.Context, account *Account, status int, headers http.Header, body []byte) bool {
	if !isLegacyClineAccount(account) || !isClineWrappedRateLimit(account, status, body) {
		return false
	}
	limit, ok := cline.Classify(status, headers, body, "", time.Now())
	if !ok {
		return false
	}
	until := limit.Until()
	s.notifyAccountSchedulingBlocked(account, until, "cline_wrapped_429")
	if err := setClineRateLimited(ctx, s.accountRepo, account.ID, until); err != nil {
		slog.Warn("cline.wrapped_limit_persist_failed", "account_id", account.ID, "upstream_status", status)
		return true
	}
	slog.Info("cline.wrapped_rate_limit", "account_id", account.ID, "upstream_status", status, "classified_status", http.StatusTooManyRequests, "reset_at", until)
	return true
}
