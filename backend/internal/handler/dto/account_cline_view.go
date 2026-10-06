package dto

import (
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// Build a bounded, credential-checked local projection before credential
// redaction. Neither listing nor editing an account performs an upstream read.
func clineAccountUsageView(a *service.Account, extra map[string]any) (*service.ClineMetadataView, map[string]any) {
	if !a.IsCline() {
		return nil, extra
	}
	view := service.ClineMetadataForAccount(a, time.Now().UTC())
	view.Catalog = nil // Use the explicit metadata endpoint for model discovery.
	clean := make(map[string]any, len(extra))
	for k, v := range extra {
		if k != service.ClineStateExtraKey && k != "model_rate_limits" {
			clean[k] = v
		}
	}
	return view, clean
}
