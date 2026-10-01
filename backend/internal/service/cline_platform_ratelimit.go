package service

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/cline"
)

type clineScopedRateLimitRepository interface {
	SetClineRateLimitIfLater(context.Context, *Account, string, time.Time, string, bool) error
}

func isClineScopedError(account *Account, status int, body []byte) bool {
	if !account.IsCline() {
		return false
	}
	_, ok := cline.Classify(status, nil, body, "", time.Now())
	return ok
}

func (s *RateLimitService) handleClineScopedError(ctx context.Context, account *Account, status int, headers http.Header, body []byte, requestedModel string) bool {
	if !account.IsCline() {
		return false
	}
	upstreamModel := account.GetMappedModel(requestedModel)
	limit, handled := cline.Classify(status, headers, body, upstreamModel, time.Now())
	if !handled {
		return false
	}
	repo, ok := s.accountRepo.(clineScopedRateLimitRepository)
	if !ok {
		slog.Error("cline.scoped_limit_repository_unavailable", "account_id", account.ID)
		return true
	}
	if err := repo.SetClineRateLimitIfLater(ctx, account, limit.Scope, limit.Until(), limit.Kind, limit.ResetAt != nil); err != nil {
		slog.Error("cline.scoped_limit_persist_failed", "account_id", account.ID, "scope", limit.Scope, "error", err)
	}
	return true
}
