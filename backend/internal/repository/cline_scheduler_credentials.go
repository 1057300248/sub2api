package repository

import "github.com/Wei-Shaw/sub2api/internal/service"

// Cline candidate admission must retain the identity used by scoped cooldowns.
// Keep other platforms' existing projection unchanged. Full credentials are
// still hydrated before forwarding or the credentials-equality database CAS.
func filterSchedulerAccountCredentials(account *service.Account) map[string]any {
	if account == nil {
		return nil
	}
	filtered := filterSchedulerCredentials(account.Credentials)
	if !account.IsCline() {
		return filtered
	}
	if filtered == nil {
		filtered = make(map[string]any)
	}
	for _, key := range []string{"base_url", "account_mode", "cline_auth_type", "api_protocol"} {
		if value, ok := account.Credentials[key]; ok && value != nil {
			filtered[key] = value
		}
	}
	return filtered
}
