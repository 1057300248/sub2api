package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/cline"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const ClineQuotaFreshness = 5 * time.Minute
const ClineCatalogFreshness = time.Hour
const ClineMetadataTimeout = 15 * time.Second

var ErrClineMetadataUnavailable = infraerrors.New(http.StatusServiceUnavailable, "CLINE_METADATA_UNAVAILABLE", "Cline metadata service is unavailable")
var ErrClineRefreshTooSoon = infraerrors.New(http.StatusTooManyRequests, "CLINE_REFRESH_BUSY", "Cline metadata refresh is already running or was requested recently")
var ErrClineMetadataChanged = infraerrors.New(http.StatusConflict, "CLINE_ACCOUNT_CHANGED", "Cline configuration changed during the request; reload the account")

type clineMetadataRepository interface {
	ClaimClineMetadataRefresh(context.Context, *Account, time.Time) (bool, error)
	SaveClineStateIfUnchanged(context.Context, *Account, *ClineState) (bool, error)
}

type ClineMetadataWindow struct {
	Type        string     `json:"type"`
	PercentUsed *float64   `json:"percent_used"`
	ResetsAt    *time.Time `json:"resets_at,omitempty"`
}

// This explicit projection is the only metadata response exposed to the UI.
// Do not marshal ClineState or an Account into this response: they carry identity
// fingerprints and credentials that the panel does not need.
type ClineMetadataView struct {
	AutoRefresh        bool                  `json:"auto_refresh"`
	NextRefreshAt      *time.Time            `json:"next_refresh_at,omitempty"`
	Cooldowns          []ClineCooldownView   `json:"cooldowns"`
	RecoveryStatus     string                `json:"recovery_status"`
	Mode               string                `json:"mode"`
	AuthType           string                `json:"auth_type"`
	QuotaStatus        string                `json:"quota_status"`
	CredentialStatus   string                `json:"credential_status"`
	IdentityVerified   bool                  `json:"identity_verified"`
	Windows            []ClineMetadataWindow `json:"windows"`
	Catalog            *cline.Catalog        `json:"catalog,omitempty"`
	CatalogStatus      string                `json:"catalog_status"`
	CatalogFetchedAt   *time.Time            `json:"catalog_fetched_at,omitempty"`
	FetchedAt          *time.Time            `json:"fetched_at,omitempty"`
	LastSuccessAt      *time.Time            `json:"last_success_at,omitempty"`
	Persisted          bool                  `json:"persisted"`
	Error              string                `json:"error,omitempty"`
	IncrementalCostUSD *float64              `json:"incremental_cost_usd"`
}

