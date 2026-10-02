//go:build integration

package handler

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	_ "github.com/Wei-Shaw/sub2api/ent/runtime"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/cline"
	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

// This test starts its own disposable database and applies the application's
// complete migration set. There is no external DSN, real key or paid inference.
// Auth identity and upstream I/O are synthetic; handlers, forwarding, admission,
// RecordUsage, SQL debit/dedup and usage-log persistence are production code.
func TestClineHTTPHandlersDurableBillingAndTermination(t *testing.T) {
	if err := exec.Command("docker", "info").Run(); err != nil {
		if os.Getenv("CI") != "" {
			t.Fatal("Docker required for Cline handler/ledger integration")
		}
		t.Skip("Docker required for disposable handler/ledger database")
	}
	ctx := context.Background()
	pg, err := tcpostgres.Run(ctx, "postgres:16-alpine", tcpostgres.WithDatabase("cline_handler_fixture"), tcpostgres.WithUsername("fixture"), tcpostgres.WithPassword("fixture-only"), tcpostgres.BasicWaitStrategies())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, pg.Terminate(context.Background())) })
	dsn, err := pg.ConnectionString(ctx, "sslmode=disable", "TimeZone=UTC")
	require.NoError(t, err)
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	db.SetMaxOpenConns(12)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	require.NoError(t, repository.ApplyMigrations(ctx, db))
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	// Enable the real scheduler in this disposable database. A healthy control
	// must record a sample, otherwise zero health samples on cancellation would
	// be a vacuous assertion against an unconfigured scheduler.
	settings := service.NewSettingService(repository.NewSettingRepository(client), &config.Config{})
	original, err := settings.GetAllSettings(ctx)
	require.NoError(t, err)
	enabled := *original
	enabled.OpenAIAdvancedSchedulerEnabled = true
	require.NoError(t, settings.UpdateSettings(ctx, &enabled))
	t.Cleanup(func() { require.NoError(t, settings.UpdateSettings(context.Background(), original)) })
	for _, protocol := range []string{"responses", "messages", "chat"} {
		t.Run(protocol, func(t *testing.T) {
			t.Parallel()
			for _, scenario := range []string{"complete", "cancel_then_usage", "writer_stall", "deadline", "deadline_headers"} {
				t.Run(scenario, func(t *testing.T) { exerciseClineHTTPHandler(t, client, db, settings, protocol, scenario) })
			}
		})
	}
}

type clineHTTPObservedLedger struct {
	service.UsageBillingRepository
	mu    sync.Mutex
	calls int
	last  *service.UsageBillingCommand
}

func (l *clineHTTPObservedLedger) Apply(ctx context.Context, cmd *service.UsageBillingCommand) (*service.UsageBillingApplyResult, error) {
	result, err := l.UsageBillingRepository.Apply(ctx, cmd)
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls++
	copy := *cmd
	l.last = &copy
	return result, err
}

type clineHTTPFixtureUpstream struct {
	service.HTTPUpstream
	scenario       string
	cancel         context.CancelFunc
	abort          chan struct{}
	calls          atomic.Int32
	requestContext context.Context
	closed         atomic.Int32
}

func (u *clineHTTPFixtureUpstream) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	u.calls.Add(1)
	u.requestContext = req.Context()
	if u.scenario == "deadline_headers" {
		select {
		case <-req.Context().Done():
			return nil, context.Cause(req.Context())
		case <-u.abort:
			return nil, io.ErrClosedPipe
		}
	}
	payload := "data: {\"id\":\"cline-handler\",\"model\":\"cline-pass/model\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"fixture answer\"}}],\"usage\":{\"prompt_tokens\":8,\"completion_tokens\":4,\"total_tokens\":12}}\n\n"
	hang := u.scenario == "writer_stall" || u.scenario == "deadline"
	if !hang {
		payload += "data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"
	}
	if u.scenario == "cancel_then_usage" {
		u.cancel()
	}
	body := &clineHTTPFixtureBody{reader: strings.NewReader(payload), ctx: req.Context(), hang: hang, closed: &u.closed, delay: u.scenario == "cancel_then_usage", abort: u.abort}
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}, "X-Request-Id": []string{"cline-handler-" + u.scenario}}, Body: body}, nil
}

