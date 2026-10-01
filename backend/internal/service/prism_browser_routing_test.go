package service

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
)

func TestPrismBrowserResponsesURL(t *testing.T) {
	tests := []struct {
		name string
		base string
		want string
	}{
		{name: "v1 base", base: "http://127.0.0.1:8319/v1", want: "http://127.0.0.1:8319/v1/responses"},
		{name: "responses suffix", base: "http://adapter.example/v1/responses/", want: "http://adapter.example/v1/responses"},
		{name: "empty", base: " ", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := prismBrowserResponsesURL(tt.base); got != tt.want {
				t.Fatalf("prismBrowserResponsesURL(%q) = %q, want %q", tt.base, got, tt.want)
			}
		})
	}
}

func prismTestService(endpoint string) (*OpenAIGatewayService, *Account) {
	cfg := &config.Config{}
	cfg.Gateway.PrismBrowser = config.GatewayPrismBrowserConfig{Enabled: true, BaseURL: endpoint + "/v1", APIKey: "fixture-bridge-key"}
	return &OpenAIGatewayService{cfg: cfg}, &Account{ID: 42, Platform: PlatformOpenAI, Type: AccountTypeOAuth,
		Credentials: map[string]any{"access_token": "fixture-oauth"}, Extra: map[string]any{"openai_prism_browser": true}}
}

func TestPrismBrowserCallProtectsCredentialBoundary(t *testing.T) {
	var count int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count++
		if r.URL.Path != "/v1/responses" || r.Header.Get("Authorization") != "Bearer fixture-bridge-key" ||
			r.Header.Get("X-Prism-OAuth-Token") != "fixture-oauth" || r.Header.Get("X-Prism-Account-ID") != "42" {
			t.Error("unexpected adapter request")
		}
		w.Header().Set("Location", "http://127.0.0.1:1/credential-leak")
		w.WriteHeader(http.StatusTemporaryRedirect)
	}))
	defer server.Close()
	s, account := prismTestService(server.URL)
	if _, _, _, err := s.callPrismBrowser(context.Background(), account, []byte(`{"input":"test"}`)); err == nil {
		t.Fatal("redirect must be rejected")
	}
	if count != 1 {
		t.Fatalf("request count = %d", count)
	}
	s.cfg.Gateway.PrismBrowser.Enabled = false
	if !accountHasPrismBrowser(account) {
		t.Fatal("disabled server config must not erase account intent")
	}
	if _, _, _, err := s.callPrismBrowser(context.Background(), account, nil); err == nil {
		t.Fatal("disabled adapter must fail closed")
	}
	if count != 1 {
		t.Fatal("disabled adapter submitted a request")
	}
}

func TestPrismBrowserForwardTerminalAndUsage(t *testing.T) {
	const terminal = `{"id":"resp_fixture","status":"completed","model":"gpt-5.6-sol","usage":null,"output":[{"content":[{"text":"21"}]}]}`
	for _, stream := range []bool{false, true} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if stream {
				_, _ = io.WriteString(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":"+terminal+"}\n\n")
			} else {
				_, _ = io.WriteString(w, terminal)
			}
		}))
		s, account := prismTestService(server.URL)
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
		body := `{"model":"gpt-5.6-sol","input":"candy","stream":false}`
		if stream {
			body = strings.Replace(body, "false", "true", 1)
		}
		result, err := s.forwardPrismBrowser(context.Background(), c, account, []byte(body), time.Now())
		server.Close()
		if err != nil || w.Code != http.StatusOK || result == nil || !result.UsageUnavailable || result.ResponseID != "resp_fixture" {
			t.Fatalf("unexpected result: result=%+v status=%d err=%v", result, w.Code, err)
		}
		if err := s.RecordUsage(context.Background(), &OpenAIRecordUsageInput{Result: result}); err == nil {
			t.Fatal("unknown usage must not enter billing as zero tokens")
		}
	}
	for _, raw := range []string{`event: response.completed`, `data: {"type":"response.failed"}`, "data: not-json"} {
		if _, err := prismBrowserTerminal([]byte(raw), "gpt-5.6-sol", true); err == nil {
			t.Fatal("invalid stream accepted")
		}
	}
	if _, err := prismBrowserTerminal([]byte(terminal), "gpt-6-astra", false); err == nil {
		t.Fatal("model substitution accepted")
	}
}

func TestPrismBrowserAdapterURLStaysOnLoopback(t *testing.T) {
	valid := []string{"http://127.0.0.1:8319/v1", "http://[::1]:8319/v1/responses"}
	for _, input := range valid {
		if _, err := prismBrowserAdapterURL(input); err != nil {
			t.Fatalf("valid adapter %q rejected: %v", input, err)
		}
	}
	invalid := []string{
		"https://127.0.0.1:8319/v1", "http://localhost:8319/v1",
		"http://adapter.example:8319/v1", "http://127.0.0.2:8319/v1",
		"http://127.0.0.1:8319/v1?next=evil", "http://user@127.0.0.1:8319/v1",
		"http://127.0.0.1:8319/other", "http://127.0.0.1/v1",
	}
	for _, input := range invalid {
		if _, err := prismBrowserAdapterURL(input); err == nil {
			t.Fatalf("unsafe adapter %q accepted", input)
		}
	}
}

func TestAccountUsesPrismBrowserRequiresServerAndAccountSwitch(t *testing.T) {
	cfg := &config.Config{}
	cfg.Gateway.PrismBrowser.Enabled = true

	account := &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth, Extra: map[string]any{"openai_prism_browser": true}}
	if !accountUsesPrismBrowser(account, cfg) {
		t.Fatal("enabled OpenAI account should use Prism browser adapter")
	}

	account.Extra["openai_prism_browser"] = false
	if accountUsesPrismBrowser(account, cfg) {
		t.Fatal("disabled account switch should keep the normal route")
	}

	account.Extra["openai_prism_browser"] = true
	cfg.Gateway.PrismBrowser.Enabled = false
	if accountUsesPrismBrowser(account, cfg) {
		t.Fatal("disabled server adapter must prevent Prism routing")
	}

	cfg.Gateway.PrismBrowser.Enabled = true
	account.Type = AccountTypeAPIKey
	if accountUsesPrismBrowser(account, cfg) {
		t.Fatal("API key accounts must not use the OAuth Prism bridge")
	}
}
