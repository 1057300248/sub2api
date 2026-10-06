package repository

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Wei-Shaw/sub2api/internal/pkg/cline"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

// Last completed observation only, credential-bound, not a quota/entitlement
// write. Older completion reports cannot replace newer ones. This key is kept
// outside cline_state so overlapping metadata refreshes cannot erase it.
func (r *accountRepository) SaveClineRouteObservation(ctx context.Context, a *service.Account, observation cline.ProviderObservation) error {
	if !a.IsCline() || !cline.ValidProviderObservation(&observation) {
		return fmt.Errorf("invalid Cline route observation")
	}
	credentials, err := json.Marshal(a.Credentials)
	if err != nil {
		return err
	}
	state := service.ClineRouteState{CredentialFingerprint: service.ClineCredentialFingerprint(a), Observation: observation, ObservedUnixNS: observation.ObservedAt.UnixNano()}
	payload, err := json.Marshal(state)
	if err != nil {
		return err
	}
	_, err = clientFromContext(ctx, r.client).ExecContext(ctx, `UPDATE accounts SET extra=jsonb_set(COALESCE(extra,'{}'::jsonb),'{cline_route}',$3::jsonb,true)
 WHERE id=$1 AND platform='cline' AND credentials=$2::jsonb AND deleted_at IS NULL
 AND (extra#>>'{cline_route,credential_fingerprint}' IS DISTINCT FROM $4
 OR CASE WHEN jsonb_typeof(extra#>'{cline_route,observed_unix_ns}')='number'
 THEN (extra#>>'{cline_route,observed_unix_ns}')::numeric<=$5 ELSE true END)`, a.ID, string(credentials), string(payload), state.CredentialFingerprint, state.ObservedUnixNS)
	return err
}
