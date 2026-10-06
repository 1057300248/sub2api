//go:build unit

package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/cline"
	"github.com/stretchr/testify/require"
)

type clineMetadataRepoStub struct {
	AccountRepository
	claim    bool
	save     bool
	failure  error
	stored   *ClineState
	captured *Account
}

func (r *clineMetadataRepoStub) ClaimClineMetadataRefresh(_ context.Context, _ *Account, _ time.Time) (bool, error) {
	return r.claim, r.failure
}
func (r *clineMetadataRepoStub) SaveClineStateIfUnchanged(_ context.Context, a *Account, state *ClineState) (bool, error) {
	r.captured, r.stored = a, state
	return r.save, r.failure
}

type clineMetadataHTTPStub struct {
	HTTPUpstream
	responses map[string]string
	statuses  map[string]int
	requests  []*http.Request
	failure   error
}

func (s *clineMetadataHTTPStub) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	s.requests = append(s.requests, req)
	if s.failure != nil {
		return nil, s.failure
	}
	status := s.statuses[req.URL.String()]
	if status == 0 {
		status = http.StatusOK
	}
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(s.responses[req.URL.String()]))}, nil
}

func clineMetadataFixture(t *testing.T) (*Account, *clineMetadataRepoStub, *clineMetadataHTTPStub, *OpenAIGatewayService) {
	t.Helper()
	a := clineRoutingAccount(t, cline.ModePass, cline.AuthAPIKey)
	repo := &clineMetadataRepoStub{claim: true, save: true}
	transport := &clineMetadataHTTPStub{responses: map[string]string{
		cline.ModelCatalogURL: `{"data":[{"id":"cline-pass/model","supportsImages":false,"supportsReasoning":true}]}`,
		cline.CatalogURL:      `{"clinePass":[{"id":"cline-pass/model"}],"free":[{"id":"vendor/free"}],"recommended":[]}`,
		cline.ProfileURL:      `{"id":"user-fixture","active_account_id":"account-fixture","email":"private@example.invalid"}`,
		cline.UsageURL:        `{"success":true,"data":{"limits":[{"type":"weekly","percentUsed":12.5}]}}`,
	}}
	return a, repo, transport, &OpenAIGatewayService{accountRepo: repo, httpUpstream: transport}
}

func TestClineMetadataRefreshUsesBoundedCredentialSafeGETs(t *testing.T) {
	a, repo, transport, gateway := clineMetadataFixture(t)
	view, err := gateway.RefreshClineMetadata(context.Background(), a)
	require.NoError(t, err)
	require.True(t, view.Persisted)
	require.Equal(t, "partial", view.QuotaStatus)
	require.Nil(t, view.Windows[0].PercentUsed)
	require.InDelta(t, 12.5, *view.Windows[1].PercentUsed, 0.001)
	require.Nil(t, view.Windows[2].PercentUsed)
	require.Nil(t, view.IncrementalCostUSD)
	require.True(t, view.IdentityVerified)
	require.Len(t, transport.requests, 4)
	for _, req := range transport.requests {
		require.Equal(t, "GET", req.Method)
		require.Equal(t, "api.cline.bot", req.URL.Host)
		require.True(t, HTTPUpstreamRedirectsDisabled(req.Context()))
		require.True(t, HTTPUpstreamPublicHostsOnly(req.Context()))
		_, hasDeadline := req.Context().Deadline()
		require.True(t, hasDeadline)
		if req.URL.String() == cline.CatalogURL || req.URL.String() == cline.ModelCatalogURL {
			require.Empty(t, req.Header.Get("Authorization"))
		} else {
			require.Equal(t, "Bearer test-key-not-real", req.Header.Get("Authorization"))
		}
	}
	body, err := json.Marshal(view)
	require.NoError(t, err)
	for _, secret := range []string{"test-key-not-real", "private@example.invalid", "user-fixture", "account-fixture", repo.stored.Identity, repo.stored.CredentialFingerprint} {
		require.NotContains(t, string(body), secret)
	}
	require.Equal(t, a.Credentials, repo.captured.Credentials)
	require.False(t, IsUpstreamBillingProbeIdentity(PlatformCline, AccountTypeAPIKey))
}

