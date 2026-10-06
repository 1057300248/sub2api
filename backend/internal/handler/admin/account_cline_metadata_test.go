//go:build unit

package admin

import (
	"context"
	"github.com/Wei-Shaw/sub2api/internal/pkg/cline"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"net/http/httptest"
	"testing"
)

type clineMetadataAdminStub struct {
	service.AdminService
	account *service.Account
	calls   int
}

func (s *clineMetadataAdminStub) GetAccount(context.Context, int64) (*service.Account, error) {
	s.calls++
	return s.account, nil
}
func TestClineMetadataHandlerIsLocalAndRedacted(t *testing.T) {
	a := &service.Account{ID: 42, Platform: service.PlatformCline, Type: service.AccountTypeAPIKey, Credentials: map[string]any{"api_key": "never-expose-key", "account_mode": cline.ModePass, "cline_auth_type": cline.AuthAPIKey}, Extra: map[string]any{}}
	stub := &clineMetadataAdminStub{account: a}
	h := &AccountHandler{adminService: stub}
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest("GET", "/42/cline/state", nil)
	c.Params = gin.Params{{Key: "id", Value: "42"}}
	h.GetClineMetadata(c)
	require.Equal(t, 200, rec.Code)
	require.Equal(t, 1, stub.calls)
	require.Equal(t, "no-store", rec.Header().Get("Cache-Control"))
	require.Contains(t, rec.Body.String(), `"percent_used":null`)
	require.Contains(t, rec.Body.String(), `"quota_status":"unknown"`)
	require.NotContains(t, rec.Body.String(), "never-expose-key")
	require.NotContains(t, rec.Body.String(), "credential_fingerprint")
	rec = httptest.NewRecorder()
	c, _ = gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest("POST", "/42/cline/refresh", nil)
	c.Params = gin.Params{{Key: "id", Value: "42"}}
	h.RefreshClineMetadata(c)
	require.Equal(t, 503, rec.Code)
}
func TestClinePortableDataRetainsModesButNotManagedGrants(t *testing.T) {
	item := DataAccount{Name: "fixture", Platform: service.PlatformCline, Type: service.AccountTypeAPIKey, Credentials: map[string]any{"api_key": "synthetic-backup-key", "account_mode": cline.ModePass, "cline_auth_type": cline.AuthAccountToken, "model_mapping": map[string]any{"alias": "cline-pass/model"}}}
	require.NoError(t, validateDataAccount(item))
	require.NotContains(t, item.Credentials, "base_url")
	extra := map[string]any{"cline_state": "private-observation", "model_rate_limits": "local-only", "unrelated": "keep"}
	require.Equal(t, map[string]any{"unrelated": "keep"}, portableClineExtra(item.Platform, extra))
	require.Contains(t, extra, "cline_state")
	require.Equal(t, cline.ModePass, item.Credentials["account_mode"])
	require.Equal(t, cline.AuthAccountToken, item.Credentials["cline_auth_type"])
	item.Credentials["api_protocol"] = "responses"
	require.Error(t, validateDataAccount(item))
	item.Platform = service.PlatformDeepseek
	require.Equal(t, extra, portableClineExtra(item.Platform, extra))
}
