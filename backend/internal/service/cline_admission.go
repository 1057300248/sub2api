package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

var ErrClineAdmissionUnavailable = infraerrors.New(http.StatusServiceUnavailable, "CLINE_ADMISSION_UNAVAILABLE", "Cline durable scheduling state is unavailable")
var ErrClineMetadataRequired = infraerrors.New(http.StatusConflict, "CLINE_METADATA_REQUIRED", "Refresh the saved Cline credential metadata before forwarding")

var ErrClineObservedCooldown = infraerrors.New(http.StatusTooManyRequests, "CLINE_OBSERVED_COOLDOWN", "Cline has an observed account or model cooldown")

type clineAdmissionRepository interface {
	CheckClineAdmission(context.Context, *Account, string) error
}

func (s *OpenAIGatewayService) checkClineAdmission(ctx context.Context, account *Account, body []byte) error {
	if !account.IsCline() {
		return nil
	}
	if s == nil {
		return ErrClineAdmissionUnavailable
	}
	repo, ok := s.accountRepo.(clineAdmissionRepository)
	if !ok {
		return ErrClineAdmissionUnavailable
	}
	var payload struct {
		Model string `json:"model"`
	}
	if json.Unmarshal(body, &payload) != nil {
		return ErrClineAdmissionUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	err := repo.CheckClineAdmission(ctx, account, payload.Model)
	if errors.Is(err, ErrClineMetadataRequired) || errors.Is(err, ErrClineObservedCooldown) {
		s.RequestClineMetadataRefresh()
	}
	if err == nil || errors.Is(err, ErrClineObservedCooldown) || errors.Is(err, ErrClineMetadataChanged) || errors.Is(err, ErrClineMetadataRequired) {
		return err
	}
	// Database diagnostics may contain JSON credentials. Expose/log only the
	// fixed service error here, not an untrusted driver error or query arguments.
	return ErrClineAdmissionUnavailable
}
