package admin

import (
	"testing"

	"github.com/gin-gonic/gin/binding"
	"github.com/stretchr/testify/require"
)

func TestClineGroupAndCompositeRequestBindings(t *testing.T) {
	require.NoError(t, binding.Validator.ValidateStruct(CreateGroupRequest{Name: "Cline", Platform: "cline"}))
	require.NoError(t, binding.Validator.ValidateStruct(UpdateGroupRequest{Platform: "cline"}))
	require.NoError(t, binding.Validator.ValidateStruct(CompositeRouteRequest{PublicModel: "model", TargetPlatform: "cline", UpstreamModel: "cline-pass/model", Endpoint: "responses"}))
	require.Error(t, binding.Validator.ValidateStruct(CreateGroupRequest{Name: "Cline", Platform: "cline-typo"}))
	require.Error(t, binding.Validator.ValidateStruct(CompositeRouteRequest{PublicModel: "model", TargetPlatform: "composite"}))
}
