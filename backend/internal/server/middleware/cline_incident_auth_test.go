//go:build unit

package middleware

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestClineIncidentAPIKeyDiagnosticsTrackActualSelection(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name, source, selected string
		google                 bool
		headers                map[string]string
		backend                error
		status                 int
	}{
		{"bearer precedence", "authorization_bearer", "private-A", false, map[string]string{"Authorization": "Bearer private-A", "x-api-key": "private-B"}, nil, 200},
		{"bearer trim unchanged", "authorization_bearer", "private-A", false, map[string]string{"Authorization": "Bearer  private-A \t"}, nil, 200},
		{"x key preserves spaces", "x_api_key", " private-B ", false, map[string]string{"Authorization": "Basic unused", "x-api-key": " private-B "}, service.ErrAPIKeyNotFound, 401},
		{"google precedence", "x_goog_api_key", "private-G", true, map[string]string{"Authorization": "Bearer private-A", "x-api-key": "private-B", "x-goog-api-key": " private-G "}, nil, 200},
		{"google bearer", "authorization_bearer", "private-A", true, map[string]string{"Authorization": "Bearer private-A"}, service.ErrAPIKeyNotFound, 401},
		{"backend error not invalid key", "x_api_key", "private-B", false, map[string]string{"x-api-key": "private-B"}, errors.New("database unavailable"), 500},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var selected string
			var captured apiKeyCredentialDiagnostic
			user := &service.User{ID: 7, Status: service.StatusActive, Role: service.RoleUser, Balance: 10, Concurrency: 3}
			group := &service.Group{ID: 42, Name: "test", Status: service.StatusActive, Hydrated: true}
			repo := &stubApiKeyRepo{getByKey: func(_ context.Context, key string) (*service.APIKey, error) {
				selected = key
				if tc.backend != nil {
					return nil, tc.backend
				}
				return &service.APIKey{ID: 9, UserID: user.ID, Key: key, Status: service.StatusActive, User: user, Group: group, GroupID: &group.ID}, nil
			}}
			cfg := &config.Config{RunMode: config.RunModeSimple}
			svc := service.NewAPIKeyService(repo, nil, nil, nil, nil, nil, cfg)
			router := gin.New()
			router.Use(func(c *gin.Context) {
				c.Next()
				v, ok := c.Get(apiKeyDiagnosticContextKey)
				if ok {
					captured = v.(apiKeyCredentialDiagnostic)
				}
			})
			if tc.google {
				router.Use(APIKeyAuthGoogle(svc, cfg))
			} else {
				router.Use(gin.HandlerFunc(NewAPIKeyAuthMiddleware(svc, nil, cfg)))
			}
			router.GET("/t", func(c *gin.Context) { c.Status(200) })
			req := httptest.NewRequest(http.MethodGet, "/t", nil)
			for name, value := range tc.headers {
				req.Header.Set(name, value)
			}
			res := httptest.NewRecorder()
			router.ServeHTTP(res, req)
			require.Equal(t, tc.status, res.Code, res.Body.String())
			require.Equal(t, tc.selected, selected)
			require.Equal(t, tc.source, captured.Source)
			require.Equal(t, len(selected), captured.Length)
			hash := sha256.Sum256([]byte(selected))
			require.Equal(t, hex.EncodeToString(hash[:16]), captured.Fingerprint)
			require.NotContains(t, fmt.Sprintf("%+v", captured), selected)
			if tc.backend == nil {
				require.Equal(t, "matched", captured.Lookup)
				require.Equal(t, int64(9), captured.KeyID)
			}
			if errors.Is(tc.backend, service.ErrAPIKeyNotFound) {
				require.Equal(t, "not_found", captured.Lookup)
				require.Zero(t, captured.KeyID)
			}
			if tc.status == 500 {
				require.Equal(t, "backend_error", captured.Lookup)
			}
		})
	}
}

func TestClineIncidentAPIKeyDiagnosticBoundsAndDuplicates(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	c.Request.Header.Add("Authorization", "Bearer private-one")
	c.Request.Header.Add("Authorization", "Bearer private-two")
	recordAPIKeyCredentialDiagnostic(c, "authorization_bearer", "private-one")
	v, _ := c.Get(apiKeyDiagnosticContextKey)
	d := v.(apiKeyCredentialDiagnostic)
	require.Equal(t, 2, d.AuthorizationCount)
	require.Len(t, d.Fingerprint, 32)
	require.NotContains(t, fmt.Sprint(apiKeyCredentialDiagnosticFields(c)), "private-one")
	recordAPIKeyCredentialDiagnostic(c, "x_api_key", strings.Repeat("x", service.MaxAPIKeyCredentialBytes+1))
	v, _ = c.Get(apiKeyDiagnosticContextKey)
	require.Empty(t, v.(apiKeyCredentialDiagnostic).Fingerprint)
}
