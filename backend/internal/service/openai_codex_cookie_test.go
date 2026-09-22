package service

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Wei-Shaw/sub2api/internal/config"
)

// Since 2026-09-22 the upstream keeps codex sessions alive via cookies rather
// than a long-lived turn-state: the harvest response sets __oailb/__cflb/__cf_bm
// which must be persisted per account and replayed on both harvest probes and
// business requests so that fresh tickets keep being issued.
func TestProbeOpenAICodexTicket_PersistsAndReusesCookieJar(t *testing.T) {
	state := fakeCodexTicketState(292)
	firstHeader := http.Header{}
	firstHeader.Set(openAICodexTurnStateHeader, state)
	firstHeader.Add("Set-Cookie", "__oailb=oai-token; Path=/; HttpOnly; Secure")
	firstHeader.Add("Set-Cookie", "__cflb=cf-lb-token; Path=/; Expires=Tue, 22 Sep 2026 03:53:12 GMT")
	firstHeader.Add("Set-Cookie", "__cf_bm=cf-bm-token; Domain=chatgpt.com; Path=/; HttpOnly")
	firstHeader.Add("Set-Cookie", "unrelated=should-be-ignored; Path=/")

	secondHeader := http.Header{}
	secondHeader.Set(openAICodexTurnStateHeader, state)

	upstream := &httpUpstreamRecorder{
		responses: []*http.Response{
			{
				StatusCode: http.StatusOK,
				Header:     firstHeader,
				Body:       io.NopCloser(strings.NewReader("data: {}\n\n")),
			},
			{
				StatusCode: http.StatusOK,
				Header:     secondHeader,
				Body:       io.NopCloser(strings.NewReader("data: {}\n\n")),
			},
		},
	}
	svc := ticketTestService(t, config.OpenAICodexTicketConfig{
		Enabled:                      true,
		TargetLength:                 292,
		TTLSeconds:                   360,
		HarvestProxyURL:              "socks5h://user:pass@harvest.example:31",
		HarvestAttemptTimeoutSeconds: 5,
		FailClosed:                   true,
	}, upstream)
	account := ticketTestAccount(41)

	// First probe captures the ticket plus the cookie jar.
	svc.probeOnceOpenAICodexTicket(context.Background(), account, "gpt-6-astra")
	require.NotNil(t, svc.lookupOpenAICodexTicket(account, "gpt-6-astra"))
	require.Equal(t, map[string]string{
		"__oailb": "oai-token",
		"__cflb":  "cf-lb-token",
		"__cf_bm": "cf-bm-token",
	}, svc.lookupOpenAICodexCookieJar(account))

	// Second probe must replay the persisted cookie jar upstream.
	svc.probeOnceOpenAICodexTicket(context.Background(), account, "gpt-6-astra")
	require.Len(t, upstream.requests, 2)
	require.Equal(t, "__cf_bm=cf-bm-token; __cflb=cf-lb-token; __oailb=oai-token",
		upstream.requests[1].Header.Get("Cookie"))

	// Business requests inject both the ticket and the cookie jar.
	h := http.Header{}
	require.NoError(t, svc.applyOpenAICodexTicket(context.Background(), account, "gpt-6-astra", h))
	require.Equal(t, state, h.Get(openAICodexTurnStateHeader))
	require.Equal(t, "__cf_bm=cf-bm-token; __cflb=cf-lb-token; __oailb=oai-token", h.Get("Cookie"))
}

// Without a valid ticket the cookie jar alone must still be injected, because
// the upstream reissues fresh tickets as long as the cookies are alive.
func TestApplyOpenAICodexTicket_InjectsCookieJarWithoutValidTicket(t *testing.T) {
	svc := ticketTestService(t, config.OpenAICodexTicketConfig{
		Enabled:      true,
		TargetLength: 292,
		TTLSeconds:   360,
		FailClosed:   false,
	}, nil)
	account := ticketTestAccount(41)
	svc.openaiCodexCookies.Store(account.ID, map[string]string{"__oailb": "oai", "__cflb": "lb"})

	h := http.Header{}
	require.NoError(t, svc.applyOpenAICodexTicket(context.Background(), account, "gpt-6-astra", h))
	require.Equal(t, "", h.Get(openAICodexTurnStateHeader))
	require.Equal(t, "__cflb=lb; __oailb=oai", h.Get("Cookie"))
}

func TestOpenAICodexCookieJarFromResponse_FiltersUnmanagedCookies(t *testing.T) {
	header := http.Header{}
	header.Add("Set-Cookie", "__oailb=v1; Path=/")
	header.Add("Set-Cookie", "session=secret; Path=/")
	header.Add("Set-Cookie", "__cflb=; Path=/")

	jar := openAICodexCookieJarFromResponse(&http.Response{Header: header})
	require.Equal(t, map[string]string{"__oailb": "v1"}, jar)
	require.Empty(t, openAICodexCookieHeader(nil))
}

func TestOpenAICodexTicketStateValid_LengthBounds(t *testing.T) {
	require.True(t, openAICodexTicketStateValid(fakeCodexTicketState(292)))
	require.False(t, openAICodexTicketStateValid(fakeCodexTicketState(312)))
	require.False(t, openAICodexTicketStateValid(""))
	require.False(t, openAICodexTicketStateValid(strings.Repeat("X", 292)))
	require.False(t, openAICodexTicketStateValid(fakeCodexTicketState(openAICodexTicketMaxStateLength+1)))
	require.False(t, openAICodexTicketStateValid(openAICodexTicketStatePrefix))

	longButValid := openAICodexTicketStatePrefix + strings.Repeat("B", openAICodexTicketMaxStateLength-len(openAICodexTicketStatePrefix))
	require.True(t, openAICodexTicketStateValid(longButValid))
	require.False(t, openAICodexTicketStateValid(longButValid+"B"))
}
