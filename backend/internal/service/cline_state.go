package service

import (
	"encoding/json"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/cline"
)

// ClineState is a cached observation, not a declaration of subscription rights.
// Missing or expired observations must never be displayed as zero usage.
type ClineState struct {
	Mode                  string         `json:"mode"`
	AuthType              string         `json:"auth_type"`
	Transport             string         `json:"transport"`
	Identity              string         `json:"identity"`
	CredentialStatus      string         `json:"credential_status"`
	QuotaStatus           string         `json:"quota_status"`
	Windows               []cline.Window `json:"windows"`
	Catalog               *cline.Catalog `json:"catalog,omitempty"`
	CatalogFetchedAt      *time.Time     `json:"catalog_fetched_at,omitempty"`
	FetchedAt             *time.Time     `json:"fetched_at,omitempty"`
	LastSuccessAt         *time.Time     `json:"last_success_at,omitempty"`
	Error                 string         `json:"error,omitempty"`
	Persisted             bool           `json:"persisted"`
	CredentialFingerprint string         `json:"credential_fingerprint,omitempty"`
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
