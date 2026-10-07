package service

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/cline"
)

const ClineRouteExtraKey = "cline_route"

type ClineRouteState struct {
	ObservedUnixNS        int64                     `json:"observed_unix_ns"`
	CredentialFingerprint string                    `json:"credential_fingerprint"`
	Observation           cline.ProviderObservation `json:"observation"`
}
type clineRouteRepository interface {
	SaveClineRouteObservation(context.Context, *Account, cline.ProviderObservation) error
}

func ClineRouteForAccount(a *Account, now time.Time) *cline.ProviderObservation {
	if !a.IsCline() {
		return nil
	}
	raw, err := json.Marshal(a.Extra[ClineRouteExtraKey])
	if err != nil {
		return nil
	}
	var state ClineRouteState
	if json.Unmarshal(raw, &state) != nil || state.CredentialFingerprint != ClineCredentialFingerprint(a) || !cline.ValidProviderObservation(&state.Observation) || state.Observation.ObservedAt.After(now.Add(2*time.Minute)) {
		return nil
	}
	return &state.Observation
}
func (s *OpenAIGatewayService) observeClineProviderResponse(ctx context.Context, a *Account, body []byte, stream bool, resp *http.Response) {
	if !isClineProtocolAccount(a) || resp == nil || resp.Body == nil {
		return
	}
	// Capture the exact credentials before consumption; late completion after a
	// rotation cannot publish observations for the replacement account identity.
	raw, err := json.Marshal(a.Credentials)
	if err != nil {
		return
	}
	snapshot := &Account{ID: a.ID, Platform: a.Platform, Type: a.Type}
	if json.Unmarshal(raw, &snapshot.Credentials) != nil {
		return
	}
	observation := cline.RequestedProviderObservation(body)
	observation.HTTPStatus = resp.StatusCode
	resp.Body = cline.ObserveProviderBody(resp.Body, stream, observation, func(result cline.ProviderObservation) {
		slog.Info("cline.provider_observation", "account_id", snapshot.ID, "model", result.UpstreamModel, "constraint", result.ConstraintStatus, "requested", result.Requested, "actual", result.Actual, "status", result.Status, "source", result.Source, "complete", result.Complete)
		repo, ok := s.accountRepo.(clineRouteRepository)
		if !ok || !snapshot.IsCline() {
			return
		}
		persist, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer cancel()
		if err := repo.SaveClineRouteObservation(persist, snapshot, result); err != nil {
			slog.Warn("cline.provider_observation_not_saved", "account_id", snapshot.ID)
		}
	})
}

type ClineModelView struct {
	ID               string                   `json:"id"`
	Listed           bool                     `json:"listed"`
	Configured       bool                     `json:"configured"`
	Capabilities     *cline.ModelCapabilities `json:"capabilities,omitempty"`
	CapabilityStatus string                   `json:"capability_status"`
	Entitlement      string                   `json:"entitlement"`
	LastSuccessfulAt *time.Time               `json:"last_successful_at,omitempty"`
}

func clineModelViews(a *Account, state *ClineState, now time.Time) []ClineModelView {
	if state == nil || state.Catalog == nil {
		return nil
	}
	mapped := map[string]bool{}
	for _, id := range a.GetModelMapping() {
		mapped[id] = true
	}
	observed := ClineRouteForAccount(a, now)
	models := make([]ClineModelView, 0)
	for _, model := range state.Catalog.Models(a.GetClineMode()) {
		v := ClineModelView{ID: model.ID, Listed: true, Configured: mapped[model.ID], Capabilities: model.Capabilities, CapabilityStatus: "unknown", Entitlement: "unknown"}
		if v.Capabilities != nil {
			v.CapabilityStatus = "reported"
			if !clineDateFresh(state.CatalogFetchedAt, now, ClineCatalogFreshness) {
				v.CapabilityStatus = "stale"
			}
		}
		if observed != nil && observed.Complete && observed.UpstreamModel == model.ID {
			at := observed.ObservedAt
			v.LastSuccessfulAt = &at
		}
		models = append(models, v)
	}
	return models
}