type clineHTTPFixtureBody struct {
	reader      *strings.Reader
	ctx         context.Context
	hang, delay bool
	abort       <-chan struct{}
	closed      *atomic.Int32
}

func (b *clineHTTPFixtureBody) Read(p []byte) (int, error) {
	if b.delay {
		b.delay = false
		timer := time.NewTimer(20 * time.Millisecond)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-b.ctx.Done():
			return 0, context.Cause(b.ctx)
		}
	}
	if b.reader.Len() > 0 {
		return b.reader.Read(p)
	}
	if !b.hang {
		return 0, io.EOF
	}
	select {
	case <-b.ctx.Done():
		return 0, context.Cause(b.ctx)
	case <-b.abort:
		return 0, io.ErrClosedPipe
	}
}
func (b *clineHTTPFixtureBody) Close() error { b.closed.Add(1); return nil }

type clineHTTPBrokenWriter struct {
	http.ResponseWriter
	failures int
}

func (w *clineHTTPBrokenWriter) Write([]byte) (int, error) { w.failures++; return 0, io.ErrClosedPipe }
func (w *clineHTTPBrokenWriter) Flush()                    {}

func exerciseClineHTTPHandler(t *testing.T, client *dbent.Client, db *sql.DB, settings *service.SettingService, protocol, scenario string) {
	t.Helper()
	ctx := context.Background()
	name := protocol + "-" + scenario
	group, err := client.Group.Create().SetName(name).SetPlatform(service.PlatformCline).SetRateMultiplier(2).Save(ctx)
	require.NoError(t, err)
	user, err := client.User.Create().SetEmail(name + "@example.invalid").SetPasswordHash("synthetic-not-a-login").SetBalance(100).SetConcurrency(1).Save(ctx)
	require.NoError(t, err)
	key, err := client.APIKey.Create().SetName(name).SetKey("fixture-" + name).SetUserID(user.ID).SetGroupID(group.ID).Save(ctx)
	require.NoError(t, err)
	accounts := repository.NewAccountRepository(client, db, nil)
	a := &service.Account{Name: name, Platform: service.PlatformCline, Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true, Concurrency: 1, Priority: 1, Credentials: map[string]any{"api_key": "synthetic-cline-key", "account_mode": cline.ModePass, "cline_auth_type": cline.AuthAPIKey, "base_url": cline.BaseURL, "model_mapping": map[string]any{"gpt-5.1": "cline-pass/model"}}}
	require.NoError(t, accounts.Create(ctx, a))
	require.NoError(t, accounts.BindGroups(ctx, a.ID, []int64{group.ID}))
	now := time.Now().UTC()
	state := service.ClineState{Mode: cline.ModePass, CredentialFingerprint: service.ClineCredentialFingerprint(a), Identity: strings.Repeat("a", 64), IdentityVerifiedAt: &now, FetchedAt: &now, Persisted: true}
	// Seed the verified identity locally; never contact the provider in a test.
	encoded, err := json.Marshal(state)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, "UPDATE accounts SET extra=jsonb_build_object('cline_state',$1::jsonb) WHERE id=$2", string(encoded), a.ID)
	require.NoError(t, err)
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
	cfg.Pricing.FallbackFile = filepath.Join(t.TempDir(), "fixture-prices.json")
	price := `{"input_cost_per_token":0.000002,"output_cost_per_token":0.000005,"mode":"chat","litellm_provider":"openai"}`
	require.NoError(t, os.WriteFile(cfg.Pricing.FallbackFile, []byte(`{"gpt-5.1":`+price+`,"cline-pass/model":`+price+`}`), 0600))
	pricing := service.NewPricingService(cfg, nil)
	require.NoError(t, pricing.Initialize())
	defer pricing.Stop()
	cache := service.NewBillingCacheService(nil, users, subs, keys, nil, nil, cfg, nil)
	defer cache.Stop()
	ledger := &clineHTTPObservedLedger{UsageBillingRepository: repository.NewUsageBillingRepository(client, db)}
	parent, cancel := context.WithCancel(ctx)
	defer cancel()
	if strings.HasPrefix(scenario, "deadline") {
		var stop context.CancelFunc
		parent, stop = context.WithTimeout(parent, time.Second)
		defer stop()
	}
	upstream := &clineHTTPFixtureUpstream{scenario: scenario, cancel: cancel, abort: make(chan struct{})}
	slots := &concurrencyCacheMock{acquireUserSlotFn: func(context.Context, int64, int, string) (bool, error) { return true, nil }, acquireAccountSlotFn: func(context.Context, int64, int, string) (bool, error) { return true, nil }}
	concurrency := service.NewConcurrencyService(slots)
	rateLimits := service.NewRateLimitService(accounts, nil, cfg, nil, nil)
	rateLimits.SetSettingService(settings)
	gateway := service.NewOpenAIGatewayService(accounts, nil, repository.NewUsageLogRepository(client, db), ledger, users, subs, nil, nil, cfg, nil, concurrency, service.NewBillingService(cfg, pricing), rateLimits, cache, upstream, &service.DeferredService{}, nil, nil, nil, nil, nil, settings, nil)
	h := NewOpenAIGatewayHandler(gateway, concurrency, cache, service.NewAPIKeyService(keys, users, nil, nil, nil, nil, cfg), nil, nil, nil, nil, cfg)
	var reason string
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyAPIKey), apiKey)
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: user.ID, Concurrency: 1})
		c.Next()
		reason = c.GetString("cline_forward_stop_reason")
	})
	router.POST("/openai/v1/responses", h.Responses)
	router.POST("/messages", h.Messages)
	router.POST("/chat/completions", h.ChatCompletions)
	path := "/chat/completions"
	payload := `{"model":"gpt-5.1","messages":[{"role":"user","content":"fixture"}],"max_tokens":32,"stream":true}`
	if protocol == "responses" {
		path = "/openai/v1/responses"
		payload = `{"model":"gpt-5.1","input":"fixture","stream":true}`
	}
	if protocol == "messages" {
		path = "/messages"
	}
	req := httptest.NewRequest("POST", path, strings.NewReader(payload)).WithContext(parent)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	var writer http.ResponseWriter = rec
	broken := &clineHTTPBrokenWriter{ResponseWriter: rec}
	if scenario == "writer_stall" {
		writer = broken
	}
	started := time.Now()
	finished := make(chan struct{})
	go func() { defer close(finished); router.ServeHTTP(writer, req) }()
	select {
	case <-finished:
	case <-time.After(45 * time.Second):
		cancel()
		// Stop only the synthetic source; never weaken the production timeout.
		close(upstream.abort)
		select {
		case <-finished:
		case <-time.After(10 * time.Second):
		}
		t.Fatal("handler exceeded the bounded disconnect acceptance deadline")
	}
	t.Logf("%s/%s: elapsed=%v status=%d reason=%s", protocol, scenario, time.Since(started), rec.Code, reason)
	require.Equal(t, int32(1), upstream.calls.Load(), "no failover for a disconnected caller; body=%s", rec.Body.String())
	if scenario == "deadline_headers" {
		require.Zero(t, upstream.closed.Load(), "no response body was obtained")
		require.Equal(t, http.StatusGatewayTimeout, rec.Code)
	} else {
		require.Equal(t, int32(1), upstream.closed.Load(), "upstream body must close exactly once")
	}
	require.Error(t, upstream.requestContext.Err(), "request resources remain live after handler return")
	require.Equal(t, int32(1), atomic.LoadInt32(&slots.releaseAccountCalled))
	require.Equal(t, int32(1), atomic.LoadInt32(&slots.releaseUserCalled))
	if scenario == "complete" {
		require.Empty(t, reason)
	} else if strings.HasPrefix(scenario, "deadline") {
		require.Equal(t, "deadline_exceeded", reason)
		require.ErrorIs(t, context.Cause(upstream.requestContext), service.ErrClineCallerDeadline)
		require.NotContains(t, rec.Body.String(), "response.completed")
		require.NotContains(t, rec.Body.String(), "message_stop")
		require.NotContains(t, rec.Body.String(), "[DONE]")
		require.Contains(t, rec.Body.String(), "Request deadline exceeded")
	} else {
		require.Equal(t, "client_disconnected", reason)
		if scenario == "writer_stall" {
			require.NoError(t, parent.Err(), "writer test must not rely on request-context cancellation")
			require.Positive(t, broken.failures)
			require.ErrorIs(t, context.Cause(upstream.requestContext), service.ErrClineDrainTimeout)
			require.Less(t, time.Since(started), 40*time.Second, "writer failure waited for the 30-minute ceiling")
		}
	}
	metrics := gateway.SnapshotOpenAIAccountSchedulerMetrics()
	require.Zero(t, metrics.AccountSwitchTotal)
	if scenario != "complete" {
		require.Zero(t, metrics.RuntimeStatsAccountCount, "client termination must not add a failure or recovery sample")
	} else {
		require.Positive(t, metrics.RuntimeStatsAccountCount, "healthy control must exercise the scheduling-result path")
	}
	current, err := accounts.GetByID(ctx, a.ID)
	require.NoError(t, err)
	require.Equal(t, service.StatusActive, current.Status)
	require.True(t, current.Schedulable)
	ledger.mu.Lock()
	calls, last := ledger.calls, ledger.last
	ledger.mu.Unlock()
	if scenario == "deadline_headers" {
		require.Zero(t, calls, "no observed usage must not manufacture a debit")
		var balance float64
		require.NoError(t, db.QueryRowContext(ctx, "SELECT balance FROM users WHERE id=$1", user.ID).Scan(&balance))
		require.Equal(t, 100.0, balance)
		var count int
		require.NoError(t, db.QueryRowContext(ctx, "SELECT count(*) FROM usage_billing_dedup WHERE api_key_id=$1", key.ID).Scan(&count))
		require.Zero(t, count)
		return
	}
	require.Equal(t, 1, calls, "real handler must invoke durable billing once")
	require.NotNil(t, last)
	const charge = 2 * (8*0.000002 + 4*0.000005)
	require.InDelta(t, charge, last.BalanceCost, 1e-12)
	var balance float64
	require.NoError(t, db.QueryRowContext(ctx, "SELECT balance FROM users WHERE id=$1", user.ID).Scan(&balance))
	require.InDelta(t, 100-charge, balance, 1e-8)
	var count, inputTokens, outputTokens int
	var actualCost float64
	require.Eventually(t, func() bool {
		err := db.QueryRowContext(ctx, "SELECT count(*),COALESCE(sum(input_tokens),0),COALESCE(sum(output_tokens),0),COALESCE(sum(actual_cost),0) FROM usage_logs WHERE api_key_id=$1", key.ID).Scan(&count, &inputTokens, &outputTokens, &actualCost)
		return err == nil && count == 1
	}, 5*time.Second, 20*time.Millisecond, "usage log was not persisted")
	require.Equal(t, 8, inputTokens)
	require.Equal(t, 4, outputTokens)
	require.InDelta(t, charge, actualCost, 1e-8)
	// Replay the real committed command against the same PostgreSQL ledger.
	// Applied:false is asserted from SQL, never returned by a test fake.
	replay, err := ledger.UsageBillingRepository.Apply(ctx, last)
	require.NoError(t, err)
	require.False(t, replay.Applied)
	var after float64
	require.NoError(t, db.QueryRowContext(ctx, "SELECT balance FROM users WHERE id=$1", user.ID).Scan(&after))
	require.Equal(t, balance, after)
	require.NoError(t, db.QueryRowContext(ctx, "SELECT count(*) FROM usage_billing_dedup WHERE api_key_id=$1", key.ID).Scan(&count))
	require.Equal(t, 1, count)
}
