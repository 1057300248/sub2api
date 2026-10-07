package service

import (
	"context"
	"net/url"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/cline"
)

const maxClineRateLimitCooldown = 7 * 24 * time.Hour

type clineRateLimitRepository interface {
	SetRateLimited(context.Context, int64, time.Time) error
}

type clineRateLimitExtender interface {
	SetRateLimitedIfLater(context.Context, int64, time.Time) error
}

// setClineRateLimited keeps the longest observed Cline cooldown when concurrent
// requests report the same account at different times. Older repository stubs
// still use the regular setter as a compatibility fallback.
func setClineRateLimited(ctx context.Context, repo clineRateLimitRepository, accountID int64, resetAt time.Time) error {
	if extender, ok := repo.(clineRateLimitExtender); ok {
		return extender.SetRateLimitedIfLater(ctx, accountID, resetAt)
	}
	return repo.SetRateLimited(ctx, accountID, resetAt)
}

// parseClineRateLimitResetAt preserves the bounded legacy helper contract but
// delegates parsing to the same implementation as native Cline accounts.
func parseClineRateLimitResetAt(account *Account, body []byte, now time.Time) (time.Time, bool) {
	if !isClineUpstreamAccount(account) {
		return time.Time{}, false
	}
	at := cline.ParseResetAt(nil, body, now, maxClineRateLimitCooldown)
	if at == nil {
		return time.Time{}, false
	}
	return *at, true
}

// isLegacyClineAccount identifies only the existing API-key compatibility path.
// It does not migrate an account, grant a plan, or match an arbitrary gateway.
func isLegacyClineAccount(account *Account) bool {
	if account == nil || account.Type != AccountTypeAPIKey ||
		(account.Platform != PlatformOpenAI && account.Platform != PlatformDeepseek) {
		return false
	}
	parsed, err := url.Parse(strings.TrimSpace(account.GetBaseURL()))
	return err == nil && parsed.Scheme == "https" && parsed.User == nil &&
		(parsed.Port() == "" || parsed.Port() == "443") && strings.EqualFold(parsed.Hostname(), "api.cline.bot")
}

func isClineUpstreamAccount(account *Account) bool {
	if account == nil {
		return false
	}
	baseURL := strings.TrimSpace(account.GetBaseURL())
	parsed, err := url.Parse(baseURL)
	return err == nil && parsed.Scheme == "https" && parsed.User == nil &&
		(parsed.Port() == "" || parsed.Port() == "443") && strings.EqualFold(parsed.Hostname(), "api.cline.bot")
}
