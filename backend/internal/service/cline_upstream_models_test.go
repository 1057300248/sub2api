package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/cline"
	"github.com/stretchr/testify/require"
)

func TestClineNativeModelSyncUsesExistingTransportAndModeBuckets(t *testing.T) {
	for _, tc := range []struct{ mode, model string }{{"pass", "cline-pass/model"}, {"payg", "vendor/paid"}, {"free", "vendor/free"}} {
		t.Run(tc.mode, func(t *testing.T) {
			upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"clinePass":[{"id":"cline-pass/model"}],"recommended":[{"id":"vendor/paid"}],"free":[{"id":"vendor/free"}]}`))}}
			gateway := &OpenAIGatewayService{httpUpstream: upstream}
			repo := &upstreamModelMetadataRepoStub{}
			svc := &AccountTestService{openaiGatewayService: gateway, accountRepo: repo}
			proxyID := int64(9)
			a := &Account{ID: 41, Type: AccountTypeAPIKey, Platform: PlatformCline, ProxyID: &proxyID, Proxy: &Proxy{Protocol: "http", Host: "proxy.example.test", Port: 8080}, Credentials: map[string]any{"api_key": "account-secret", "base_url": cline.BaseURL, "account_mode": tc.mode, "cline_auth_type": "account_token", "model_mapping": map[string]any{"invalid": nil}}, Extra: map[string]any{"quota_used": 12, "cline_state": map[string]any{"quota_status": "exhausted"}}}
			before, _ := json.Marshal(a)
			catalog, err := svc.SyncUpstreamModelCatalog(context.Background(), a)
			require.NoError(t, err)
			require.Equal(t, []string{tc.model}, catalog.Models)
			require.Empty(t, catalog.Metadata, "unknown capabilities must not be invented")
			require.Equal(t, "cline_public_catalog", catalog.Warnings[0].Code)
			require.Len(t, upstream.requests, 1)
			req := upstream.lastReq
			require.Equal(t, cline.CatalogURL, req.URL.String())
			require.Equal(t, http.MethodGet, req.Method)
			require.Empty(t, req.Header.Get("Authorization"))
			require.Empty(t, req.Header.Get("X-Api-Key"))
			require.True(t, HTTPUpstreamRedirectsDisabled(req.Context()))
			require.True(t, HTTPUpstreamPublicHostsOnly(req.Context()))
			deadline, ok := req.Context().Deadline()
			require.True(t, ok)
			require.WithinDuration(t, time.Now().Add(30*time.Second), deadline, 2*time.Second)
			require.Equal(t, a.Proxy.URL(), upstream.lastProxyURL)
			after, _ := json.Marshal(a)
			require.Equal(t, string(before), string(after))
			require.Empty(t, repo.updates, "catalog selection must never grant quota or modify the saved model set")
		})
	}
}

func TestClineNativeModelSyncRejectsInvalidConnectionsWithoutHTTP(t *testing.T) {
	for _, tc := range []struct {
		name        string
		credentials map[string]any
		accountType string
	}{
		{"unknown", map[string]any{"api_key": "fixture", "account_mode": "unknown"}, AccountTypeAPIKey},
		{"bad_mode", map[string]any{"api_key": "fixture", "account_mode": "anything"}, AccountTypeAPIKey},
		{"empty_key", map[string]any{"account_mode": "pass"}, AccountTypeAPIKey},
		{"custom_origin", map[string]any{"api_key": "fixture", "account_mode": "pass", "base_url": "https://custom.example.test/api/v1"}, AccountTypeAPIKey},
		{"bad_auth", map[string]any{"api_key": "fixture", "account_mode": "pass", "cline_auth_type": "oauth"}, AccountTypeAPIKey},
		{"wrong_type", map[string]any{"api_key": "fixture", "account_mode": "pass"}, AccountTypeOAuth},
	} {
		t.Run(tc.name, func(t *testing.T) {
			upstream := &httpUpstreamRecorder{}
			svc := &AccountTestService{openaiGatewayService: &OpenAIGatewayService{httpUpstream: upstream}}
			_, err := svc.SyncUpstreamModelCatalog(context.Background(), &Account{Platform: PlatformCline, Type: tc.accountType, Credentials: tc.credentials})
			require.Error(t, err)
			require.Empty(t, upstream.requests)
		})
	}
}

func TestClineNativeModelSyncFailureAndPickerContracts(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
	}{
		{"status", "secret-error", 429}, {"unauthorized", "secret-error", 401}, {"redirect", "", 302},
		{"invalid", "not-json", 200}, {"missing_bucket", `{"recommended":[]}`, 200},
		{"wrong_mode_model", `{"clinePass":[{"id":"vendor/paid"}]}`, 200},
		{"duplicate", `{"clinePass":[{"id":"cline-pass/a"},{"id":"cline-pass/a"}]}`, 200},
		{"too_large", strings.Repeat("a", cline.MaxBodyBytes+1), 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: tc.status, Header: http.Header{"Retry-After": []string{"900"}}, Body: io.NopCloser(strings.NewReader(tc.body))}}
			svc := &AccountTestService{openaiGatewayService: &OpenAIGatewayService{httpUpstream: upstream}}
			a := &Account{Platform: PlatformCline, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": "fixture", "account_mode": "pass"}}
			models, err := svc.FetchUpstreamSupportedModels(context.Background(), a)
			require.Error(t, err)
			require.Nil(t, models)
			require.NotContains(t, err.Error(), "secret-error")
			require.Len(t, upstream.requests, 1, "no blind retry after rate-limit or redirect")
		})
	}
	t.Run("no_transport", func(t *testing.T) {
		svc := &AccountTestService{}
		_, err := svc.SyncUpstreamModelCatalog(context.Background(), &Account{Platform: PlatformCline, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": "fixture", "account_mode": "pass"}})
		require.Error(t, err)
	})
}
