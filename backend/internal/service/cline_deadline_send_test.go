//go:build unit

package service

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/cline"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type clineDeadlineHeaderUpstream struct{ HTTPUpstream }

func (*clineDeadlineHeaderUpstream) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	<-req.Context().Done()
	return nil, context.Cause(req.Context())
}

func TestClineDeadlineSendPreservesCauseBeforeHeaders(t *testing.T) {
	parent, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/messages", nil).WithContext(parent)
	s := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), accountRepo: &clineAdmissionTestRepository{}, httpUpstream: &clineDeadlineHeaderUpstream{}}
	resp, err := s.sendCCUpstreamRequest(context.Background(), c, clineRoutingAccount(t, cline.ModePass, cline.AuthAPIKey), cline.BaseURL+"/chat/completions", []byte(`{"model":"cline-pass/model","messages":[{"role":"user","content":"fixture"}]}`), false, "synthetic", "", "")
	require.Nil(t, resp)
	require.ErrorIs(t, err, ErrClineCallerDeadline)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.NotErrorIs(t, err, context.Canceled)
	var failover *UpstreamFailoverError
	require.False(t, errors.As(err, &failover))
	require.False(t, c.Writer.Written(), "caller deadline belongs to the handler, not a generic upstream 502")
}
