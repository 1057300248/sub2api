//go:build integration

package handler

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/Wei-Shaw/sub2api/internal/pkg/cline"
	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

// The upstream and authenticated identity are synthetic. Handler dispatch,
// mapping, parsing, forwarding, resource ownership and SQL billing are real.
func TestClineHTTPRawChatAndDoneBoundaries(t *testing.T) {
	if err := exec.Command("docker", "info").Run(); err != nil {
		if os.Getenv("CI") != "" {
			t.Fatal("Docker required for Cline boundary regressions")
		}
		t.Skip("Docker required for disposable PostgreSQL")
	}
	ctx := context.Background()
	pg, err := tcpostgres.Run(ctx, "postgres:18.6-alpine", tcpostgres.WithDatabase("cline_p2"), tcpostgres.WithUsername("fixture"), tcpostgres.WithPassword("fixture-only"), tcpostgres.BasicWaitStrategies())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, pg.Terminate(context.Background())) })
	dsn, err := pg.ConnectionString(ctx, "sslmode=disable", "TimeZone=UTC")
	require.NoError(t, err)
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	var version string
	require.NoError(t, db.QueryRowContext(ctx, "SHOW server_version").Scan(&version))
	require.True(t, strings.HasPrefix(version, "18.6"), version)
	t.Logf("Disposable PostgreSQL %s", version)
	require.NoError(t, repository.ApplyMigrations(ctx, db))
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	settings := service.NewSettingService(repository.NewSettingRepository(client), &config.Config{})
	for _, platform := range []string{service.PlatformCline, service.PlatformOpenAI, service.PlatformDeepseek} {
		t.Run(platform, func(t *testing.T) {
			for _, scenario := range []string{"aliases_json", "aliases_stream", "duplicate_model", "duplicate_stream", "duplicate_escaped", "duplicate_tools", "duplicate_provider"} {
				t.Run(scenario, func(t *testing.T) { exerciseClineP2HTTP(t, client, db, settings, platform, "chat", scenario) })
			}
		})
	}
	for _, protocol := range []string{"chat", "responses", "messages"} {
		t.Run("done_"+protocol, func(t *testing.T) {
			exerciseClineP2HTTP(t, client, db, settings, service.PlatformCline, protocol, "done_deadline")
		})
	}
}

type clineP2Upstream struct {
	service.HTTPUpstream
	calls    int
	request  apicompat.ChatCompletionsRequest
	raw      []byte
	ctx      context.Context
	body     *clineP2Body
	keepOpen bool
}

func (u *clineP2Upstream) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	u.calls++
	u.ctx = req.Context()
	var err error
	u.raw, err = io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	// A real case-insensitive decoder determines upstream stream behavior.
	if err := json.Unmarshal(u.raw, &u.request); err != nil {
		return nil, err
	}
	payload := `{"id":"p2","model":"cline-pass/model","choices":[{"index":0,"message":{"role":"assistant","content":"fixture answer"},"finish_reason":"stop"}],"usage":{"prompt_tokens":8,"completion_tokens":4,"total_tokens":12}}`
	media := "application/json"
	if u.request.Stream {
		media = "text/event-stream"
		payload = "data: {\"id\":\"p2\",\"model\":\"cline-pass/model\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"fixture answer\"},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":8,\"completion_tokens\":4,\"total_tokens\":12}}\n\ndata: [DONE]\n\n"
	}
	u.body = &clineP2Body{reader: strings.NewReader(payload), ctx: req.Context(), keepOpen: u.keepOpen}
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{media}}, Body: u.body}, nil
}

type clineP2Body struct {
	reader     *strings.Reader
	ctx        context.Context
	keepOpen   bool
	closed     int
	readsAtEnd int
}

func (b *clineP2Body) Read(p []byte) (int, error) {
	if b.reader.Len() > 0 {
		return b.reader.Read(p)
	}
	if b.keepOpen {
		b.readsAtEnd++
		<-b.ctx.Done()
		return 0, context.Cause(b.ctx)
	}
	return 0, io.EOF
}
func (b *clineP2Body) Close() error { b.closed++; return nil }

type clineP2DeadlineWriter struct {
	*httptest.ResponseRecorder
	ctx      context.Context
	marker   string
	observed bool
}

func (w *clineP2DeadlineWriter) WriteString(s string) (int, error) {
	return w.Write([]byte(s))
}

func (w *clineP2DeadlineWriter) Write(p []byte) (int, error) {
	n, err := w.ResponseRecorder.Write(p)
	if !w.observed && strings.Contains(w.Body.String(), w.marker) {
		// The response is already committed, but Forward has not returned.
		// Wait for an actual deadline, not a post-Forward injected context.
		<-w.ctx.Done()
		w.observed = true
	}
	return n, err
}

