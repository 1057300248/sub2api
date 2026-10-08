package service

import (
	"context"
	"net/http"
	"sort"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/cline"
)

// Cline plugs into the existing account model-sync endpoints and transport.
// The public catalog is a mode-specific list, NOT proof of credential entitlement.
// It must not modify models, credentials, billing, quota state or scheduling.
func (s *AccountTestService) syncClineUpstreamModelCatalog(ctx context.Context, account *Account) (*UpstreamModelCatalog, error) {
	// Validate provider connection fields without requiring a working model map:
	// this action also repairs malformed or obsolete saved whitelist entries.
	credentials := map[string]any{
		"api_key":         account.GetCredential("api_key"),
		"base_url":        account.GetCredential("base_url"),
		"account_mode":    account.GetCredential("account_mode"),
		"cline_auth_type": account.GetCredential("cline_auth_type"),
	}
	if err := NormalizeClineCredentials(PlatformCline, account.Type, credentials); err != nil {
		return nil, newUpstreamModelSyncConfigError("Invalid Cline connection settings", nil)
	}
	mode := cline.NormalizeMode(account.GetCredential("account_mode"))
	if mode == cline.ModeUnknown {
		return nil, newUpstreamModelSyncConfigError("Choose a Cline account mode before syncing models", nil)
	}
	if !cline.IsOfficialBase(account.GetClineBaseURL()) {
		return nil, newUpstreamModelSyncUnsupportedError("Automatic Cline catalog discovery is available only for the official API origin; configure custom-origin models explicitly", nil)
	}
	if s.openaiGatewayService == nil || s.openaiGatewayService.httpUpstream == nil {
		return nil, newUpstreamModelSyncInternalError("Cline metadata transport is not configured", nil)
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	body, status, err := s.openaiGatewayService.fetchClineMetadata(ctx, account, cline.CatalogURL, false)
	if err != nil || status != http.StatusOK {
		return nil, newUpstreamModelSyncUpstreamError("Failed to read Cline model catalog", nil)
	}
	catalog, err := cline.ParseCatalog(body)
	if err != nil {
		return nil, newUpstreamModelSyncUpstreamError("Cline returned an invalid model catalog", nil)
	}
	models := make([]string, 0, len(catalog.Models(mode)))
	for _, model := range catalog.Models(mode) {
		if err := cline.ValidateUpstreamModel(mode, model.ID); err != nil && mode != cline.ModeFree {
			return nil, newUpstreamModelSyncUpstreamError("Cline returned a model outside the selected mode", nil)
		}
		models = append(models, model.ID)
	}
	if len(models) == 0 {
		return nil, newUpstreamModelSyncUpstreamError("Cline returned no models for the selected mode", nil)
	}
	sort.Strings(models)
	return &UpstreamModelCatalog{Models: models, Warnings: []UpstreamModelSyncWarning{{
		Code: "cline_public_catalog", Message: "Public Cline catalog only: model listing does not establish account entitlement, remaining quota or API access. Free API forwarding remains disabled.",
	}}}, nil
}
