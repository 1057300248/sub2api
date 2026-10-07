package cline

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
)

const ModelsURL = BaseURL + "/ai/cline/models"
const MaxModelCatalogBytes = 4 << 20

// Nil is unknown, NOT false. Only explicit provider metadata is retained; names,
// descriptions, recommendation buckets and prices never imply capabilities or
// a caller's entitlement. Known false values must survive JSON round trips.
type ModelCapabilities struct {
	ContextWindow       *int64   `json:"context_window,omitempty"`
	MaxOutputTokens     *int64   `json:"max_output_tokens,omitempty"`
	SupportsImages      *bool    `json:"supports_images,omitempty"`
	SupportsTools       *bool    `json:"supports_tools,omitempty"`
	SupportsReasoning   *bool    `json:"supports_reasoning,omitempty"`
	SupportsPromptCache *bool    `json:"supports_prompt_cache,omitempty"`
	ReasoningEfforts    []string `json:"reasoning_efforts,omitempty"`
	Source              string   `json:"source"`
}

// This endpoint is used by the official client. Its schema remains optional:
// invalid/missing capability values stay unknown and do not disable Pass usage.
// ID matching is exact, never strip cline-pass/ and borrow another model's limits.
func EnrichCatalogCapabilities(catalog *Catalog, body []byte) error {
	if catalog == nil || len(body) > MaxModelCatalogBytes {
		return errors.New("invalid Cline capability catalog")
	}
	raw := bytes.TrimSpace(body)
	if len(raw) == 0 {
		return errors.New("empty Cline capability catalog")
	}
	if raw[0] == '{' {
		var envelope map[string]json.RawMessage
		if json.Unmarshal(raw, &envelope) != nil {
			return errors.New("invalid Cline capability catalog")
		}
		raw = envelope["data"]
	}
	var rows []map[string]json.RawMessage
	if json.Unmarshal(raw, &rows) != nil || rows == nil || len(rows) > 4096 {
		return errors.New("invalid Cline capability rows")
	}
	caps := map[string]*ModelCapabilities{}
	for _, row := range rows {
		var id string
		if json.Unmarshal(row["id"], &id) != nil || !ValidModelID(id) {
			continue
		}
		if _, duplicate := caps[id]; duplicate {
			return errors.New("duplicate Cline capability model")
		}
		cap := parseCapabilities(row)
		caps[id] = cap
	}
	for _, models := range [][]Model{catalog.Recommended, catalog.Pass, catalog.Free} {
		for i := range models {
			models[i].Capabilities = caps[models[i].ID]
		}
	}
	return nil
}
func parseCapabilities(row map[string]json.RawMessage) *ModelCapabilities {
	cap := &ModelCapabilities{Source: "cline_models"}
	cap.ContextWindow = positiveCapabilityInt(row, "contextWindow", "context_length")
	cap.MaxOutputTokens = positiveCapabilityInt(row, "maxTokens", "max_output_tokens")
	var top map[string]json.RawMessage
	if json.Unmarshal(row["top_provider"], &top) == nil {
		if cap.ContextWindow == nil {
			cap.ContextWindow = positiveCapabilityInt(top, "context_length")
		}
		if cap.MaxOutputTokens == nil {
			cap.MaxOutputTokens = positiveCapabilityInt(top, "max_completion_tokens")
		}
	}
	cap.SupportsImages = booleanCapability(row, "supportsImages")
	cap.SupportsReasoning = booleanCapability(row, "supportsReasoning")
	cap.SupportsTools = booleanCapability(row, "supportsTools")
	cap.SupportsPromptCache = booleanCapability(row, "supportsPromptCache")
	var architecture struct {
		InputModalities []string `json:"input_modalities"`
	}
	if cap.SupportsImages == nil && json.Unmarshal(row["architecture"], &architecture) == nil && len(architecture.InputModalities) > 0 {
		images := false
		for _, modality := range architecture.InputModalities {
			if modality == "image" {
				images = true
			}
		}
		cap.SupportsImages = &images
	}
	var parameters []string
	if json.Unmarshal(row["supported_parameters"], &parameters) == nil && parameters != nil {
		tools, reasoning := false, false
		for _, p := range parameters {
			if p == "tools" {
				tools = true
			}
			if p == "reasoning" || p == "reasoning_effort" {
				reasoning = true
			}
		}
		if cap.SupportsTools == nil {
			cap.SupportsTools = &tools
		}
		if cap.SupportsReasoning == nil {
			cap.SupportsReasoning = &reasoning
		}
	}
	var efforts []string
	if json.Unmarshal(row["reasoningEfforts"], &efforts) != nil {
		_ = json.Unmarshal(row["reasoning_efforts"], &efforts)
	}
	if len(efforts) <= 16 {
		seen := map[string]bool{}
		for _, e := range efforts {
			switch e {
			case "none", "minimal", "low", "medium", "high", "xhigh", "max":
				if !seen[e] {
					seen[e] = true
					cap.ReasoningEfforts = append(cap.ReasoningEfforts, e)
				}
			}
		}
	}
	if cap.ContextWindow == nil && cap.MaxOutputTokens == nil && cap.SupportsImages == nil && cap.SupportsTools == nil && cap.SupportsReasoning == nil && cap.SupportsPromptCache == nil && len(cap.ReasoningEfforts) == 0 {
		return nil
	}
	return cap
}
func booleanCapability(m map[string]json.RawMessage, name string) *bool {
	raw, exists := m[name]
	if !exists || strings.TrimSpace(string(raw)) == "null" {
		return nil
	}
	var value bool
	if json.Unmarshal(raw, &value) != nil {
		return nil
	}
	return &value
}
func positiveCapabilityInt(m map[string]json.RawMessage, names ...string) *int64 {
	for _, name := range names {
		var value int64
		if json.Unmarshal(m[name], &value) == nil && value > 0 && value <= 1<<31 {
			return &value
		}
	}
	return nil
}