func exerciseClineP2HTTP(t *testing.T, client *dbent.Client, db *sql.DB, settings *service.SettingService, platform, protocol, scenario string) {
	t.Helper()
	ctx := context.Background()
	name := "p2-" + platform + "-" + protocol + "-" + scenario
	duplicate := strings.HasPrefix(scenario, "duplicate_")
	done := scenario == "done_deadline"
	stream := scenario == "aliases_stream" || done
	group, err := client.Group.Create().SetName(name).SetPlatform(platform).SetAllowMessagesDispatch(protocol == "messages").SetRateMultiplier(2).Save(ctx)
	require.NoError(t, err)
	user, err := client.User.Create().SetEmail(name + "@example.invalid").SetPasswordHash("synthetic-not-a-login").SetBalance(100).SetConcurrency(1).Save(ctx)
	require.NoError(t, err)
	key, err := client.APIKey.Create().SetName(name).SetKey("fixture-" + name).SetUserID(user.ID).SetGroupID(group.ID).Save(ctx)
	require.NoError(t, err)
	accounts := repository.NewAccountRepository(client, db, nil)
	a := &service.Account{Name: name, Platform: platform, Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true, Concurrency: 1, Priority: 1, Credentials: map[string]any{"api_key": "synthetic-not-real", "account_mode": cline.ModePass, "cline_auth_type": cline.AuthAPIKey, "base_url": cline.BaseURL, "model_mapping": map[string]any{"gpt-5.1": "cline-pass/model"}}, Extra: map[string]any{"openai_responses_mode": "force_chat_completions"}}
	require.NoError(t, accounts.Create(ctx, a))
	require.NoError(t, accounts.BindGroups(ctx, a.ID, []int64{group.ID}))
	if platform == service.PlatformCline {
		now := time.Now().UTC()
		state := service.ClineState{Mode: cline.ModePass, CredentialFingerprint: service.ClineCredentialFingerprint(a), Identity: strings.Repeat("a", 64), IdentityVerifiedAt: &now, FetchedAt: &now, Persisted: true}
		encoded, err := json.Marshal(state)
		require.NoError(t, err)
		_, err = db.ExecContext(ctx, "UPDATE accounts SET extra=extra||jsonb_build_object('cline_state',$1::jsonb) WHERE id=$2", string(encoded), a.ID)
		require.NoError(t, err)
	}
	keys := repository.NewAPIKeyRepository(client, db)
	apiKey, err := keys.GetByID(ctx, key.ID)
	require.NoError(t, err)
	require.NotNil(t, apiKey.Group)
	apiKey.Group.Hydrated = true
	users := repository.NewUserRepository(client, db)
	subs := repository.NewUserSubscriptionRepository(client)
	cfg := &config.Config{RunMode: config.RunModeStandard}
	cfg.Default.RateMultiplier = 1
	cfg.Gateway.MaxAccountSwitches = 1
	cfg.Pricing.DataDir = t.TempDir()
	cfg.Pricing.FallbackFile = filepath.Join(t.TempDir(), "prices.json")
	price := `{"input_cost_per_token":0.000002,"output_cost_per_token":0.000005,"mode":"chat","litellm_provider":"openai"}`
	require.NoError(t, os.WriteFile(cfg.Pricing.FallbackFile, []byte(`{"gpt-5.1":`+price+`,"cline-pass/model":`+price+`}`), 0600))
	pricing := service.NewPricingService(cfg, nil)
	require.NoError(t, pricing.Initialize())
	defer pricing.Stop()
	cache := service.NewBillingCacheService(nil, users, subs, keys, nil, nil, cfg, nil)
	defer cache.Stop()
	ledger := &clineHTTPObservedLedger{UsageBillingRepository: repository.NewUsageBillingRepository(client, db)}
	slots := &concurrencyCacheMock{acquireUserSlotFn: func(context.Context, int64, int, string) (bool, error) { return true, nil }, acquireAccountSlotFn: func(context.Context, int64, int, string) (bool, error) { return true, nil }}
	concurrency := service.NewConcurrencyService(slots)
	limits := service.NewRateLimitService(accounts, nil, cfg, nil, nil)
	limits.SetSettingService(settings)
	upstream := &clineP2Upstream{keepOpen: done}
	gateway := service.NewOpenAIGatewayService(accounts, nil, repository.NewUsageLogRepository(client, db), ledger, users, subs, nil, nil, cfg, nil, concurrency, service.NewBillingService(cfg, pricing), limits, cache, upstream, &service.DeferredService{}, nil, nil, nil, nil, nil, settings, nil)
	h := NewOpenAIGatewayHandler(gateway, concurrency, cache, service.NewAPIKeyService(keys, users, nil, nil, nil, nil, cfg), nil, nil, nil, nil, cfg)
	router := gin.New()
	var reason string
	router.Use(func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyAPIKey), apiKey)
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: user.ID, Concurrency: 1})
		c.Next()
		reason = c.GetString("cline_forward_stop_reason")
	})
	router.POST("/chat/completions", h.ChatCompletions)
	router.POST("/openai/v1/responses", h.Responses)
	router.POST("/messages", h.Messages)
	payload := fmt.Sprintf(`{"model":"gpt-5.1","messages":[{"role":"user","content":"fixture"}],"stream":%t,"max_tokens":32,"tools":[]`, stream)
	if strings.HasPrefix(scenario, "aliases_") {
		payload += fmt.Sprintf(`,"MODEL":"foreign","STREAM":%t,"ſtream":%t,"TOOLS":[{"type":"unknown"}],"MAX_TOKENS":999,"max_toKens":999`, !stream, !stream)
	}
	switch scenario {
	case "duplicate_model":
		payload += `,"model":"foreign"`
	case "duplicate_stream":
		payload += `,"stream":true`
	case "duplicate_escaped":
		payload += `,"\u0073tream":true`
	case "duplicate_tools":
		payload += `,"tools":[]`
	case "duplicate_provider":
		payload += `,"providerOptions":{}`
	}
	payload += `,"providerOptions":{"gateway":{"only":["deepseek"]}},"vendor":{"zero":0,"off":false,"large":9007199254740993,"nested":{"MODEL":"untouched"}}}`
	path, marker := "/chat/completions", "[DONE]"
	if protocol == "responses" {
		path, marker = "/openai/v1/responses", "response.completed"
		payload = `{"model":"gpt-5.1","input":"fixture","stream":true}`
	} else if protocol == "messages" {
		path, marker = "/messages", "message_stop"
	}
	requestCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(payload)).WithContext(requestCtx)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	deadlineWriter := &clineP2DeadlineWriter{ResponseRecorder: rec, ctx: requestCtx, marker: marker}
	var writer http.ResponseWriter = rec
	if done {
		writer = deadlineWriter
	}
	router.ServeHTTP(writer, req)
	if duplicate {
		require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
		require.Zero(t, upstream.calls)
		require.True(t, json.Valid(rec.Body.Bytes()))
		var balance float64
		var count int
		require.NoError(t, db.QueryRowContext(ctx, "SELECT balance FROM users WHERE id=$1", user.ID).Scan(&balance))
		require.NoError(t, db.QueryRowContext(ctx, "SELECT count(*) FROM usage_billing_dedup WHERE api_key_id=$1", key.ID).Scan(&count))
		require.Equal(t, 100.0, balance)
		require.Zero(t, count)
		return
	}
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, 1, upstream.calls)
	require.Equal(t, "cline-pass/model", upstream.request.Model)
	require.Equal(t, stream, upstream.request.Stream)
	require.Equal(t, 1, upstream.body.closed)
	require.Equal(t, int32(1), slots.releaseUserCalled)
	require.Equal(t, int32(1), slots.releaseAccountCalled)
	require.Empty(t, reason)
	if done {
		require.True(t, deadlineWriter.observed, "the caller deadline must expire inside Forward after terminal output")
		require.ErrorIs(t, requestCtx.Err(), context.DeadlineExceeded)
		require.Zero(t, upstream.body.readsAtEnd, "do not wait for upstream EOF after [DONE]")
		require.Error(t, upstream.ctx.Err(), "upstream resources must close even after successful completion")
		require.NotContains(t, rec.Body.String(), "Request deadline exceeded")
		require.NotContains(t, rec.Body.String(), `"type":"error"`)
		require.Contains(t, rec.Body.String(), marker)
	} else {
		require.NotNil(t, upstream.request.MaxTokens)
		require.Equal(t, 32, *upstream.request.MaxTokens)
		require.Empty(t, upstream.request.Tools)
		var sent map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(upstream.raw, &sent))
		for _, alias := range []string{"MODEL", "STREAM", "ſtream", "TOOLS", "MAX_TOKENS", "max_toKens"} {
			require.NotContains(t, sent, alias)
		}
		require.JSONEq(t, `{"gateway":{"only":["deepseek"]}}`, string(sent["providerOptions"]))
		require.Equal(t, `{"zero":0,"off":false,"large":9007199254740993,"nested":{"MODEL":"untouched"}}`, string(sent["vendor"]))
	}
	var count int
	var balance, cost float64
	require.Eventually(t, func() bool {
		err := db.QueryRowContext(ctx, "SELECT count(*),COALESCE(sum(actual_cost),0) FROM usage_logs WHERE api_key_id=$1", key.ID).Scan(&count, &cost)
		return err == nil && count == 1
	}, 5*time.Second, 20*time.Millisecond)
	ledger.mu.Lock()
	calls, command := ledger.calls, ledger.last
	ledger.mu.Unlock()
	require.Equal(t, 1, calls)
	require.NotNil(t, command)
	const charge = 2 * (8*0.000002 + 4*0.000005)
	require.InDelta(t, charge, cost, 1e-8)
	require.NoError(t, db.QueryRowContext(ctx, "SELECT balance FROM users WHERE id=$1", user.ID).Scan(&balance))
	require.InDelta(t, 100-charge, balance, 1e-8)
	replayed, err := ledger.UsageBillingRepository.Apply(ctx, command)
	require.NoError(t, err)
	require.False(t, replayed.Applied)
	var after float64
	require.NoError(t, db.QueryRowContext(ctx, "SELECT balance FROM users WHERE id=$1", user.ID).Scan(&after))
	require.Equal(t, balance, after)
}
