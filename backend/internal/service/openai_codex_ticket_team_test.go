package service

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Wei-Shaw/sub2api/internal/config"
)

// Plan-specific turn-state shape: personal accounts mint 10 ciphertext blocks
// (~292 bytes), Team/Business accounts 12 blocks (~332). The observed upstream
// also rotates the exact byte count (292/312 both seen), so only block-level
// buckets are enforced.
func TestOpenAICodexTicketState_PlanBuckets(t *testing.T) {
	personal := ticketTestAccount(41)
	personal.Credentials["plan_type"] = "plus"
	team := ticketTestAccount(42)
	team.Credentials["plan_type"] = "team"

	require.Equal(t, openAICodexStateBucketPersonal, openAICodexStateBucket(personal))
	require.Equal(t, openAICodexStateBucketTeam, openAICodexStateBucket(team))

	require.True(t, openAICodexTicketStateMatchesAccount(personal, fakeCodexTicketState(292)))
	require.True(t, openAICodexTicketStateMatchesAccount(team, fakeCodexTicketState(332)))

	// 个人号的 11 块（312）是降级形态，任何档位都不接受。
	require.True(t, openAICodexStateDegraded(fakeCodexTicketState(312)))
	require.False(t, openAICodexTicketStateMatchesAccount(personal, fakeCodexTicketState(312)))
	require.False(t, openAICodexTicketStateMatchesAccount(team, fakeCodexTicketState(312)))

	// A personal-shaped ticket must not be replayed on a Team account and vice versa.
	require.False(t, openAICodexTicketStateMatchesAccount(team, fakeCodexTicketState(292)))
	require.False(t, openAICodexTicketStateMatchesAccount(personal, fakeCodexTicketState(272)))

	// Unknown plans keep the old global behaviour (shape-only check).
	unknown := ticketTestAccount(44)
	require.Equal(t, openAICodexStateBucketUnknown, openAICodexStateBucket(unknown))
	require.True(t, openAICodexTicketStateMatchesAccount(unknown, fakeCodexTicketState(292)))
	require.True(t, openAICodexTicketStateMatchesAccount(unknown, fakeCodexTicketState(332)))
}

func TestOpenAICodexTicketUsable_RejectsBucketChangeAndHarvestPending(t *testing.T) {
	svc := ticketTestService(t, config.OpenAICodexTicketConfig{
		Enabled:      true,
		TargetLength: 292,
		TTLSeconds:   900,
		FailClosed:   true,
	}, nil)
	require.NotNil(t, svc)
	account := ticketTestAccount(41)
	now := time.Now()

	account.Credentials["plan_type"] = "plus"
	ticket := &openAICodexTicket{
		AccountID:   41,
		Model:       "gpt-6-astra",
		State:       fakeCodexTicketState(292),
		Length:      292,
		CapturedAt:  now,
		ExpiresAt:   now.Add(time.Minute),
		StateBucket: openAICodexStateBucketPersonal,
	}
	require.True(t, ticket.usableFor(account, now, 0))
	require.False(t, ticket.usableFor(account, now, time.Minute))

	// Plan changed under the same account -> the old personal ticket is stale.
	teamAccount := ticketTestAccount(41)
	teamAccount.Credentials["plan_type"] = "team"
	require.False(t, ticket.usableFor(teamAccount, now, 0))

	// Watchdog signal from a business response forces a re-harvest instead of reuse.
	pending := *ticket
	pending.HarvestPending = true
	require.False(t, pending.usableFor(account, now, 0))
}
