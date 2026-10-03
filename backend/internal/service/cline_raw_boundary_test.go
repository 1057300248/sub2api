//go:build unit

package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/Wei-Shaw/sub2api/internal/pkg/cline"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClineRawChatCanonicalFieldsAndExtensions(t *testing.T) {
	for _, platform := range []string{PlatformCline, PlatformOpenAI, PlatformDeepseek} {
		t.Run(platform, func(t *testing.T) {
			a := &Account{Platform: platform, Type: AccountTypeAPIKey, Credentials: map[string]any{"base_url": cline.BaseURL}}
			body := []byte(`{"model":"mapped","MODEL":"other","stream":false,"STREAM":true,"ſtream":true,"tools":[],"TOOLS":[{"type":"unknown"}],"max_tokens":8,"MAX_TOKENS":999,"max_toKens":999,"providerOptions":{"gateway":{"only":["deepseek"]}},"vendor":{ "zero":0,"off":false,"large":9007199254740993,"nested":{"MODEL":"untouched"} }}`)
			original := append([]byte(nil), body...)
			got, err := normalizeClineRawChatBody(a, body)
			require.NoError(t, err)
			var sent apicompat.ChatCompletionsRequest
			require.NoError(t, json.Unmarshal(got, &sent), "equivalent case-insensitive upstream decoder")
			assert.Equal(t, "mapped", sent.Model)
			assert.False(t, sent.Stream)
			require.NotNil(t, sent.MaxTokens)
			assert.Equal(t, 8, *sent.MaxTokens)
			assert.Empty(t, sent.Tools)
			var before, after map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(body, &before))
			require.NoError(t, json.Unmarshal(got, &after))
			assert.Equal(t, before["providerOptions"], after["providerOptions"])
			assert.Equal(t, before["vendor"], after["vendor"], "do not coerce numbers or rewrite nested vendor keys")
			assert.Equal(t, original, body, "retry must see an unchanged source request")
			for _, alias := range []string{"MODEL", "STREAM", "ſtream", "TOOLS", "MAX_TOKENS", "max_toKens"} {
				assert.NotContains(t, after, alias)
			}
		})
	}
	// Every DTO field, not just the examples, has the same folded-name guard.
	a := &Account{Platform: PlatformCline}
	typ := reflect.TypeOf(apicompat.ChatCompletionsRequest{})
	for i := 0; i < typ.NumField(); i++ {
		name := strings.Split(typ.Field(i).Tag.Get("json"), ",")[0]
		if name == "" || name == "-" {
			continue
		}
		for _, alias := range []string{strings.ToUpper(name), strings.ToUpper(name[:1]) + name[1:], strings.ReplaceAll(name, "s", "ſ"), strings.ReplaceAll(name, "k", "K")} {
			if alias == name {
				continue
			}
			source, err := json.Marshal(map[string]any{name: "canonical", alias: "must-not-pass", "vendorFlag": false})
			require.NoError(t, err)
			got, err := normalizeClineRawChatBody(a, source)
			require.NoError(t, err)
			var sent map[string]any
			require.NoError(t, json.Unmarshal(got, &sent))
			assert.Equal(t, "canonical", sent[name])
			assert.NotContains(t, sent, alias)
			assert.Equal(t, false, sent["vendorFlag"])
		}
	}
}

func TestClineRawChatDuplicateKeysFailClosed(t *testing.T) {
	a := &Account{Platform: PlatformCline}
	for _, body := range []string{
		`{"model":"a","model":"b"}`, `{"stream":false,"stream":true}`,
		`{"stream":false,"\u0073tream":true}`, `{"tools":[],"tools":[{}]}`,
		`{"providerOptions":{},"providerOptions":{"gateway":{}}}`,
		`{"vendor":1,"vendor":2}`, `{"STREAM":false,"STREAM":true}`,
		`[]`, `null`, `{"model":"a"} {}`, `{"model":`,
	} {
		_, err := normalizeClineRawChatBody(a, []byte(body))
		require.Error(t, err, body)
	}
	for _, body := range []string{`{"MODEL":"not-promoted"}`, `{"STREAM":true}`} {
		got, err := normalizeClineRawChatBody(a, []byte(body))
		require.NoError(t, err)
		assert.JSONEq(t, `{}`, string(got))
	}
	unrelated := &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"base_url": "https://gateway.example/v1"}}
	original := []byte(`{"model":"a","MODEL":"b","stream":false,"stream":true}`)
	got, err := normalizeClineRawChatBody(unrelated, original)
	require.NoError(t, err)
	assert.Equal(t, original, got, "other providers retain their existing contracts")
}

type clineTerminalSource struct {
	reader *strings.Reader
	tail   error
	reads  int
	closed int
}

func (b *clineTerminalSource) Read(p []byte) (int, error) {
	b.reads++
	if b.reader.Len() > 0 {
		return b.reader.Read(p)
	}
	return 0, b.tail
}
func (b *clineTerminalSource) Close() error { b.closed++; return nil }

func TestClineDeadlineAfterValidatedDonePreservesCompletion(t *testing.T) {
	for _, ending := range []string{"\n\n", "\r\n\r\n", "\r\r"} {
		source := &clineTerminalSource{reader: strings.NewReader("data: [DONE]" + ending), tail: context.DeadlineExceeded}
		lifetime := newClineRequestLifetime(context.Background(), time.Second, time.Minute)
		body := &clineLifetimeBody{source: cline.GuardSSEBody(source, 4096, nil), lifetime: lifetime}
		data, err := io.ReadAll(body)
		require.NoError(t, err)
		require.Contains(t, string(data), "[DONE]")
		require.True(t, body.ClineSSEComplete())
		reads := source.reads
		lifetime.cancel(ErrClineCallerDeadline)
		n, err := body.Read(make([]byte, 32))
		assert.Zero(t, n)
		assert.ErrorIs(t, err, io.EOF)
		assert.NoError(t, clineBodyReadError(body, nil))
		assert.Equal(t, reads, source.reads, "do not wait for EOF after the terminal frame")
		assert.ErrorIs(t, clineBodyReadError(body, io.ErrUnexpectedEOF), ErrClineCallerDeadline, "genuine scanner errors must not be erased")
		require.NoError(t, body.Close())
		require.NoError(t, body.Close())
		assert.Equal(t, 1, source.closed)
	}
}

func TestClineTerminalGuardDoesNotPromotePartialOrFailedStreams(t *testing.T) {
	for _, payload := range []string{
		"data: {\"usage\":{\"prompt_tokens\":8},\"choices\":[{\"finish_reason\":\"stop\"}]}\n\n",
		"data: [DONE]\n",
		"data: {\"error\":{\"message\":\"failed\"}}\n\ndata: [DONE]\n\n",
	} {
		source := &clineTerminalSource{reader: strings.NewReader(payload), tail: io.EOF}
		lifetime := newClineRequestLifetime(context.Background(), time.Second, time.Minute)
		body := &clineLifetimeBody{source: cline.GuardSSEBody(source, 4096, nil), lifetime: lifetime}
		_, err := io.ReadAll(body)
		require.Error(t, err)
		assert.False(t, body.ClineSSEComplete())
		assert.True(t, errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, cline.ErrStreamFailure))
		require.NoError(t, body.Close())
	}
}
