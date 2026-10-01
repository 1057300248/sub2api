package service

import (
	"time"

	"github.com/Wei-Shaw/sub2api/internal/domain"
	"github.com/Wei-Shaw/sub2api/internal/pkg/cline"
)

func clineMonitorCapability(account *Account) error {
	if account.IsCline() && account.Type == AccountTypeAPIKey && account.GetClineMode() == cline.ModePass && cline.IsOfficialBase(account.GetClineBaseURL()) {
		return nil
	}
	return ErrChannelMonitorAccountNotSupportable
}

// Cached-only monitoring never sends an inference/balance probe. Unknown windows
// remain absent and the overall result is not healthy unless all three are fresh.
func ClineMonitorQuotaSnapshot(account *Account, now time.Time) *domain.MonitorQuotaSnapshot {
	view := ClineMetadataForAccount(account, now)
	snapshot := &domain.MonitorQuotaSnapshot{Source: "usage", FetchedAt: now, CredentialInvalid: view.CredentialStatus == "invalid"}
	for _, window := range view.Windows {
		if window.PercentUsed == nil {
			continue
		}
		name := window.Type
		if name == "five_hour" {
			name = "5h"
		}
		tier := domain.MonitorQuotaTier{Window: name, Label: "cline", UsedPercent: *window.PercentUsed}
		if window.ResetsAt != nil {
			tier.ResetAt = window.ResetsAt.UTC().Format(time.RFC3339)
		}
		snapshot.Tiers = append(snapshot.Tiers, tier)
	}
	snapshot.Success = view.QuotaStatus == "ok" && len(snapshot.Tiers) == 3
	if !snapshot.Success {
		snapshot.Error = "Cline quota observation is incomplete or stale; refresh account metadata"
	}
	return snapshot
}
