package service

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

const clineMetadataBatchSize = 50

// Optional interface keeps the worker out of unrelated repositories and tests.
// Pages are keyset-ordered, bounded, and include accounts in observed cooldown.
type clineMetadataCandidateRepository interface {
	ListClineMetadataCandidates(context.Context, int64, int) ([]Account, error)
}

type clineMetadataWorker struct {
	mu       sync.Mutex
	cancel   context.CancelFunc
	done     chan struct{}
	wake     chan struct{}
	stopped  bool
	lastWake time.Time
}

// WithClineMetadataWorker controls automatic metadata refresh at composition
// time. Production defaults to enabled; deterministic HTTP fixtures disable it
// before construction so they cannot race an unrelated metadata request.
func WithClineMetadataWorker(enabled bool) OpenAIGatewayOption {
	return func(s *OpenAIGatewayService) { s.clineMetadataWorker.stopped = !enabled }
}

func (s *OpenAIGatewayService) StartClineMetadataWorker() {
	if s == nil || s.httpUpstream == nil {
		return
	}
	if _, ok := s.accountRepo.(clineMetadataCandidateRepository); !ok {
		return
	}
	w := &s.clineMetadataWorker
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.cancel != nil || w.stopped {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	w.cancel, w.done, w.wake = cancel, make(chan struct{}), make(chan struct{}, 1)
	go s.runClineMetadataWorker(ctx, w.done, w.wake)
}

func (s *OpenAIGatewayService) StopClineMetadataWorker() {
	if s == nil {
		return
	}
	w := &s.clineMetadataWorker
	w.mu.Lock()
	w.stopped = true
	cancel, done := w.cancel, w.done
	w.mu.Unlock()
	if cancel != nil {
		cancel()
		<-done
	}
}

// RequestClineMetadataRefresh only wakes the bounded worker; no per-request
// goroutine, inference probe, arbitrary URL or client credentials are captured.
func (s *OpenAIGatewayService) RequestClineMetadataRefresh() {
	if s == nil {
		return
	}
	w := &s.clineMetadataWorker
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.wake != nil && !w.stopped && time.Since(w.lastWake) >= 30*time.Second {
		w.lastWake = time.Now()
		select {
		case w.wake <- struct{}{}:
		default:
		}
	}
}

func (s *OpenAIGatewayService) runClineMetadataWorker(ctx context.Context, done chan struct{}, wake <-chan struct{}) {
	defer close(done)
	var cursor int64
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		case <-wake:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
		}
		next, full, err := s.refreshClineMetadataPage(ctx, cursor)
		delay := 30 * time.Second
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			slog.Warn("cline.metadata_scan_failed") // Never expose driver/credential details.
			cursor = 0
		} else if full && next > cursor {
			cursor, delay = next, time.Second
		} else {
			cursor = 0
		}
		timer.Reset(delay)
	}
}

func (s *OpenAIGatewayService) refreshClineMetadataPage(ctx context.Context, after int64) (int64, bool, error) {
	repo := s.accountRepo.(clineMetadataCandidateRepository)
	queryCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	accounts, err := repo.ListClineMetadataCandidates(queryCtx, after, clineMetadataBatchSize)
	cancel()
	if err != nil {
		return after, false, err
	}
	sem := make(chan struct{}, 4)
	var wg sync.WaitGroup
	next := after
	for i := range accounts {
		a := &accounts[i]
		if a.ID > next {
			next = a.ID
		}
		if !ClineMetadataRefreshDue(a, time.Now().UTC()) {
			continue
		}
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			wg.Wait()
			return next, false, ctx.Err()
		}
		wg.Add(1)
		go func(account *Account) {
			defer wg.Done()
			defer func() { <-sem }()
			_, _ = s.refreshClineMetadata(ctx, account, true)
		}(a)
	}
	wg.Wait()
	return next, len(accounts) == clineMetadataBatchSize, ctx.Err()
}
