package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/cline"
	"github.com/stretchr/testify/require"
)

type clineGuardLimitObservation struct {
	account       *Account
	scope         string
	until         time.Time
	reason        string
	authoritative bool
	contextErr    error
	deadline      time.Time
}

type clineGuardLimitRepository struct {
	AccountRepository
	observations []clineGuardLimitObservation
}

func (r *clineGuardLimitRepository) SetClineRateLimitIfLater(ctx context.Context, account *Account, scope string, until time.Time, reason string, authoritative bool) error {
	deadline, _ := ctx.Deadline()
	r.observations = append(r.observations, clineGuardLimitObservation{account, scope, until, reason, authoritative, ctx.Err(), deadline})
	return nil
}

func clineGuardResponse(t *testing.T, guard func(*http.Response), contentType, body string) (string, error) {
	t.Helper()
	resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{contentType}}, Body: io.NopCloser(strings.NewReader(body))}
	guard(resp)
	t.Cleanup(func() {
		if err := resp.Body.Close(); err != nil {
			t.Errorf("close Cline guarded response: %v", err)
		}
	})
	output, err := io.ReadAll(resp.Body)
	return string(output), err
}

func TestClineResponseGuardPersistsScopedErrors(t *testing.T) {
	for _, tc := range []struct {
		name, payload, scope, reason string
		authoritative                bool
	}{
		{"five_hour", `{"error":{"message":"ClinePass limit 5-hour. Try again in 1h"}}`, cline.ScopePass + "five_hour", "pass_limit", true},
		{"weekly", `{"error":{"message":"ClinePass limit weekly. Try again in 2d"}}`, cline.ScopePass + "weekly", "pass_limit", true},
		{"monthly", `{"error":{"message":"ClinePass limit monthly"}}`, cline.ScopePass + "monthly", "pass_limit", false},
		{"generic", `{"error":{"code":429,"message":"Busy"}}`, cline.ScopeThrottle, "throttle", false},
		{"balance", `{"error":{"code":"insufficient_balance"}}`, cline.ScopePayG, "insufficient_balance", false},
		{"entitlement", `{"error":{"message":"The user is not subscribed to required model plan"}}`, cline.ScopePass + "entitlement", "not_subscribed", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, streaming := range []bool{false, true} {
				repo := &clineGuardLimitRepository{}
				s := &OpenAIGatewayService{rateLimitService: &RateLimitService{accountRepo: repo}}
				guard, err := s.prepareClineResponseGuard(context.Background(), clineTestAccount(cline.ModePass), []byte(`{"model":"cline-pass/model"}`), streaming)
				if err != nil {
					t.Fatal(err)
				}
				body, mimeType := tc.payload, "application/json"
				if streaming {
					body, mimeType = "data: "+tc.payload+"\n\ndata: [DONE]\n\n", "text/event-stream; charset=utf-8"
				}
				output, err := clineGuardResponse(t, guard, mimeType, body)
				if output != "" || !errors.Is(err, cline.ErrStreamFailure) || len(repo.observations) != 1 {
					t.Fatalf("output=%q err=%v writes=%d", output, err, len(repo.observations))
				}
				got := repo.observations[0]
				if got.scope != tc.scope || got.reason != tc.reason || got.authoritative != tc.authoritative || !got.until.After(time.Now()) {
					t.Fatalf("incorrect scoped limit: %+v", got)
				}
			}
		})
	}
}

func TestClineResponseGuardFreeScopeUsesActualModel(t *testing.T) {
	// The response guard is exercised in isolation; Free outbound forwarding
	// remains rejected by ValidateClineOutboundBody and is not enabled here.
	repo := &clineGuardLimitRepository{}
	s := &OpenAIGatewayService{rateLimitService: &RateLimitService{accountRepo: repo}}
	a := clineTestAccount(cline.ModeFree)
	a.Credentials["model_mapping"] = map[string]any{"public-model": "vendor/actual", "vendor/actual": "vendor/wrong"}
	guard, err := s.prepareClineResponseGuard(context.Background(), a, []byte(`{"model":"vendor/actual"}`), true)
	if err != nil {
		t.Fatal(err)
	}
	_, err = clineGuardResponse(t, guard, "text/event-stream", "event: error\ndata: {\"message\":\"Free limit reached on model vendor/actual. Try again in 1h\"}\n\n")
	if !errors.Is(err, cline.ErrStreamFailure) || len(repo.observations) != 1 || repo.observations[0].scope != cline.ScopeFree+"vendor/actual" {
		t.Fatalf("upstream model remapped: %v %+v", err, repo.observations)
	}
}

