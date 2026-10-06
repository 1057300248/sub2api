package cline

import (
	"encoding/json"
	"errors"
)

const MaxModelCatalogBytes = 4 << 20

// Capabilities are observations, not an entitlement, routing or billing policy.
// A nil field is unknown, never a guessed provider/model-family default.
type ModelCapabilities struct {
	Source          string `json:"source"`
	ContextWindow   *int64 `json:"context_window,omitempty"`
	MaxOutputTokens *int64 `json:"max_output_tokens,omitempty"`
	Images          *bool  `json:"images,omitempty"`
	Tools           *bool  `json:"tools,omitempty"`
	Reasoning       *bool  `json:"reasoning,omitempty"`
}

// ApplyModelCapabilities joins exact IDs only. Pass prefixes and model suffixes
// are not stripped: a similarly named paid model is not evidence for Pass.
func (c *Catalog) ApplyModelCapabilities(body []byte) error {
	if c == nil || len(body) > MaxModelCatalogBytes {
		return errors.New("invalid Cline model catalog size")
	}
	var entries []json.RawMessage
	if json.Unmarshal(body, &entries) != nil {
		var envelope struct {
			Data    []json.RawMessage `json:"data"`
			Success *bool             `json:"success"`
		}
		if json.Unmarshal(body, &envelope) != nil || envelope.Data == nil || (envelope.Success != nil && !*envelope.Success) {
			return errors.New("invalid Cline model catalog")
		}
		entries = envelope.Data
	}
	if entries == nil || len(entries) > 4096 {
		return errors.New("invalid Cline model catalog entries")
	}
	byID := make(map[string]*ModelCapabilities, len(entries))
	for _, raw := range entries {
		var item map[string]json.RawMessage
		if json.Unmarshal(raw, &item) != nil || item == nil {
			return errors.New("invalid Cline model catalog entry")
		}
		var id string
		if json.Unmarshal(item["id"], &id) != nil || !ValidModelID(id) {
			return errors.New("invalid Cline model catalog ID")
		}
		if _, exists := byID[id]; exists {
			return errors.New("duplicate Cline model catalog ID")
		}
		cap := &ModelCapabilities{Source: "official_model_catalog"}
		cap.ContextWindow = positiveCatalogInteger(item, "contextWindow", "context_length")
		cap.MaxOutputTokens = positiveCatalogInteger(item, "maxTokens", "max_completion_tokens")
		cap.Images = catalogBoolean(item, "supportsImages")
		cap.Tools = catalogBoolean(item, "supportsTools", "supportsToolUse")
		cap.Reasoning = catalogBoolean(item, "supportsReasoning")
		var params []string
		if json.Unmarshal(item["supported_parameters"], &params) == nil && len(params) <= 128 {
			for _, p := range params {
				switch p {
				case "tools":
					if cap.Tools == nil {
						yes := true
						cap.Tools = &yes
					}
				case "reasoning", "reasoning_effort":
					if cap.Reasoning == nil {
						yes := true
						cap.Reasoning = &yes
					}
				}
			}
		}
		if cap.ContextWindow == nil && cap.MaxOutputTokens == nil && cap.Images == nil && cap.Tools == nil && cap.Reasoning == nil {
			cap = nil
		}
		byID[id] = cap
	}
	// Validate the whole response before changing any saved projection.
	for _, bucket := range [][]Model{c.Recommended, c.Pass, c.Free} {
		for i := range bucket {
			bucket[i].Capabilities = byID[bucket[i].ID]
		}
	}
	c.CapabilitiesStatus = "ok"
	return nil
}

func positiveCatalogInteger(item map[string]json.RawMessage, names ...string) *int64 {
	for _, name := range names {
		var n int64
		if json.Unmarshal(item[name], &n) == nil && n > 0 && n <= 1_000_000_000 {
			return &n
		}
	}
	return nil
}
func catalogBoolean(item map[string]json.RawMessage, names ...string) *bool {
	for _, name := range names {
		raw := item[name]
		if string(raw) == "true" || string(raw) == "false" {
			v := string(raw) == "true"
			return &v
		}
	}
	return nil
}
