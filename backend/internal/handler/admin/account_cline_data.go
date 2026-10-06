package admin

import "github.com/Wei-Shaw/sub2api/internal/service"

// Explicit administrator backups retain the existing secret-bearing contract.
// Server-verified identity/cooldowns are local observations, not portable grants.
func portableClineExtra(platform string, extra map[string]any) map[string]any {
	if platform != service.PlatformCline {
		return extra
	}
	out := make(map[string]any, len(extra))
	for key, value := range extra {
		if key != service.ClineStateExtraKey && key != "model_rate_limits" {
			out[key] = value
		}
	}
	return out
}
func validateClineDataAccount(item DataAccount) error {
	if item.Platform != service.PlatformCline {
		return nil
	}
	credentials := make(map[string]any, len(item.Credentials))
	for key, value := range item.Credentials {
		credentials[key] = value
	}
	return service.NormalizeClineCredentials(item.Platform, item.Type, credentials)
}