func ClineMetadataForAccount(account *Account, now time.Time) *ClineMetadataView {
	view := &ClineMetadataView{Mode: cline.ModeUnknown, QuotaStatus: "unknown", CredentialStatus: "unknown", CatalogStatus: "unknown"}
	for _, name := range []string{"five_hour", "weekly", "monthly"} {
		view.Windows = append(view.Windows, ClineMetadataWindow{Type: name})
	}
	if !account.IsCline() {
		return view
	}
	view.Mode, view.AuthType = account.GetClineMode(), account.GetCredential("cline_auth_type")
	view.AutoRefresh = ClineMetadataAutoEligible(account, now)
	view.RecoveryStatus = "unknown"
	state := account.GetClineState()
	if state == nil {
		view.Cooldowns = clineCooldownViews(account, now)
		if len(view.Cooldowns) > 0 {
			view.RecoveryStatus = "pending_recheck"
		}
		return view
	}
	view.FetchedAt, view.LastSuccessAt, view.Persisted = state.FetchedAt, state.LastSuccessAt, state.Persisted
	view.NextRefreshAt = state.NextRefreshAt
	if view.Mode == cline.ModePass {
		view.Cooldowns = clineCooldownViews(account, now)
		if len(view.Cooldowns) > 0 {
			view.RecoveryStatus = "cooling"
			for _, block := range view.Cooldowns {
				if block.ResetAt == nil || !block.ResetAt.After(now) {
					view.RecoveryStatus = "pending_recheck"
				}
			}
		}
	}
	view.Error = safeClineMetadataCode(state.Error)
	view.Catalog, view.CatalogFetchedAt = state.Catalog, state.CatalogFetchedAt
	if state.Catalog != nil {
		view.CatalogStatus = "stale"
		if state.CatalogStatus == "ok" && clineDateFresh(state.CatalogFetchedAt, now, ClineCatalogFreshness) {
			view.CatalogStatus = "ok"
		}
	}
	view.IdentityVerified = cline.ValidSubjectHash(state.Identity) && clineDateFresh(state.IdentityVerifiedAt, now, ClineCatalogFreshness)
	if clineDateFresh(state.FetchedAt, now, ClineQuotaFreshness) {
		switch state.CredentialStatus {
		case "valid", "invalid":
			view.CredentialStatus = state.CredentialStatus
		}
	}
	if view.Mode != cline.ModePass {
		view.QuotaStatus = "not_applicable"
		return view
	}
	if state.QuotaStatus != "ok" || !state.Persisted || !clineDateFresh(state.LastSuccessAt, now, ClineQuotaFreshness) {
		return view
	}
	known := 0
	for n := range view.Windows {
		for _, window := range state.Windows {
			if window.Type == view.Windows[n].Type && window.PercentUsed >= 0 && window.PercentUsed <= 100 && (window.ResetsAt == nil || window.ResetsAt.After(now)) {
				value := window.PercentUsed
				view.Windows[n].PercentUsed, view.Windows[n].ResetsAt = &value, window.ResetsAt
				known++
				break
			}
		}
	}
	if known > 0 {
		view.QuotaStatus = "partial"
		if known == len(view.Windows) {
			view.QuotaStatus = "ok"
			if len(view.Cooldowns) == 0 && state.CredentialStatus == "valid" && ClineQuotaAdmission(account, now) == nil {
				view.RecoveryStatus = "available"
			}
		}
	}
	return view
}

func clineDateFresh(at *time.Time, now time.Time, ttl time.Duration) bool {
	if at == nil || at.After(now) {
		return false
	}
	return now.Sub(*at) < ttl
}

func safeClineMetadataCode(code string) string {
	switch code {
	case "catalog_unavailable", "profile_unavailable", "profile_schema", "usage_unavailable", "usage_schema", "unsupported_origin", "state_not_persisted":
		return code
	default:
		return ""
	}
}

// RefreshClineMetadata performs only bounded GETs to documented/optional fixed
// metadata endpoints. The public catalog receives no Authorization header.
// Opening/saving a form never calls upstream. The server worker also uses this
// path for active official Pass accounts, subject to the same database lease.
func (s *OpenAIGatewayService) RefreshClineMetadata(ctx context.Context, account *Account) (*ClineMetadataView, error) {
	return s.refreshClineMetadata(ctx, account, false)
}

