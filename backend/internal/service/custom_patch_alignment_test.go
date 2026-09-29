package service

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

// The old fork aligned two sets of defaults. Keep that invariant, but use the
// new upstream's 240/60 defaults instead of restoring the obsolete 900/300.
func TestCustomPatchTicketDefaultAlignment(t *testing.T) {
	for _, tt := range []struct {
		name string
		svc  *OpenAIGatewayService
		ttl  int
		lead int
	}{
		{"nil service", nil, 240, 60},
		{"nil config", &OpenAIGatewayService{}, 240, 60},
		{"zero config", ticketTestService(t, config.OpenAICodexTicketConfig{}, nil), 240, 60},
		{"negative config", ticketTestService(t, config.OpenAICodexTicketConfig{TTLSeconds: -1, RefreshBeforeSeconds: -1}, nil), 240, 60},
		{"explicit old fork values", ticketTestService(t, config.OpenAICodexTicketConfig{TTLSeconds: 900, RefreshBeforeSeconds: 300}, nil), 900, 300},
		{"explicit legacy upstream values", ticketTestService(t, config.OpenAICodexTicketConfig{TTLSeconds: 3600, RefreshBeforeSeconds: 600}, nil), 3600, 600},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.svc.openAICodexTicketConfig()
			require.Equal(t, tt.ttl, got.TTLSeconds)
			require.Equal(t, tt.lead, got.RefreshBeforeSeconds)
		})
	}
}

func TestCustomPatchTicketExpiryDefaults(t *testing.T) {
	now := time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC)
	for _, ttl := range []int{0, -1} {
		require.Equal(t, now.Add(240*time.Second), codexTicketExpiryFromShape(ttl, openAICodexTicketShape{}, now))
	}
	require.Equal(t, now.Add(900*time.Second), codexTicketExpiryFromShape(900, openAICodexTicketShape{}, now))
	// An explicit long TTL must still lose to the signed credential lifetime.
	shape := openAICodexTicketShape{Blocks: 33, IssuedAt: now.Add(-200 * time.Second)}
	require.Equal(t, now.Add(40*time.Second), codexTicketExpiryFromShape(900, shape, now))
}

func TestCustomPatchTicketCookieIsolation(t *testing.T) {
	h := http.Header{"Cookie": []string{"unrelated-client-cookie=must-not-leak"}}
	ticket := &openAICodexTicket{
		Model: "gpt-6-astra", Length: 292, HarvestSessionID: "synthetic-session",
		CapturedAt: time.Now(), HarvestCookiesAt: time.Now().Add(-time.Hour),
		HarvestCookies: []string{"__cflb=synthetic-old-cookie"},
	}
	// A recently rotated state must not make old cookies fresh again.
	restoreBoundCodexTicketHarvestIdentity(h, ticket)
	require.Empty(t, h.Get("Cookie"))
	ticket.HarvestCookiesAt = time.Now()
	restoreBoundCodexTicketHarvestIdentity(h, ticket)
	require.Equal(t, "__cflb=synthetic-old-cookie", h.Get("Cookie"))
}

func TestCustomPatchTicketPlanChangeRejectsPriorBucket(t *testing.T) {
	cfg := config.OpenAICodexTicketConfig{Enabled: true, Models: []string{"gpt-6-astra"}}
	svc := ticketTestService(t, cfg, nil)
	account := ticketTestAccount(291)
	ticket := &openAICodexTicket{
		Model: "gpt-6-astra", State: fakeCodexTicketState(292), Length: 292,
		CapturedAt: time.Now(), ExpiresAt: time.Now().Add(time.Minute),
	}
	require.NoError(t, svc.storeOpenAICodexTicket(context.Background(), account, ticket))
	// Include the persisted representation, not just the in-memory cache.
	account.Extra = map[string]any{openAICodexTicketExtraKey(ticket.Model): ticket}
	require.True(t, svc.lookupOpenAICodexTicket(account, ticket.Model).valid(time.Now(), 292))
	account.Credentials["plan_type"] = "self_serve_business_prolite"
	target := openAICodexTicketTargetLength(account, cfg)
	require.Equal(t, 332, target)
	require.False(t, svc.lookupOpenAICodexTicket(account, ticket.Model).valid(time.Now(), target))
	statuses := OpenAICodexTicketStatuses(account, cfg, time.Now())
	require.Len(t, statuses, 1)
	require.False(t, statuses[0].Ready)
}

func TestCustomPatchTicketStatusRedactsCredentials(t *testing.T) {
	now := time.Now()
	account := ticketTestAccount(292)
	state := fakeCodexTicketState(292)
	account.Extra = map[string]any{
		openAICodexTicketExtraKey("gpt-6-astra"): &openAICodexTicket{
			Model: "gpt-6-astra", State: state, Length: 292,
			CapturedAt: now, ExpiresAt: now.Add(time.Minute), HarvestCookiesAt: now,
			HarvestCookies: []string{"__cflb=synthetic-private-value"},
		},
	}
	statuses := OpenAICodexTicketStatuses(account, config.OpenAICodexTicketConfig{Enabled: true, Models: []string{"gpt-6-astra"}}, now)
	require.Len(t, statuses, 1)
	require.True(t, statuses[0].Ready)
	require.Equal(t, 1, statuses[0].CookieCount)
	require.NotNil(t, statuses[0].CookieExpiresAt)
	encoded, err := json.Marshal(statuses)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), state)
	require.NotContains(t, string(encoded), "synthetic-private-value")
	require.NotContains(t, string(encoded), "harvest_cookies")
}
