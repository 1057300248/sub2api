package clinemigration

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/cline"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClineMigrationExactHostAndExplicitModels(t *testing.T) {
	base := &snapshot{Platform: service.PlatformDeepseek, Type: service.AccountTypeAPIKey, Credentials: map[string]any{"api_key": "synthetic-migration-key", "base_url": cline.BaseURL, "model_mapping": map[string]any{"alias": "vendor/model"}}, Extra: map[string]any{}}
	spec := Spec{AccountID: 1, Mode: cline.ModePass, AuthType: cline.AuthAPIKey, ModelMapping: map[string]string{"alias": "cline-pass/model"}}
	for _, host := range []string{"", "https://api.deepseek.com", "https://api.cline.bot.attacker.invalid/api/v1", "http://api.cline.bot", "https://key@api.cline.bot", "https://api.cline.bot/api/v1?x=1"} {
		base.Credentials["base_url"] = host
		require.False(t, eligible(base), host)
	}
	base.Credentials["base_url"] = cline.BaseURL
	target, err := targetCredentials(base, spec)
	require.NoError(t, err)
	require.Equal(t, "synthetic-migration-key", target["api_key"])
	require.Equal(t, cline.ModePass, target["account_mode"])
	require.NotContains(t, base.Credentials, "account_mode")
	spec.ModelMapping = map[string]string{"new-public-alias": "cline-pass/model"}
	_, err = targetCredentials(base, spec)
	require.ErrorIs(t, err, ErrSpecification)
}

func TestClineMigrationPlanApprovalIsBoundToEveryInput(t *testing.T) {
	plan := Plan{Version: 1, Spec: Spec{AccountID: 1, Mode: cline.ModePass, AuthType: cline.AuthAPIKey, ModelMapping: map[string]string{"alias": "cline-pass/model"}}, BeforeDigest: "state-digest", Warning: PlanWarning}
	var err error
	plan.Approval, err = planDigest(plan)
	require.NoError(t, err)
	require.NoError(t, verifyApproval(plan, plan.Approval, MaintenanceAcknowledgement))
	require.ErrorIs(t, verifyApproval(plan, plan.Approval, ""), ErrApproval)
	plan.Spec.Mode = cline.ModePayG
	require.ErrorIs(t, verifyApproval(plan, plan.Approval, MaintenanceAcknowledgement), ErrApproval)
	require.NotContains(t, SafeError(errors.New("postgres password=secret")), "secret")
}

func TestClineMigrationBindingPolicyPreservesRestrictions(t *testing.T) {
	before := &snapshot{Bindings: []binding{{GroupID: 1, Priority: 17, AllowedModels: json.RawMessage(`["only-this-model"]`)}}, Groups: json.RawMessage(`[{"id":1,"platform":"deepseek"},{"id":2,"platform":"cline"}]`)}
	spec := Spec{GroupMoves: []GroupMove{{From: 1, To: 2}}}
	after, err := targetBindings(before, spec)
	require.NoError(t, err)
	require.Equal(t, 2, int(after[0].GroupID))
	require.Equal(t, 17, after[0].Priority)
	require.JSONEq(t, string(before.Bindings[0].AllowedModels), string(after[0].AllowedModels))
	spec.GroupMoves[0].To = 1
	_, err = targetBindings(before, spec)
	require.ErrorIs(t, err, ErrSpecification)
}

func TestClineMigrationExplicitPassExclusions(t *testing.T) {
	before := &snapshot{Platform: service.PlatformDeepseek, Type: service.AccountTypeAPIKey, Credentials: map[string]any{
		"api_key": "synthetic-migration-key", "base_url": cline.BaseURL,
		"model_mapping": map[string]string{"pass": "cline-pass/model", "free": "vmc/free-model"},
	}}
	spec := Spec{AccountID: 1, Mode: cline.ModePass, AuthType: cline.AuthAPIKey, ModelMapping: map[string]string{"pass": "cline-pass/model"}, ExcludedModels: []string{"free"}}
	require.NoError(t, validateSpec(spec))
	target, err := targetCredentials(before, spec)
	require.NoError(t, err)
	assert.Equal(t, spec.ModelMapping, target["model_mapping"])
	assert.Equal(t, map[string]string{"pass": "cline-pass/model", "free": "vmc/free-model"}, before.Credentials["model_mapping"])
	for _, tc := range []struct {
		name     string
		excluded []string
		mapping  map[string]string
		mode     string
	}{
		{"missing explicit exclusion", nil, spec.ModelMapping, cline.ModePass},
		{"unknown exclusion", []string{"unknown"}, spec.ModelMapping, cline.ModePass},
		{"duplicate exclusion", []string{"free", "free"}, spec.ModelMapping, cline.ModePass},
		{"cannot exclude valid Pass", []string{"pass"}, map[string]string{"free": "cline-pass/model"}, cline.ModePass},
		{"cannot exclude for PAYG", []string{"free"}, map[string]string{"pass": "vendor/model"}, cline.ModePayG},
	} {
		t.Run(tc.name, func(t *testing.T) {
			candidate := spec
			candidate.ExcludedModels, candidate.ModelMapping, candidate.Mode = tc.excluded, tc.mapping, tc.mode
			err := validateSpec(candidate)
			if err == nil {
				_, err = targetCredentials(before, candidate)
			}
			require.ErrorIs(t, err, ErrSpecification)
		})
	}
}