func (s *OpenAIGatewayService) refreshClineMetadata(ctx context.Context, account *Account, automated bool) (*ClineMetadataView, error) {
	if s == nil || s.httpUpstream == nil || !account.IsCline() || account.Type != AccountTypeAPIKey {
		return nil, ErrClineMetadataUnavailable
	}
	repo, ok := s.accountRepo.(clineMetadataRepository)
	if !ok {
		return nil, ErrClineMetadataUnavailable
	}
	encoded, err := json.Marshal(account.Credentials)
	if err != nil {
		return nil, ErrClineMetadataUnavailable
	}
	var credentials map[string]any
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	if decoder.Decode(&credentials) != nil || NormalizeClineCredentials(PlatformCline, AccountTypeAPIKey, credentials) != nil {
		return nil, infraerrors.BadRequest("INVALID_CLINE_CONFIGURATION", "Correct the Cline account configuration before refreshing")
	}
	// Keep the exact stored representation for CAS, not the normalized map.
	credentials = nil
	decoder = json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	if decoder.Decode(&credentials) != nil {
		return nil, ErrClineMetadataUnavailable
	}
	snapshot := &Account{ID: account.ID, Platform: account.Platform, Type: account.Type, Credentials: credentials, ProxyID: account.ProxyID, Proxy: account.Proxy, Concurrency: account.Concurrency, Extra: account.Extra, Status: account.Status, Schedulable: account.Schedulable, AutoPauseOnExpired: account.AutoPauseOnExpired, ExpiresAt: account.ExpiresAt}
	if !cline.IsOfficialBase(snapshot.GetClineBaseURL()) {
		return nil, infraerrors.BadRequest("CLINE_METADATA_ORIGIN", "Cline metadata is supported only for the official Cline origin")
	}
	ctx, cancel := context.WithTimeout(ctx, ClineMetadataTimeout)
	defer cancel()
	now := time.Now().UTC()
	claimed, err := repo.ClaimClineMetadataRefresh(ctx, snapshot, now)
	if err != nil {
		return nil, ErrClineMetadataUnavailable
	}
	if !claimed {
		return nil, ErrClineRefreshTooSoon
	}
	state := snapshot.GetClineState()
	if state == nil {
		state = &ClineState{}
	}
	state.QuotaBlocks = ClineQuotaBlocksForState(state)
	wasInvalid := state.CredentialStatus == "invalid"
	state.Mode, state.AuthType, state.Transport = snapshot.GetClineMode(), snapshot.GetCredential("cline_auth_type"), "chat_completions"
	state.FetchedAt, state.ObservationUnixMS = &now, now.UnixMilli()
	state.CredentialFingerprint = ClineCredentialFingerprint(snapshot)
	state.QuotaStatus, state.CredentialStatus, state.CatalogStatus, state.Error = "unknown", "unknown", "unknown", ""
	if wasInvalid {
		state.CredentialStatus = "invalid"
	}
	state.Persisted = true
	var body []byte
	var status int
	var fetchErr error
	var metadataRetryAt *time.Time
	fetch := func(endpoint string, auth bool) ([]byte, int, error) {
		b, code, err := s.fetchClineMetadata(ctx, snapshot, endpoint, auth)
		var retry *clineMetadataRetryError
		if errors.As(err, &retry) && (metadataRetryAt == nil || retry.until.After(*metadataRetryAt)) {
			metadataRetryAt = &retry.until
		}
		return b, code, err
	}
	if automated && state.Catalog != nil && clineDateFresh(state.CatalogFetchedAt, now, ClineCatalogFreshness) {
		state.CatalogStatus = "ok"
	} else {
		body, status, fetchErr = fetch(cline.CatalogURL, false)
		if fetchErr == nil && status == http.StatusOK {
			if catalog, parseErr := cline.ParseCatalog(body); parseErr == nil {
				state.Catalog, state.CatalogFetchedAt, state.CatalogStatus = catalog, &now, "ok"
			} else {
				state.Error = "catalog_unavailable"
			}
		} else {
			state.Error = "catalog_unavailable"
		}
	}
	if metadataRetryAt == nil && (state.Mode == cline.ModePass || state.Mode == cline.ModePayG) {
		body, status, fetchErr = fetch(cline.ProfileURL, true)
		if fetchErr == nil && status == http.StatusOK {
			if subject, parseErr := cline.ParseSubjectHash(body); parseErr == nil {
				if state.Identity != "" && state.Identity != subject {
					state.Windows, state.LastSuccessAt, state.QuotaBlocks = nil, nil, nil
				}
				state.Identity, state.IdentityVerifiedAt, state.CredentialStatus = subject, &now, "valid"
			} else {
				state.Error = "profile_schema"
			}
		} else {
			state.Error = "profile_unavailable"
			if fetchErr == nil && status == http.StatusUnauthorized {
				state.CredentialStatus = "invalid"
			}
		}
		if metadataRetryAt == nil && state.Mode == cline.ModePass && state.CredentialStatus == "valid" && state.IdentityVerifiedAt != nil && state.IdentityVerifiedAt.Equal(now) {
			body, status, fetchErr = fetch(cline.UsageURL, true)
			if fetchErr == nil && status == http.StatusOK {
				if windows, parseErr := cline.ParseUsage(body); parseErr == nil {
					state.QuotaBlocks = mergeClineQuotaBlocks(state, windows, now)
					state.Windows, state.LastSuccessAt, state.QuotaStatus = windows, &now, "ok"
				} else {
					state.Error = "usage_schema"
				}
			} else {
				state.Error = "usage_unavailable"
			}
		}
	}
	state.MetadataRetryAt = metadataRetryAt
	if state.Mode == cline.ModePass {
		if state.QuotaStatus == "ok" && len(state.Windows) == 3 && state.CredentialStatus == "valid" {
			state.RefreshFailures = 0
		} else {
			state.RefreshFailures = min(max(state.RefreshFailures, 0)+1, 7)
		}
		next := ClineMetadataNextRefresh(snapshot, state, now)
		if metadataRetryAt != nil && metadataRetryAt.After(next) {
			next = *metadataRetryAt
		}
		state.NextRefreshAt = &next
	}
	// Do not convert metadata 403/429 into inference cooldowns or entitlement
	// changes. Metadata freshness and inference scheduling are separate domains.
	persistCtx, stop := context.WithTimeout(context.WithoutCancel(ctx), clineLimitPersistTimeout)
	defer stop()
	saved, err := repo.SaveClineStateIfUnchanged(persistCtx, snapshot, state)
	if err != nil {
		return nil, ErrClineMetadataUnavailable
	}
	if !saved {
		return nil, ErrClineMetadataChanged
	}
	snapshot.Extra = map[string]any{ClineStateExtraKey: state, "model_rate_limits": snapshot.Extra["model_rate_limits"]}
	return ClineMetadataForAccount(snapshot, time.Now().UTC()), nil
}

