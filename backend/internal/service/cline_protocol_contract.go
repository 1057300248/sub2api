package service

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/Wei-Shaw/sub2api/internal/pkg/cline"
	"github.com/tidwall/sjson"
)

func isClineTransport(a *Account) bool {
	return a != nil && (a.IsCline() || isLegacyClineAccount(a))
}

// The gateway's negotiated response mode wins, including omitted/false on
// compaction requests. A bool with omitempty must not select upstream SSE.
func clineOutboundContract(a *Account, body []byte, stream bool, token string) ([]byte, string, error) {
	if !isClineTransport(a) {
		return body, token, nil
	}
	body, err := sjson.SetBytes(body, "stream", stream)
	if err != nil {
		return nil, "", fmt.Errorf("invalid Cline stream contract")
	}
	if !stream {
		body, err = sjson.DeleteBytes(body, "stream_options")
		if err != nil {
			return nil, "", fmt.Errorf("invalid Cline stream options")
		}
	}
	if a.IsCline() {
		token, err = cline.BearerToken(a.GetCredential("cline_auth_type"), a.GetCredential("api_key"))
		if err != nil {
			return nil, "", err
		}
	}
	return body, token, nil
}

// Legacy protocol converters intentionally drop unsupported server tools. Cline
// must not silently turn a caller's forced tool into auto after that lowering.
// Missing/auto/none keep their existing semantics. No request/body/key is logged.
func validateClineLoweredToolChoice(a *Account, source json.RawMessage, chat *apicompat.ChatCompletionsRequest) error {
	if !isClineTransport(a) || len(source) == 0 || bytes.Equal(bytes.TrimSpace(source), []byte("null")) {
		return nil
	}
	fail := func() error {
		return fmt.Errorf("cline chat transport cannot preserve the requested tool_choice; use a supported declared function/custom tool or an explicit auto/none choice")
	}
	var value string
	if json.Unmarshal(source, &value) == nil {
		switch value {
		case "auto", "none":
			return nil
		case "required":
			if chat != nil && len(chat.Tools) > 0 && string(chat.ToolChoice) == `"required"` {
				return nil
			}
		}
		return fail()
	}
	var choice struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(source, &choice) != nil {
		return fail()
	}
	if choice.Type == "auto" || choice.Type == "none" {
		return nil
	}
	if chat == nil || len(chat.Tools) == 0 || len(chat.ToolChoice) == 0 {
		return fail()
	}
	if choice.Type == "any" {
		if string(chat.ToolChoice) == `"required"` {
			return nil
		}
		return fail()
	}
	switch choice.Type {
	case "function", "custom", "tool", "tool_search", "x_search":
		var lowered struct {
			Type     string `json:"type"`
			Function struct {
				Name string `json:"name"`
			} `json:"function"`
		}
		if json.Unmarshal(chat.ToolChoice, &lowered) != nil {
			return fail()
		}
		for _, tool := range chat.Tools {
			if lowered.Type == "function" && lowered.Function.Name != "" && tool.Type == "function" && tool.Function != nil && tool.Function.Name == lowered.Function.Name {
				return nil
			}
			if lowered.Type == "x_search" && tool.Type == "x_search" {
				return nil
			}
		}
	}
	return fail()
}
