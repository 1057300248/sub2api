package admin

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// Exercise the real HTTP bind/handler rather than only decoding a DTO. A missing
// field on either side of the handler used to silently ignore schedulable=false.
func TestClineAccountSettingsHTTPBinding(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := newStubAdminService()
	svc.getAccountResult = &service.Account{ID: 7, Platform: service.PlatformCline, Type: service.AccountTypeAPIKey, Status: service.StatusActive}
	h := NewAccountHandler(svc, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	r := gin.New()
	r.POST("/accounts", h.Create)
	r.PUT("/accounts/:id", h.Update)
	create := `{"name":"Cline fixture","platform":"cline","type":"apikey","credentials":{"api_key":"fixture","account_mode":"pass","cline_auth_type":"api_key","model_mapping":{"public":"cline-pass/model"}},"schedulable":false,"proxy_id":9,"rate_multiplier":0,"group_rate_multiplier":2.5,"load_factor":7,"expires_at":1917523424,"auto_pause_on_expired":false,"extra":{"cost_multiplier":0.2}}`
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/accounts", bytes.NewBufferString(create))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.Len(t, svc.createdAccounts, 1)
	in := svc.createdAccounts[0]
	require.NotNil(t, in.Schedulable)
	require.False(t, *in.Schedulable)
	require.Equal(t, int64(9), *in.ProxyID)
	require.Equal(t, 0.0, *in.RateMultiplier)
	require.Equal(t, 2.5, *in.GroupRateMultiplier)
	require.Equal(t, 7, *in.LoadFactor)
	require.Equal(t, int64(1917523424), *in.ExpiresAt)
	require.False(t, *in.AutoPauseOnExpired)
	require.Equal(t, 0.2, in.Extra["cost_multiplier"])
	update := `{"notes":"","schedulable":false,"proxy_id":0,"load_factor":0,"expires_at":0,"status":"inactive","auto_pause_on_expired":false,"rate_multiplier":0,"group_rate_multiplier":0,"group_allowed_models":{"41":["public"]},"credentials":{"header_override_enabled":true,"header_overrides":{"User-Agent":"fixture/1"}},"extra":{"cost_multiplier":0}}`
	w = httptest.NewRecorder()
	req = httptest.NewRequest("PUT", "/accounts/7", bytes.NewBufferString(update))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.Equal(t, 1, svc.updateAccountCalls)
	out := svc.lastUpdateAccountInput
	require.NotNil(t, out.Schedulable)
	require.False(t, *out.Schedulable)
	require.NotNil(t, out.Notes)
	require.Empty(t, *out.Notes)
	require.Zero(t, *out.ProxyID)
	require.Zero(t, *out.LoadFactor)
	require.Zero(t, *out.ExpiresAt)
	require.Zero(t, *out.RateMultiplier)
	require.Zero(t, *out.GroupRateMultiplier)
	require.Equal(t, "inactive", out.Status)
	require.Equal(t, map[int64][]string{41: {"public"}}, out.GroupAllowedModels)
	require.Equal(t, map[string]any{"header_override_enabled": true, "header_overrides": map[string]any{"User-Agent": "fixture/1"}}, out.Credentials)
	require.Equal(t, float64(0), out.Extra["cost_multiplier"])
}
