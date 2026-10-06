package service

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/cline"
)

// The Cline editor submits credential deltas. Keep unedited options on the
// server, rather than round-tripping stored secrets through the browser.
func mergeClineAccountCredentials(account *Account, incoming map[string]any) map[string]any {
	if !account.IsCline() {
		return MergePreservingSensitiveCreds(account.Credentials, incoming)
	}
	out := make(map[string]any, len(account.Credentials)+len(incoming))
	for key, value := range account.Credentials {
		out[key] = value
	}
	for key, value := range incoming {
		out[key] = value
	}
	return out
}

// Scheduling is an operator preference, not permission to bypass admission.
// Free/unknown modes must not appear enabled after a create or edit operation.
func applyClineSchedulingSettings(account *Account, requested *bool) {
	if !account.IsCline() {
		return
	}
	if requested != nil {
		account.Schedulable = *requested
	}
	if mode := account.GetClineMode(); mode != cline.ModePass && mode != cline.ModePayG {
		account.Schedulable = false
	}
}

// Account defaults use the same positive list and byte bounds as request
// overrides. This validates even disabled stored rows; explicit {} clears them.
func validateClineAccountHeaderSettings(platform string, credentials map[string]any) error {
	if platform != PlatformCline {
		return nil
	}
	if raw, exists := credentials[credKeyHeaderOverrideEnabled]; exists {
		if _, ok := raw.(bool); !ok {
			return ErrClineHeaderOverrides
		}
	}
	raw, exists := credentials[credKeyHeaderOverrides]
	if !exists {
		return nil
	}
	data, err := json.Marshal(map[string]any{"header_overrides": raw})
	if err != nil {
		return ErrClineHeaderOverrides
	}
	_, _, err = extractClineHeaderOverrides(&Account{Platform: PlatformCline}, data)
	return err
}

func clineAccountHeaderDefaults(account *Account) map[string]string {
	if validateClineAccountHeaderSettings(account.Platform, account.Credentials) != nil {
		return nil
	}
	raw, exists := account.Credentials[credKeyHeaderOverrides]
	if !exists {
		return nil
	}
	data, err := json.Marshal(map[string]any{"header_overrides": raw})
	if err != nil {
		return nil
	}
	_, headers, err := extractClineHeaderOverrides(account, data)
	if err != nil {
		return nil
	}
	out := make(map[string]string, len(headers))
	for name := range headers {
		if value := strings.TrimSpace(headers.Get(name)); value != "" {
			out[strings.ToLower(name)] = value
		}
	}
	return out
}

// Remove all wire-casing variants before applying the per-request value.
// Account defaults may use noncanonical keys; setting only the canonical key
// would otherwise send both defaults and overrides to the upstream.
func applyClineRequestHeaders(target, overrides http.Header) {
	for name, values := range overrides {
		for existing := range target {
			if strings.EqualFold(existing, name) {
				delete(target, existing)
			}
		}
		target[http.CanonicalHeaderKey(name)] = append([]string(nil), values...)
	}
}

// Native Cline account settings are deltas. Managed keys are still reloaded
// under the repository lock; an editable cost change must not erase ordinary
// options omitted by this small form. Other platforms retain full-PUT semantics.
func mergeClineAccountExtra(account *Account, incoming map[string]any) map[string]any {
	out := make(map[string]any, len(account.Extra)+len(incoming))
	for key, value := range account.Extra {
		out[key] = value
	}
	for key, value := range incoming {
		if value == nil && IsClineQuotaConfigKey(key) {
			delete(out, key)
		} else {
			out[key] = value
		}
	}
	return out
}
