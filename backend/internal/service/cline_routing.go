package service

import "sort"

// SupportsClineEndpointCapability describes transport capability, not model
// entitlement. Mode and the exact whitelist are checked separately by admission
// and the outbound guard. Cline does not implement native Responses, compaction,
// embeddings, image generation, alpha-search or realtime/websocket endpoints.
func (a *Account) SupportsClineEndpointCapability(capability OpenAIEndpointCapability) bool {
	return a.IsCline() && a.Type == AccountTypeAPIKey &&
		(capability == "" || capability == OpenAIEndpointCapabilityChatCompletions)
}

// ClineModelIDs exposes only explicit, usable public aliases. It does not fetch
// an upstream catalog or infer entitlement from a suggested/default model.
func (a *Account) ClineModelIDs() []string {
	ids := make([]string, 0)
	if !a.IsCline() {
		return ids
	}
	for id := range a.GetModelMapping() {
		if a.IsClineModelSupported(id) {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}
