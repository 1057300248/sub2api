//go:build unit

package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/cline"
	"github.com/stretchr/testify/require"
)

type clineAdmissionTestRepository struct {
	AccountRepository
	err   error
	calls int
}

func (r *clineAdmissionTestRepository) CheckClineAdmission(context.Context, *Account, string) error {
	r.calls++
	return r.err
}

func TestClineAdmissionFailsClosedWithoutDurableState(t *testing.T) {
	a := clineRoutingAccount(t, cline.ModePass, cline.AuthAPIKey)
	body := []byte(`{"model":"cline-pass/model"}`)
	require.ErrorIs(t, (&OpenAIGatewayService{}).checkClineAdmission(context.Background(), a, body), ErrClineAdmissionUnavailable)
	for _, err := range []error{errors.New("private SQL credential fixture"), ErrClineObservedCooldown, ErrClineMetadataChanged} {
		repo := &clineAdmissionTestRepository{err: err}
		gateway := &OpenAIGatewayService{accountRepo: repo}
		got := gateway.checkClineAdmission(context.Background(), a, body)
		require.Error(t, got)
		require.NotContains(t, got.Error(), "private SQL")
		require.Equal(t, 1, repo.calls)
	}
	a.Platform = PlatformDeepseek
	require.NoError(t, (&OpenAIGatewayService{}).checkClineAdmission(context.Background(), a, body))
}