func (s *OpenAIGatewayService) fetchClineMetadata(ctx context.Context, account *Account, endpoint string, authenticated bool) ([]byte, int, error) {
	if endpoint != cline.CatalogURL && endpoint != cline.ProfileURL && endpoint != cline.UsageURL {
		return nil, 0, ErrClineMetadataUnavailable
	}
	if authenticated != (endpoint != cline.CatalogURL) {
		return nil, 0, ErrClineMetadataUnavailable
	}
	ctx = WithHTTPUpstreamPublicHostsOnly(WithHTTPUpstreamRedirectsDisabled(ctx))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, 0, ErrClineMetadataUnavailable
	}
	req.Header.Set("Accept", "application/json")
	if authenticated {
		key := strings.TrimSpace(account.GetCredential("api_key"))
		if key == "" || len(key) > 8192 || strings.ContainsAny(key, "\r\n\x00") {
			return nil, 0, ErrClineMetadataUnavailable
		}
		req.Header.Set("Authorization", "Bearer "+key)
	}
	proxyURL := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}
	resp, err := s.httpUpstream.Do(req, proxyURL, account.ID, 1)
	if err != nil {
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
		return nil, 0, ErrClineMetadataUnavailable
	}
	if resp == nil || resp.Body == nil {
		return nil, 0, ErrClineMetadataUnavailable
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusServiceUnavailable {
			return nil, resp.StatusCode, &clineMetadataRetryError{until: clineMetadataRetryAt(resp.Header, time.Now().UTC())}
		}
		return nil, resp.StatusCode, nil
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, cline.MaxBodyBytes+1))
	if err != nil || len(body) > cline.MaxBodyBytes {
		return nil, resp.StatusCode, ErrClineMetadataUnavailable
	}
	return body, resp.StatusCode, nil
}
