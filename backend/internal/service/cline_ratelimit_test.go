package service

import (
	"context"
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
}

func (s *clineRateLimitPersistStub) SetRateLimited(context.Context, int64, time.Time) error {
	s.regularCalls++
	return nil
}

func (s *clineRateLimitPersistStub) SetRateLimitedIfLater(context.Context, int64, time.Time) error {
	s.extendCalls++
	return nil
}

func TestSetClineRateLimitedPrefersAtomicExtension(t *testing.T) {
	stub := &clineRateLimitPersistStub{}
	require.NoError(t, setClineRateLimited(context.Background(), stub, 42, time.Now().Add(time.Hour)))
	require.Equal(t, 0, stub.regularCalls)
	require.Equal(t, 1, stub.extendCalls)
}
