//go:build unit

package service

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestClineMetadataFreshnessBoundaries(t *testing.T) {
	now := time.Date(2026, time.October, 1, 12, 0, 0, 0, time.UTC)
	for _, ttl := range []time.Duration{ClineQuotaFreshness, ClineCatalogFreshness} {
		t.Run(ttl.String(), func(t *testing.T) {
			future := now.Add(time.Nanosecond)
			lastFresh := now.Add(-ttl + time.Nanosecond)
			expired := now.Add(-ttl)
			older := expired.Add(-time.Nanosecond)
			require.False(t, clineDateFresh(nil, now, ttl))
			require.False(t, clineDateFresh(&future, now, ttl))
			require.True(t, clineDateFresh(&now, now, ttl))
			require.True(t, clineDateFresh(&lastFresh, now, ttl))
			require.False(t, clineDateFresh(&expired, now, ttl))
			require.False(t, clineDateFresh(&older, now, ttl))
		})
	}
	require.False(t, clineDateFresh(&now, now, 0))
	require.False(t, clineDateFresh(&now, now, -time.Second))
}
