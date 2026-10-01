//go:build unit

package service

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/cline"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type clineProbeAccountRepository struct {
	AccountRepository
	account *Account
}

func (r *clineProbeAccountRepository) GetByID(context.Context, int64) (*Account, error) {
	return r.account, nil
}

func TestClineAdministrativeProbeUsesGuardedPipeline(t *testing.T) {
	for _, failure := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "failure"}[failure], func(t *testing.T) {
			a := clineRoutingAccount(t, cline.ModePass, cline.AuthAccountToken)
			payload := "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\ndata: [DONE]\n\n"
			if failure {
				payload = "data: {\"choices\":[{\"finish_reason\":\"error\"}]}\n\ndata: [DONE]\n\n"
			}
			upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(payload))}}
			gateway := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream}
			s := &AccountTestService{accountRepo: &clineProbeAccountRepository{account: a}, openaiGatewayService: gateway}
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest("POST", "/api/v1/admin/accounts/42/test", nil)
			err := s.TestAccountConnection(c, a.ID, "public-model", "hi", "default")
			require.Equal(t, failure, err != nil)
			require.NotNil(t, upstream.lastReq)
			require.Equal(t, cline.BaseURL+"/chat/completions", upstream.lastReq.URL.String())
			require.Equal(t, "cline-pass/model", gjson.GetBytes(upstream.lastBody, "model").String())
			require.Equal(t, int64(64), gjson.GetBytes(upstream.lastBody, "max_tokens").Int())
			if failure {
				require.NotContains(t, rec.Body.String(), `"success":true`)
			} else {
				require.Contains(t, rec.Body.String(), `"success":true`)
			}
		})
	}
}

func TestClineAdministrativeProbeRejectsImplicitModelsAndNativeModes(t *testing.T) {
	for _, tc := range []struct{ accountMode, model, mode string }{
		{cline.ModePass, "", "default"}, {cline.ModePass, "not-mapped", "default"}, {cline.ModePass, "public-model", "compact"}, {cline.ModePass, "public-model", "image"}, {cline.ModeFree, "public-model", "default"}, {cline.ModeUnknown, "public-model", "default"},
	} {
		a := clineRoutingAccount(t, tc.accountMode, cline.AuthAPIKey)
		upstream := &httpUpstreamRecorder{}
		s := &AccountTestService{openaiGatewayService: &OpenAIGatewayService{httpUpstream: upstream}}
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest("POST", "/test", nil)
		require.Error(t, s.testClineAccountConnection(c, a, tc.model, "hi", tc.mode))
		require.Nil(t, upstream.lastReq)
	}
}
