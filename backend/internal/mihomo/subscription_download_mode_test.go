package mihomo

import (
	"context"
	"github.com/stretchr/testify/require"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
)

func TestSubscriptionDownloadModeRoutesAndFallback(t *testing.T) {
	for _, tc := range []struct {
		name                    string
		mode                    SubscriptionDownloadMode
		emptyProxy              bool
		proxyCalls, directCalls int32
		wantErr                 bool
	}{
		{"direct_bypasses_running_proxy", SubscriptionDownloadDirect, false, 0, 1, false},
		{"proxy_only_success", SubscriptionDownloadProxy, false, 1, 0, false},
		{"proxy_only_invalid_body", SubscriptionDownloadProxy, true, 1, 0, true},
		{"auto_falls_back_on_empty_body", SubscriptionDownloadAuto, true, 1, 1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var directCalls, proxyCalls atomic.Int32
			direct := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				directCalls.Add(1)
				_, _ = w.Write([]byte("proxies:\n  - {name: direct-source, type: http, server: direct.example, port: 8080}\n"))
			}))
			defer direct.Close()
			proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				proxyCalls.Add(1)
				if !tc.emptyProxy {
					_, _ = w.Write([]byte("proxies:\n  - {name: proxy-source, type: http, server: proxy.example, port: 8080}\n"))
				}
			}))
			defer proxy.Close()
			m := New(t.TempDir())
			t.Cleanup(m.Close)
			m.state.Running = true
			m.subscriptionProxyURL = proxy.URL
			m.saved.DownloadMode = tc.mode
			nodes, _, err := m.fetchNodes(t.Context(), []string{direct.URL + "/?token=private-subscription"})
			if tc.wantErr {
				require.Error(t, err)
				require.NotContains(t, err.Error(), "private-subscription")
			} else {
				require.NoError(t, err)
				require.Len(t, nodes, 1)
			}
			require.Equal(t, tc.proxyCalls, proxyCalls.Load())
			require.Equal(t, tc.directCalls, directCalls.Load())
		})
	}
}
func TestSubscriptionProxyModeFailsClosedWhenStopped(t *testing.T) {
	var calls atomic.Int32
	direct := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }))
	defer direct.Close()
	m := New(t.TempDir())
	t.Cleanup(m.Close)
	m.saved.DownloadMode = SubscriptionDownloadProxy
	_, _, err := m.fetchNodes(t.Context(), []string{direct.URL})
	require.ErrorContains(t, err, "not running")
	require.Zero(t, calls.Load())
}
func TestSubscriptionDownloadModePersistsWithoutChangingSources(t *testing.T) {
	dir := t.TempDir()
	m := New(dir)
	t.Cleanup(m.Close)
	m.saved.URLs = []string{"https://example.test/?token=private-subscription"}
	m.saved.DynamicProxies = []string{"http://user:private-proxy@localhost:9000"}
	m.saved.Secret = "private-controller"
	m.saved.Disabled = map[string]string{"keep": "disabled"}
	require.NoError(t, m.SetSubscriptionDownloadMode(context.Background(), SubscriptionDownloadDirect))
	require.Equal(t, SubscriptionDownloadDirect, m.Status().DownloadMode)
	require.False(t, m.Status().Running)
	savedMode := m.saved.DownloadMode
	require.ErrorIs(t, m.SetSubscriptionDownloadMode(t.Context(), "invalid"), ErrSubscriptionDownloadMode)
	require.Equal(t, savedMode, m.saved.DownloadMode)
	restored := New(dir)
	t.Cleanup(restored.Close)
	require.Equal(t, SubscriptionDownloadDirect, restored.Status().DownloadMode)
	require.Equal(t, m.saved.URLs, restored.saved.URLs)
	require.Equal(t, m.saved.DynamicProxies, restored.saved.DynamicProxies)
	require.Equal(t, m.saved.Secret, restored.saved.Secret)
	require.Equal(t, m.saved.Disabled, restored.saved.Disabled)
	info, err := os.Stat(filepath.Join(dir, "settings.json"))
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0600), info.Mode().Perm())
}
