package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime"
	"net/http"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/cline"
)

const clineJSONResponseLimit int64 = 16 << 20
const clineLimitPersistTimeout = 5 * time.Second

// prepareClineResponseGuard must run before sending the upstream request. Its
// detached snapshot is the exact credentials predicate used by the repository
// CAS, even if an operator rotates a key while the response is being read.
// The returned guard never replays a request or changes another platform.
func (s *OpenAIGatewayService) prepareClineResponseGuard(ctx context.Context, account *Account, requestBody []byte, stream bool) (func(*http.Response), error) {
	if !account.IsCline() {
		return nil, nil
	}
	credentials, err := json.Marshal(account.Credentials)
	if err != nil {
		return nil, fmt.Errorf("snapshot Cline request credentials: %w", err)
	}
	var snapshotCredentials map[string]any
	decoder := json.NewDecoder(bytes.NewReader(credentials))
	decoder.UseNumber()
	if err := decoder.Decode(&snapshotCredentials); err != nil {
		return nil, fmt.Errorf("snapshot Cline request credentials: %w", err)
	}
	snapshot := &Account{ID: account.ID, Platform: account.Platform, Type: account.Type, Credentials: snapshotCredentials}
	if state := account.GetClineState(); state != nil {
		snapshot.Extra = map[string]any{ClineStateExtraKey: state}
	}
	var request struct {
		Model string `json:"model"`
	}
	if json.Unmarshal(requestBody, &request) != nil || !cline.ValidModelID(request.Model) {
		return nil, fmt.Errorf("invalid Cline request model for response guard")
	}
	lineLimit := defaultMaxLineSize
	if s != nil && s.cfg != nil && s.cfg.Gateway.MaxLineSize > 0 {
		lineLimit = s.cfg.Gateway.MaxLineSize
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return func(resp *http.Response) {
		if resp == nil || resp.Body == nil || resp.StatusCode >= 400 {
			return
		}
		headers := resp.Header.Clone()
		onError := func(payload []byte) {
			status, confirmed := cline.GenerationErrorStatus(payload)
			if !confirmed || s == nil || s.rateLimitService == nil {
				return
			}
			persistCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), clineLimitPersistTimeout)
			defer cancel()
			s.rateLimitService.handleClineScopedUpstreamError(persistCtx, snapshot, status, headers, payload, request.Model)
		}
		mediaType, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
		if stream && (strings.EqualFold(mediaType, "text/event-stream") || strings.TrimSpace(resp.Header.Get("Content-Type")) == "") {
			resp.Body = cline.GuardSSEBody(resp.Body, lineLimit, onError)
		} else {
			// Normalization may remove an envelope; never forward its old length
			// or representation validators with the normalized response body.
			resp.ContentLength = -1
			for _, header := range []string{"Content-Length", "Content-MD5", "Digest", "ETag"} {
				resp.Header.Del(header)
			}
			resp.Body = cline.GuardJSONBody(resp.Body, clineJSONResponseLimit, stream, onError)
		}
	}, nil
}
