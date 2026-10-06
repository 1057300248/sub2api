//go:build unit

package service

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClineCustomParametersProtectAllProtocolFieldsAndCaseAliases(t *testing.T) {
	for _, platform := range []string{PlatformCline, PlatformOpenAI, PlatformDeepseek} {
		for _, shape := range []any{apicompat.ResponsesRequest{}, apicompat.AnthropicRequest{}} {
			t.Run(platform+"/"+reflect.TypeOf(shape).Name(), func(t *testing.T) {
				account := &Account{Platform: platform, Type: AccountTypeAPIKey, Credentials: map[string]any{"base_url": "https://api.cline.bot/api/v1"}}
				source := map[string]any{"providerOptions": map[string]any{"gateway": map[string]any{"only": []string{"deepseek"}}}, "vendor": map[string]any{"zero": 0, "disabled": false}}
				// Cover the complete source and target protocol contract, including
				// Unicode case-fold aliases accepted by encoding/json.
				for _, dto := range []any{shape, apicompat.ChatCompletionsRequest{}} {
					typeOf := reflect.TypeOf(dto)
					for i := 0; i < typeOf.NumField(); i++ {
						name := strings.Split(typeOf.Field(i).Tag.Get("json"), ",")[0]
						if name == "" || name == "-" {
							continue
						}
						// tool_choice has its own strict lowering contract. Multiple
						// conflicting aliases are deliberately rejected there, so they do
						// not belong in this custom-extension shadowing regression.
						if name == "tool_choice" {
							continue
						}
						for _, alias := range []string{name, strings.ToUpper(name), strings.ToUpper(name[:1]) + name[1:], strings.ReplaceAll(name, "s", "ſ"), strings.ReplaceAll(name, "k", "K")} {
							source[alias] = "must-not-override-lowered-request"
						}
					}
				}
				raw, err := json.Marshal(source)
				require.NoError(t, err)
				outbound := []byte(`{"model":"mapped","messages":[{"role":"user","content":"original"}],"stream":false}`)
				got, err := mergeClineCustomRequestParameters(account, raw, outbound, shape)
				require.NoError(t, err)
				assert.JSONEq(t, `{"model":"mapped","messages":[{"role":"user","content":"original"}],"stream":false,"providerOptions":{"gateway":{"only":["deepseek"]}},"vendor":{"zero":0,"disabled":false}}`, string(got))
			})
		}
	}
}
