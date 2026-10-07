package middleware

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

const apiKeyDiagnosticContextKey = "api_key_credential_diagnostic"

type apiKeyCredentialDiagnostic struct {
	Source             string
	Length             int
	Fingerprint        string
	AuthorizationCount int
	APIKeyCount        int
	GoogleKeyCount     int
	Lookup             string
	KeyID              int64
}

// Capture the exact value selected by authentication, not a second parser in
// the access logger. Never store the credential or log headers/query strings.
// The existing rejection sampler still bounds log volume; diagnostics do not
// change authentication precedence, whitespace handling or cache behavior.
func recordAPIKeyCredentialDiagnostic(c *gin.Context, source, credential string) {
	if c == nil || c.Request == nil {
		return
	}
	d := apiKeyCredentialDiagnostic{Source: source, Length: len(credential), Lookup: "not_attempted",
		AuthorizationCount: len(c.Request.Header.Values("Authorization")),
		APIKeyCount:        len(c.Request.Header.Values("x-api-key")),
		GoogleKeyCount:     len(c.Request.Header.Values("x-goog-api-key"))}
	if credential == "" {
		d.Source = "none"
	}
	if len(credential) > 0 && len(credential) <= service.MaxAPIKeyCredentialBytes {
		sum := sha256.Sum256([]byte(credential))
		d.Fingerprint = hex.EncodeToString(sum[:16])
	}
	c.Set(apiKeyDiagnosticContextKey, d)
}

func recordAPIKeyLookupDiagnostic(c *gin.Context, key *service.APIKey, err error) {
	value, ok := c.Get(apiKeyDiagnosticContextKey)
	if !ok {
		return
	}
	d, ok := value.(apiKeyCredentialDiagnostic)
	if !ok {
		return
	}
	switch {
	case errors.Is(err, service.ErrAPIKeyNotFound):
		d.Lookup = "not_found"
	case errors.Is(err, service.ErrAPIKeyAuthOverloaded):
		d.Lookup = "overloaded"
	case err != nil:
		d.Lookup = "backend_error"
	case key != nil:
		d.Lookup = "matched"
		d.KeyID = key.ID
	default:
		d.Lookup = "empty_result"
	}
	c.Set(apiKeyDiagnosticContextKey, d)
}

func apiKeyCredentialDiagnosticFields(c *gin.Context) []zap.Field {
	value, ok := c.Get(apiKeyDiagnosticContextKey)
	if !ok {
		return nil
	}
	d, ok := value.(apiKeyCredentialDiagnostic)
	if !ok {
		return nil
	}
	return []zap.Field{
		zap.String("auth_key_source", d.Source), zap.Int("auth_key_length", d.Length),
		zap.String("auth_key_sha256_128", d.Fingerprint), zap.String("auth_key_lookup", d.Lookup),
		zap.Int64("auth_key_id", d.KeyID), zap.Int("authorization_header_count", d.AuthorizationCount),
		zap.Int("x_api_key_header_count", d.APIKeyCount), zap.Int("x_goog_api_key_header_count", d.GoogleKeyCount),
	}
}
