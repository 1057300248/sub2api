package clinemigration

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/cline"
	"github.com/Wei-Shaw/sub2api/internal/service"
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