func TestClineResponseGuardFreezesCredentialsAndCancellation(t *testing.T) {
	repo := &clineGuardLimitRepository{}
	s := &OpenAIGatewayService{rateLimitService: &RateLimitService{accountRepo: repo}}
	a := clineTestAccount(cline.ModePass)
	fingerprint := ClineCredentialFingerprint(a)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	guard, err := s.prepareClineResponseGuard(ctx, a, []byte(`{"model":"cline-pass/model"}`), true)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate an edit during upstream transport, before the guard sees a response.
	a.Credentials["api_key"] = "rotated-key"
	mapping, ok := a.Credentials["model_mapping"].(map[string]any)
	if !ok {
		t.Fatal("test account model mapping has unexpected type")
	}
	mapping["public-model"] = "cline-pass/changed"
	cancel()
	_, err = clineGuardResponse(t, guard, "text/event-stream", "data: {\"error\":{\"code\":429}}\n\n")
	if !errors.Is(err, cline.ErrStreamFailure) || len(repo.observations) != 1 {
		t.Fatalf("err=%v writes=%d", err, len(repo.observations))
	}
	got := repo.observations[0]
	if ClineCredentialFingerprint(got.account) != fingerprint || got.account.GetModelMapping()["public-model"] != "cline-pass/model" {
		t.Fatal("request identity changed during read")
	}
	if got.contextErr != nil || got.deadline.IsZero() || got.deadline.After(time.Now().Add(clineLimitPersistTimeout)) {
		t.Fatal("persistence lost its independent bounded context")
	}
}

func TestClineResponseGuardNeverInventsQuotaFromOutput(t *testing.T) {
	for _, tc := range []struct {
		payload     string
		wantFailure bool
	}{
		{`{"choices":[{"delta":{"content":"ClinePass limit weekly. Try again in 2d"}}]}`, false},
		{`{"error":{"message":"unrelated failure"}}`, true},
		{`{"choices":[{"finish_reason":"error"}]}`, true},
	} {
		repo := &clineGuardLimitRepository{}
		s := &OpenAIGatewayService{rateLimitService: &RateLimitService{accountRepo: repo}}
		guard, err := s.prepareClineResponseGuard(context.Background(), clineTestAccount(cline.ModePass), []byte(`{"model":"cline-pass/model"}`), true)
		if err != nil {
			t.Fatal(err)
		}
		output, err := clineGuardResponse(t, guard, "text/event-stream", "data: "+tc.payload+"\n\ndata: [DONE]\n\n")
		if errors.Is(err, cline.ErrStreamFailure) != tc.wantFailure || len(repo.observations) != 0 {
			t.Fatalf("err=%v writes=%d", err, len(repo.observations))
		}
		if !tc.wantFailure && !strings.Contains(output, "[DONE]") {
			t.Fatal("normal completion lost")
		}
	}
}

func TestClineResponseGuardStreamingJSONAndIsolation(t *testing.T) {
	s := &OpenAIGatewayService{}
	guard, err := s.prepareClineResponseGuard(context.Background(), clineTestAccount(cline.ModePass), []byte(`{"model":"cline-pass/model"}`), true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := clineGuardResponse(t, guard, "application/json", `{"error":{"message":"failed"}}`); !errors.Is(err, cline.ErrStreamFailure) {
		t.Fatal(err)
	}
	if _, err := clineGuardResponse(t, guard, "application/json", `{"choices":[]}`); !errors.Is(err, cline.ErrExpectedSSE) {
		t.Fatal(err)
	}
	a := clineTestAccount(cline.ModePass)
	a.Platform = "deepseek"
	if other, err := s.prepareClineResponseGuard(context.Background(), a, nil, true); other != nil || err != nil {
		t.Fatal("DeepSeek received Cline guard")
	}
	body := io.NopCloser(strings.NewReader("HTTP failure"))
	defer func() {
		if err := body.Close(); err != nil {
			t.Error(err)
		}
	}()
	resp := &http.Response{StatusCode: 429, Body: body}
	guard(resp)
	if resp.Body != body {
		t.Fatal("ordinary HTTP error path replaced")
	}
	a = clineTestAccount(cline.ModePass)
	a.Credentials["invalid"] = make(chan int)
	if _, err := s.prepareClineResponseGuard(context.Background(), a, []byte(`{"model":"cline-pass/model"}`), true); err == nil {
		t.Fatal("unfreezable identity accepted")
	}
}

func TestClineResponseGuardNormalizesLegacyClineEnvelope(t *testing.T) {
	account := clineTestAccount(cline.ModePass)
	account.Platform = PlatformDeepseek
	account.Credentials["base_url"] = cline.BaseURL

	service := &OpenAIGatewayService{}
	guard, err := service.prepareClineResponseGuard(context.Background(), account, []byte(`{"model":"cline-pass/model"}`), false)
	require.NoError(t, err)
	require.NotNil(t, guard)

	body, err := clineGuardResponse(t, guard, "application/json", `{"success":true,"data":{"id":"legacy-1","model":"cline-pass/model","choices":[{"message":{"role":"assistant","content":"ok"}}]}}`)
	require.NoError(t, err)
	var normalized map[string]any
	require.NoError(t, json.Unmarshal([]byte(body), &normalized))
	require.Contains(t, normalized, "choices")
	require.NotContains(t, normalized, "success")
	require.NotContains(t, normalized, "data")
}
