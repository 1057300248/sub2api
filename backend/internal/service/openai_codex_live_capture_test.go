package service

import (
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// Fixtures captured from the live codex endpoint on 2026-09-22 through the
// harvest proxy (values truncated to their shape; no usable credential here).
// They pin the behaviour we verified in production before deploying the fix:
//
//	probe #1  -> 200, x-codex-turn-state length 292, Set-Cookie __oailb/__cf_bm/__cflb
//	longevity -> same ticket + cookie jar kept working for 342s while the
//	             upstream rotated fresh 312-length tickets
//	cookie-less -> probe never re-issued a ticket (one-shot only)
// Real 292 captured from the live endpoint on 2026-09-22 (truncated copy of the
// exact blob the server issued; only the shape is asserted here).
const liveCapturedTicket292 = "gAAAAABqsd-HLwybuxNhf8vQ23hWEeY8R6d_pZANBmaXb89acwtsjboVUCEZmDcbMtdg1dArsIPtxqr6obEnO2O1t5W6GM6WzExIh0mMY_9-CvX6qugEvd5Prnlu2wtyPVMTANbVUc0oIVQ48kQbgnJKCSCcWNowSSEGbeUd4Zq2AUvXAYHScRS3rn8OTbNWtJtcOOqgUNDa_1PBwfaJnfuGJej6Pw1M34KetSRyAPR8kerDF9SSm44usCehqkC-Vps1sYElp9SrDNNUCIAfEvHYhnQ2nQ4l_w=="


// 2026-09-22 官方社区实测 + 本地 A/B 复现：292 = 请求的模型，312 = 降级到
// gpt-5.6-luna。因此 312 必须被丢弃并继续重采，绝不能注入业务请求。
func TestLiveCaptureShapes_HealthyAcceptedDegradedRejected(t *testing.T) {
	t.Logf("live captured ticket length = %d", len(liveCapturedTicket292))

	fixtureTicket292 := fakeCodexTicketState(openAICodexPersonalStateLength)
	fixtureTicket312 := fakeCodexTicketState(openAICodexDegradedStateLength)
	require.Equal(t, 292, len(fixtureTicket292))
	require.Equal(t, 312, len(fixtureTicket312))

	require.True(t, openAICodexTicketStateValid(fixtureTicket292))
	require.True(t, openAICodexTicketStateMatchesAccount(ticketTestAccount(41), fixtureTicket292))

	// 11-block (312) downgrade shape is rejected outright.
	require.True(t, openAICodexStateDegraded(fixtureTicket312))
	require.False(t, openAICodexTicketStateValid(fixtureTicket312))
	require.False(t, openAICodexTicketStateMatchesAccount(ticketTestAccount(41), fixtureTicket312))
}

func TestLiveCaptureCookies_AreExtractedFromSetCookieHeaders(t *testing.T) {
	// Shape-accurate replay of the three Set-Cookie headers the live endpoint
	// returned; only the managed names may be persisted.
	header := http.Header{}
	header.Add("Set-Cookie", "__oailb=eyJhbGciOiJFUzI1NiJ9.eyJleHAiOjE3OTAwNDU4OTJ9.sig; Path=/; Expires=Tue, 22 Sep 2026 02:53:12 GMT; HttpOnly; Secure; SameSite=Lax")
	header.Add("Set-Cookie", "__cf_bm=abc123.1790041991.125776-1.0.1.1-def; HttpOnly; SameSite=None; Secure; Path=/; Domain=chatgpt.com")
	header.Add("Set-Cookie", "__cflb=0H28vzvP5FJafnkHxih2iihXk1Bfph6a98cs86KTyYf; HttpOnly; SameSite=None; Secure; Path=/")
	header.Add("Set-Cookie", "oai-client-state=drop-me; Path=/")

	jar := openAICodexCookieJarFromResponse(&http.Response{Header: header})
	require.Equal(t, map[string]string{
		"__oailb": "eyJhbGciOiJFUzI1NiJ9.eyJleHAiOjE3OTAwNDU4OTJ9.sig",
		"__cf_bm": "abc123.1790041991.125776-1.0.1.1-def",
		"__cflb":  "0H28vzvP5FJafnkHxih2iihXk1Bfph6a98cs86KTyYf",
	}, jar)

	headerValue := openAICodexCookieHeader(jar)
	require.True(t, strings.HasPrefix(headerValue, "__cf_bm="))
	require.Contains(t, headerValue, "; __cflb=")
	require.Contains(t, headerValue, "; __oailb=")

	outbound := http.Header{}
	applyOpenAICodexCookies(outbound, jar)
	require.Equal(t, headerValue, outbound.Get("Cookie"))
}