func TestClineMetadataErrorsPreserveUnknownAndLastValidCatalog(t *testing.T) {
	for _, scenario := range []string{"forbidden", "schema", "oversized", "network", "redirect"} {
		t.Run(scenario, func(t *testing.T) {
			a, repo, transport, gateway := clineMetadataFixture(t)
			old := time.Now().UTC().Add(-time.Minute)
			a.Extra[ClineStateExtraKey] = &ClineState{Mode: cline.ModePass, CredentialFingerprint: ClineCredentialFingerprint(a), FetchedAt: &old, LastSuccessAt: &old, CatalogFetchedAt: &old, Catalog: &cline.Catalog{Pass: []cline.Model{{ID: "cline-pass/last-valid"}}}, Windows: []cline.Window{{Type: "weekly", PercentUsed: 42}}, QuotaStatus: "ok", Persisted: true}
			transport.responses[cline.CatalogURL] = `{"unknown":[]}`
			switch scenario {
			case "forbidden":
				transport.statuses = map[string]int{cline.UsageURL: 403}
			case "schema":
				transport.responses[cline.UsageURL] = `{"success":true,"data":{"limits":[{"type":"weekly"}]}}`
			case "oversized":
				transport.responses[cline.UsageURL] = strings.Repeat("x", cline.MaxBodyBytes+1)
			case "network":
				transport.failure = errors.New("private driver error: secret-fixture")
			case "redirect":
				transport.statuses = map[string]int{cline.UsageURL: 302}
			}
			view, err := gateway.RefreshClineMetadata(context.Background(), a)
			require.NoError(t, err)
			require.Equal(t, "unknown", view.QuotaStatus)
			for _, window := range view.Windows {
				require.Nil(t, window.PercentUsed)
			}
			require.Equal(t, "stale", view.CatalogStatus)
			require.Equal(t, "cline-pass/last-valid", view.Catalog.Pass[0].ID)
			require.Equal(t, 42.0, repo.stored.Windows[0].PercentUsed)
			require.Equal(t, old, *repo.stored.LastSuccessAt)
			require.NotContains(t, view.Error, "secret")
			require.Equal(t, cline.ModePass, a.GetClineMode())
		})
	}
}

func TestClineMetadataBoundariesAndPersistenceFailure(t *testing.T) {
	for _, scenario := range []string{"custom_origin", "busy", "rotated", "repository_failure", "free", "unknown"} {
		t.Run(scenario, func(t *testing.T) {
			a, repo, transport, gateway := clineMetadataFixture(t)
			switch scenario {
			case "custom_origin":
				a.Credentials["base_url"] = "https://custom.example.invalid/api/v1"
			case "busy":
				repo.claim = false
			case "rotated":
				repo.save = false
			case "repository_failure":
				repo.failure = errors.New("private database detail")
			case "free":
				a.Credentials["account_mode"] = cline.ModeFree
			case "unknown":
				a.Credentials["account_mode"] = cline.ModeUnknown
			}
			view, err := gateway.RefreshClineMetadata(context.Background(), a)
			if scenario == "free" || scenario == "unknown" {
				require.NoError(t, err)
				require.Equal(t, "not_applicable", view.QuotaStatus)
				require.Len(t, transport.requests, 1)
				require.Empty(t, transport.requests[0].Header.Get("Authorization"))
				return
			}
			require.Error(t, err)
			require.Nil(t, view)
			require.NotContains(t, err.Error(), "private database")
			if scenario != "rotated" {
				require.Empty(t, transport.requests)
			}
		})
	}
}

func TestClineMetadataExpiryAndIdentityRotation(t *testing.T) {
	a, _, _, gateway := clineMetadataFixture(t)
	view, err := gateway.RefreshClineMetadata(context.Background(), a)
	require.NoError(t, err)
	now := time.Now().UTC()
	state := &ClineState{Mode: cline.ModePass, CredentialFingerprint: ClineCredentialFingerprint(a), FetchedAt: &now, LastSuccessAt: &now, QuotaStatus: "ok", Persisted: true, Windows: []cline.Window{{Type: "weekly", PercentUsed: 0}}}
	a.Extra[ClineStateExtraKey] = state
	fresh := ClineMetadataForAccount(a, now)
	require.NotNil(t, fresh.Windows[1].PercentUsed)
	require.Equal(t, 0.0, *fresh.Windows[1].PercentUsed)
	stale := ClineMetadataForAccount(a, now.Add(ClineQuotaFreshness))
	require.Equal(t, "unknown", stale.QuotaStatus)
	require.Nil(t, stale.Windows[1].PercentUsed)
	a.Credentials["api_key"] = "rotated-fixture"
	require.Nil(t, ClineMetadataForAccount(a, now).LastSuccessAt)
	require.NotNil(t, view.LastSuccessAt)
}
