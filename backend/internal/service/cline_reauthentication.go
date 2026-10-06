package service

import (
	"context"
	"log/slog"
	"time"
)

type clineReauthenticationRepository interface {
	MarkClineReauthenticationRequired(context.Context, *Account, time.Time) (bool, error)
}

// A rotating account token returning 401 is not proof of permanent revocation.
// Suspend admission, preserve provider/local quota state and let a later valid
// profile observation (or explicit credential replacement) establish recovery.
// We never own/rotate the official client's refresh token.
func (s *RateLimitService) handleClineReauthentication(ctx context.Context, account *Account) bool {
	if !account.IsClineAccountToken() {
		return false
	}
	now := time.Now().UTC()
	repo, ok := s.accountRepo.(clineReauthenticationRepository)
	if ok {
		persist, cancel := context.WithTimeout(context.WithoutCancel(ctx), clineLimitPersistTimeout)
		_, err := repo.MarkClineReauthenticationRequired(persist, account, now)
		cancel()
		if err != nil {
			slog.Error("cline.reauthentication_state_failed", "account_id", account.ID)
		}
		if err != nil && s.runtimeBlocker != nil {
			s.runtimeBlocker.BlockAccountScheduling(account, now.Add(5*time.Minute), "cline_reauthentication_state_failed")
		}
	} else if s.runtimeBlocker != nil {
		s.runtimeBlocker.BlockAccountScheduling(account, now.Add(5*time.Minute), "cline_reauthentication_required")
	}
	if wake, ok := s.runtimeBlocker.(interface{ RequestClineMetadataRefresh() }); ok {
		wake.RequestClineMetadataRefresh()
	}
	return true
}
