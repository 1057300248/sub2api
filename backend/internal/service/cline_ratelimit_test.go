package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestParseClineRateLimitResetAt(t *testing.T) {
	now := time.Date(2026, 9, 24, 3, 0, 0, 0, time.UTC)
	account := &Account{Type: AccountTypeAPIKey, Credentials: map[string]any{"base_url": "https://api.cline.bot/api/v1"}}

	tests := []struct {
		name string
		body string
		want time.Duration
		ok   bool
	}{
		{name: "hours and minutes", body: `{"error":{"message":"Daily free limit reached. Try again in 1h25m"}}`, want: 85 * time.Minute, ok: true},
		{name: "spaced units", body: `Try again in 2h 10m`, want: 130 * time.Minute, ok: true},
		{name: "seconds", body: `Try again in 45s`, want: 45 * time.Second, ok: true},
		{name: "malformed", body: `Try again later`, ok: false},
		{name: "bounded", body: `Try again in 8d`, ok: false},
		{name: "days and mixed units", body: `Try again in 1d2h3m4s`, want: 26*time.Hour + 3*time.Minute + 4*time.Second, ok: true},
		{name: "fractional rounds up", body: `Try again in 0.25s`, want: time.Second, ok: true},
		{name: "case insensitive", body: `TRY AGAIN IN 1H 2M`, want: 62 * time.Minute, ok: true},
		{name: "exact upper bound", body: `Try again in 7d`, want: 7 * 24 * time.Hour, ok: true},
		{name: "combined upper bound", body: `Try again in 6d24h1s`, ok: false},
		{name: "zero", body: `Try again in 0s`, ok: false},
		{name: "negative", body: `Try again in -1h`, ok: false},
		{name: "nan", body: `Try again in NaNh`, ok: false},
		{name: "infinity", body: `Try again in Infh`, ok: false},
		{name: "numeric overflow", body: `Try again in ` + strings.Repeat("9", 400) + `h`, ok: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := parseClineRateLimitResetAt(account, []byte(tt.body), now)
			require.Equal(t, tt.ok, ok)
			if ok {
				require.Equal(t, now.Add(tt.want), got)
			}
		})
	}
}

func TestParseClineRateLimitResetAtRejectsOtherUpstream(t *testing.T) {
	now := time.Date(2026, 9, 24, 3, 0, 0, 0, time.UTC)
	account := &Account{Type: AccountTypeAPIKey, Credentials: map[string]any{"base_url": "https://api.deepseek.com/v1"}}
	_, ok := parseClineRateLimitResetAt(account, []byte(`Try again in 1h`), now)
	require.False(t, ok)
}

type clineRateLimitPersistStub struct {
	regularCalls int
	extendCalls  int
	extendErr    error
}

func (s *clineRateLimitPersistStub) SetRateLimited(context.Context, int64, time.Time) error {
	s.regularCalls++
	return nil
}

func (s *clineRateLimitPersistStub) SetRateLimitedIfLater(context.Context, int64, time.Time) error {
	s.extendCalls++
	return s.extendErr
}

func TestSetClineRateLimitedPrefersAtomicExtension(t *testing.T) {
	stub := &clineRateLimitPersistStub{}
	require.NoError(t, setClineRateLimited(context.Background(), stub, 42, time.Now().Add(time.Hour)))
	require.Equal(t, 0, stub.regularCalls)
	require.Equal(t, 1, stub.extendCalls)
}

func TestSetClineRateLimitedDoesNotFallbackOnExtensionError(t *testing.T) {
	persistErr := errors.New("CAS persistence failed")
	stub := &clineRateLimitPersistStub{extendErr: persistErr}
	err := setClineRateLimited(context.Background(), stub, 42, time.Now().Add(time.Hour))
	require.ErrorIs(t, err, persistErr)
	require.Zero(t, stub.regularCalls)
	require.Equal(t, 1, stub.extendCalls)
}

type clineRateLimitLegacyRepoStub struct {
	calls     int
	accountID int64
	resetAt   time.Time
	err       error
}

func (s *clineRateLimitLegacyRepoStub) SetRateLimited(_ context.Context, accountID int64, resetAt time.Time) error {
	s.calls++
	s.accountID = accountID
	s.resetAt = resetAt
	return s.err
}

func TestSetClineRateLimitedSupportsLegacyRepository(t *testing.T) {
	persistErr := errors.New("legacy persistence failed")
	for _, wantErr := range []error{nil, persistErr} {
		stub := &clineRateLimitLegacyRepoStub{err: wantErr}
		resetAt := time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC)
		err := setClineRateLimited(context.Background(), stub, 42, resetAt)
		require.ErrorIs(t, err, wantErr)
		require.Equal(t, 1, stub.calls)
		require.Equal(t, int64(42), stub.accountID)
		require.Equal(t, resetAt, stub.resetAt)
	}
}

func TestParseClineRateLimitResetAtScopesExactHostname(t *testing.T) {
	now := time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC)
	for _, tt := range []struct {
		baseURL string
		ok      bool
	}{
		{baseURL: "https://API.CLINE.BOT:443/api/v1", ok: true},
		{baseURL: "https://api.cline.bot.example.com/api/v1"},
		{baseURL: "https://api.cline.bot@example.com/api/v1"},
		{baseURL: "https://api.deepseek.com/v1"},
		{baseURL: "://invalid"},
		{baseURL: ""},
	} {
		t.Run(tt.baseURL, func(t *testing.T) {
			account := &Account{Type: AccountTypeAPIKey, Credentials: map[string]any{"base_url": tt.baseURL}}
			_, ok := parseClineRateLimitResetAt(account, []byte(`Try again in 1h`), now)
			require.Equal(t, tt.ok, ok)
		})
	}
	_, ok := parseClineRateLimitResetAt(nil, []byte(`Try again in 1h`), now)
	require.False(t, ok)
}
