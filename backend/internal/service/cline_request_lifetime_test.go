//go:build unit

package service

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestClineDrainGraceRetainsLateUsageAndStops(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	defer cancel()
	life := newClineRequestLifetime(parent, 200*time.Millisecond, 2*time.Second)
	defer life.close()
	cancel()
	require.NoError(t, life.ctx.Err())
	r := &clineLifetimeBody{source: io.NopCloser(strings.NewReader(`{"usage":{"prompt_tokens":8}}`)), lifetime: life}
	data, err := io.ReadAll(r)
	require.NoError(t, err)
	require.Contains(t, string(data), "prompt_tokens")
	require.NoError(t, r.Close())
	require.ErrorIs(t, context.Cause(life.ctx), context.Canceled)
	require.NoError(t, r.Close())
}

// Real net/http transport, loopback only. Cancellation must interrupt a blocked
// body Read and reach the server; a timer which only stops DB billing is not enough.
func TestClineDrainDeadlineInterruptsBlockedHTTPRead(t *testing.T) {
	stopped := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(stopped)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		if err := http.NewResponseController(w).Flush(); err != nil {
			return
		}
		<-r.Context().Done()
	}))
	defer server.Close()
	parent, cancel := context.WithCancel(context.Background())
	defer cancel()
	life := newClineRequestLifetime(parent, 40*time.Millisecond, 2*time.Second)
	defer life.close()
	req, err := http.NewRequestWithContext(life.ctx, http.MethodGet, server.URL, nil)
	require.NoError(t, err)
	resp, err := server.Client().Do(req)
	require.NoError(t, err)
	body := &clineLifetimeBody{source: resp.Body, lifetime: life}
	defer func() { require.NoError(t, body.Close()) }()
	cancel()
	finished := make(chan error, 1)
	go func() { _, err := io.ReadAll(body); finished <- err }()
	select {
	case err := <-finished:
		require.ErrorIs(t, err, ErrClineDrainTimeout)
	case <-time.After(time.Second):
		t.Fatal("blocked upstream read did not end")
	}
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("upstream connection was not canceled")
	}
}

func TestClineRequestCeilingCoversHeadersAndConcurrentCleanup(t *testing.T) {
	life := newClineRequestLifetime(context.Background(), time.Second, 30*time.Millisecond)
	select {
	case <-life.ctx.Done():
		require.ErrorIs(t, context.Cause(life.ctx), ErrClineRequestTimeout)
	case <-time.After(time.Second):
		t.Fatal("request ceiling missing")
	}
	life.close()
	for i := 0; i < 20; i++ {
		parent, cancel := context.WithCancel(context.Background())
		life := newClineRequestLifetime(parent, time.Second, time.Second)
		var wg sync.WaitGroup
		for n := 0; n < 8; n++ {
			wg.Add(1)
			go func() { defer wg.Done(); life.beginDrain(); life.close(); cancel() }()
		}
		wg.Wait()
		require.Error(t, life.ctx.Err())
	}
}

func TestClineWriteDisconnectStartsDrainWithoutParentCancellation(t *testing.T) {
	life := newClineRequestLifetime(context.Background(), 20*time.Millisecond, time.Second)
	defer life.close()
	body := &clineLifetimeBody{source: io.NopCloser(strings.NewReader("")), lifetime: life}
	defer func() { require.NoError(t, body.Close()) }()
	beginClineBodyDrain(body)
	select {
	case <-life.ctx.Done():
		require.ErrorIs(t, context.Cause(life.ctx), ErrClineDrainTimeout)
	case <-time.After(time.Second):
		t.Fatal("write disconnect did not start drain")
	}
}
