package service

import (
	"encoding/json"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/cline"
)

// ClineState is a credential-bound observation, not a grant of API entitlement.
// A failed refresh preserves last-good observations with their original dates.
// Identity is a hash of the verified active upstream account, never a raw ID.
type ClineState struct {
	MetadataRetryAt       *time.Time        `json:"metadata_retry_at,omitempty"`
	QuotaBlocks           []ClineQuotaBlock `json:"quota_blocks,omitempty"`
	NextRefreshAt         *time.Time        `json:"next_refresh_at,omitempty"`
	RefreshFailures       int               `json:"refresh_failures,omitempty"`
	Mode                  string            `json:"mode"`
	AuthType              string            `json:"auth_type"`
	Transport             string            `json:"transport"`
	Identity              string            `json:"identity"`
	IdentityVerifiedAt    *time.Time        `json:"identity_verified_at,omitempty"`
	CredentialStatus      string            `json:"credential_status"`
	QuotaStatus           string            `json:"quota_status"`
	Windows               []cline.Window    `json:"windows"`
	Catalog               *cline.Catalog    `json:"catalog,omitempty"`
	CatalogStatus         string            `json:"catalog_status"`
	CatalogFetchedAt      *time.Time        `json:"catalog_fetched_at,omitempty"`
	FetchedAt             *time.Time        `json:"fetched_at,omitempty"`
	LastSuccessAt         *time.Time        `json:"last_success_at,omitempty"`
	ObservationUnixMS     int64             `json:"observation_unix_ms"`
	Error                 string            `json:"error,omitempty"`
	Persisted             bool              `json:"persisted"`
	CredentialFingerprint string            `json:"credential_fingerprint,omitempty"`
}

func (a *Account) GetClineState() *ClineState {
	if !a.IsCline() {
		return nil
	}
	raw, ok := a.Extra[ClineStateExtraKey]
	if !ok {
		return nil
	}
	body, err := json.Marshal(raw)
	if err != nil {
		return nil
	}
	var state ClineState
	if json.Unmarshal(body, &state) != nil || state.FetchedAt == nil || state.CredentialFingerprint != ClineCredentialFingerprint(a) {
		return nil
	}
	return &state
}
