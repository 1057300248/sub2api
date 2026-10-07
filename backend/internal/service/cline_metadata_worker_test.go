//go:build unit

package service

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/cline"
	"github.com/stretchr/testify/require"
)

type clineWorkerRepository struct {
	AccountRepository
	accounts     []Account
	mu           sync.Mutex
	saved        int
	queryStarted chan struct{}
	blockQuery   bool
}

func (r *clineWorkerRepository) ListClineMetadataCandidates(ctx context.Context, _ int64, _ int) ([]Account, error) {
	if r.queryStarted != nil {
		select {
		case r.queryStarted <- struct{}{}:
		default:
		}
	}
	if r.blockQuery {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return r.accounts, nil
}
func (r *clineWorkerRepository) ClaimClineMetadataRefresh(context.Context, *Account, time.Time) (bool, error) {
	return true, nil
}
func (r *clineWorkerRepository) SaveClineStateIfUnchanged(context.Context, *Account, *ClineState) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.saved++
	return true, nil
}

type clineWorkerHTTP struct {
	HTTPUpstream
	active, maximum atomic.Int32
	gate            chan struct{}
	entered         chan struct{}
}

func (u *clineWorkerHTTP) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	n := u.active.Add(1)
	defer u.active.Add(-1)
	for old := u.maximum.Load(); n > old; old = u.maximum.Load() {
		if u.maximum.CompareAndSwap(old, n) {
			break
		}
	}
	if u.entered != nil {
		select {
		case u.entered <- struct{}{}:
		default:
		}
	}
	if u.gate != nil {
		select {
		case <-u.gate:
		case <-req.Context().Done():
			return nil, req.Context().Err()
		}
	}
	body := `{"recommended":[],"clinePass":[],"free":[]}`
	if req.URL.String() == cline.ProfileURL {
		body = `{"id":"fixture","active_account_id":"worker-fixture"}`
	} else if req.URL.String() == cline.UsageURL {
		body = `{"success":true,"data":{"limits":[{"type":"five_hour","percentUsed":0},{"type":"weekly","percentUsed":10},{"type":"monthly","percentUsed":20}]}}`
	}
	return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
}

func TestClineMetadataWorkerBoundedConcurrencyAndModeIsolation(t *testing.T) {
	repo := &clineWorkerRepository{}
	for i := int64(1); i <= 9; i++ {
		a := clineRoutingAccount(t, cline.ModePass, cline.AuthAPIKey)
		a.ID = i
		if i == 9 {
			a.Credentials["account_mode"] = cline.ModeFree
		}
		repo.accounts = append(repo.accounts, *a)
	}
	u := &clineWorkerHTTP{gate: make(chan struct{}), entered: make(chan struct{}, 20)}
	s := &OpenAIGatewayService{accountRepo: repo, httpUpstream: u}
	done := make(chan error, 1)
	go func() { _, _, err := s.refreshClineMetadataPage(context.Background(), 0); done <- err }()
	for i := 0; i < 4; i++ {
		select {
		case <-u.entered:
		case <-time.After(time.Second):
			t.Fatal("four workers did not start")
		}
	}
	require.EqualValues(t, 4, u.maximum.Load())
	close(u.gate)
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("worker page did not finish")
	}
	require.EqualValues(t, 4, u.maximum.Load())
	repo.mu.Lock()
	saved := repo.saved
	repo.mu.Unlock()
	require.Equal(t, 8, saved)
}

func TestClineMetadataWorkerShutdownCancelsInflightQuery(t *testing.T) {
	repo := &clineWorkerRepository{blockQuery: true, queryStarted: make(chan struct{}, 1)}
	s := &OpenAIGatewayService{accountRepo: repo, httpUpstream: &clineWorkerHTTP{}}
	s.StartClineMetadataWorker()
	s.StartClineMetadataWorker()
	select {
	case <-repo.queryStarted:
	case <-time.After(time.Second):
		t.Fatal("worker was not started")
	}
	done := make(chan struct{})
	go func() { s.StopClineMetadataWorker(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("shutdown did not cancel query")
	}
	s.StopClineMetadataWorker()
	s.StartClineMetadataWorker()
	s.RequestClineMetadataRefresh()
	select {
	case <-repo.queryStarted:
		t.Fatal("stopped worker restarted")
	default:
	}
}

func TestClineMetadataRetryAfterDoesNotBecomeInferenceCooldown(t *testing.T) {
	a, repo, transport, gateway := clineMetadataFixture(t)
	transport.statuses = map[string]int{cline.CatalogURL: 429}
	view, err := gateway.RefreshClineMetadata(context.Background(), a)
	require.NoError(t, err)
	require.Len(t, transport.requests, 1, "metadata 429 must stop further same-origin probes")
	require.Empty(t, view.Cooldowns)
	require.NotNil(t, repo.stored.MetadataRetryAt)
	require.GreaterOrEqual(t, repo.stored.NextRefreshAt.Sub(*repo.stored.FetchedAt), 30*time.Second)
	now := time.Now().UTC().Truncate(time.Second)
	for _, value := range []string{"3600", now.Add(time.Hour).Format(http.TimeFormat)} {
		require.Equal(t, now.Add(time.Hour), clineMetadataRetryAt(http.Header{"Retry-After": []string{value}}, now))
	}
	for _, value := range []string{"-1", "garbage", "9223372036854775807"} {
		require.Equal(t, now.Add(30*time.Second), clineMetadataRetryAt(http.Header{"Retry-After": []string{value}}, now))
	}
}

func TestClineMetadataWorkerMissingRepository(t *testing.T) {
	s := &OpenAIGatewayService{}
	next, full, err := s.refreshClineMetadataPage(context.Background(), 7)
	require.ErrorIs(t, err, ErrClineMetadataUnavailable)
	require.EqualValues(t, 7, next)
	require.False(t, full)
}
