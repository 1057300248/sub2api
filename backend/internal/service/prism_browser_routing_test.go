package service

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
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
