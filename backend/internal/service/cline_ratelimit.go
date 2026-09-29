package service

import (
	"context"
	"math"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
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

var (
	clineTryAgainPattern     = regexp.MustCompile(`(?i)\btry\s+again\s+in\s+((?:\d+(?:\.\d+)?\s*[smhd]\s*)+)`)
	clineDurationPartPattern = regexp.MustCompile(`(?i)(\d+(?:\.\d+)?)\s*([smhd])`)
)

// parseClineRateLimitResetAt recognizes the reset duration returned by Cline
// and returns an absolute time only for Cline upstream accounts. The upper
// bound prevents an untrusted upstream body from creating an indefinite block.
func parseClineRateLimitResetAt(account *Account, body []byte, now time.Time) (time.Time, bool) {
	if !isClineUpstreamAccount(account) {
		return time.Time{}, false
	}
	match := clineTryAgainPattern.FindSubmatch(body)
	if len(match) != 2 {
		return time.Time{}, false
	}

	var seconds float64
	parts := clineDurationPartPattern.FindAllStringSubmatch(string(match[1]), -1)
	if len(parts) == 0 {
		return time.Time{}, false
	}
	for _, part := range parts {
		value, err := strconv.ParseFloat(part[1], 64)
		if err != nil || math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
			return time.Time{}, false
		}
		switch strings.ToLower(part[2]) {
		case "s":
			seconds += value
		case "m":
			seconds += value * 60
		case "h":
			seconds += value * 60 * 60
		case "d":
			seconds += value * 24 * 60 * 60
		}
		if seconds > maxClineRateLimitCooldown.Seconds() {
			return time.Time{}, false
		}
	}
	if seconds <= 0 {
		return time.Time{}, false
	}
	return now.Add(time.Duration(math.Ceil(seconds)) * time.Second), true
}

func isClineUpstreamAccount(account *Account) bool {
	if account == nil {
		return false
	}
	baseURL := strings.TrimSpace(account.GetBaseURL())
	parsed, err := url.Parse(baseURL)
	return err == nil && strings.EqualFold(parsed.Hostname(), "api.cline.bot")
}
