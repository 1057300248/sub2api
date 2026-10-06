package service

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"unicode"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
)

// mergeClineCustomRequestParameters carries request-level provider-specific
// top-level JSON parameters through protocol lowering into the final Chat
// Completions body. Nothing is persisted on the account.
//
// Only fields unknown to the source protocol are treated as custom parameters.
// Chat Completions fields are protected so custom parameters cannot overwrite
// model/messages/stream/tools or other gateway-owned core fields.
func mergeClineCustomRequestParameters(account *Account, sourceBody, outboundBody []byte, sourceShape any) ([]byte, error) {
	if account == nil || (!account.IsCline() && !isLegacyClineAccount(account)) {
		return outboundBody, nil
	}
	if _, _, err := extractClineHeaderOverrides(account, sourceBody); err != nil {
		return nil, err
	}
	var source map[string]json.RawMessage
	if err := json.Unmarshal(sourceBody, &source); err != nil || source == nil {
		return nil, fmt.Errorf("parse Cline custom request parameters: invalid JSON object")
	}
	var outbound map[string]json.RawMessage
	if err := json.Unmarshal(outboundBody, &outbound); err != nil || outbound == nil {
		return nil, fmt.Errorf("merge Cline custom request parameters: invalid outbound JSON object")
	}

	if err := validateClineLoweredToolChoice(source, outbound); err != nil {
		return nil, err
	}
	protected := append(jsonStructFieldNames(sourceShape), jsonStructFieldNames(apicompat.ChatCompletionsRequest{})...)
	changed := false
	for key, raw := range source {
		// encoding/json accepts case-folded struct field names. Treat those
		// aliases as protocol fields too, never as vendor extensions which
		// could shadow the canonical field after lowering or compaction.
		known := false
		for _, field := range protected {
			if strings.EqualFold(key, field) {
				known = true
				break
			}
		}
		if known {
			continue
		}
		if !validClineCustomParameterName(key) {
			return nil, fmt.Errorf("invalid Cline custom parameter name")
		}
		outbound[key] = append(json.RawMessage(nil), raw...)
		changed = true
	}
	if !changed {
		return outboundBody, nil
	}
	merged, err := json.Marshal(outbound)
	if err != nil {
		return nil, fmt.Errorf("encode Cline custom request parameters: %w", err)
	}
	return merged, nil
}

func jsonStructFieldNames(value any) []string {
	t := reflect.TypeOf(value)
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	fields := make([]string, 0, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		tag := t.Field(i).Tag.Get("json")
		name := strings.Split(tag, ",")[0]
		if name != "" && name != "-" {
			fields = append(fields, name)
		}
	}
	return fields
}

func validClineCustomParameterName(name string) bool {
	if name == "" || len(name) > 256 {
		return false
	}
	return strings.IndexFunc(name, func(r rune) bool {
		return unicode.IsControl(r)
	}) < 0
}
