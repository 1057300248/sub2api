package service

import (
	"context"
	"errors"
	"io"
	"sync"
	"time"
)

const clineDrainGrace = 30 * time.Second
const clineRequestCeiling = 30 * time.Minute

var ErrClineDrainTimeout = errors.New("cline disconnected response drain deadline exceeded")
var ErrClineRequestTimeout = errors.New("cline upstream request lifetime exceeded")

// Each request owns its timers and cancellation. Parent cancellation starts a
// grace period, not an unbounded detached read. The overall ceiling also covers
// transports waiting for response headers and lost cancellation notifications.
// This does not extend the downstream client's timeout or lower first-token time.
type clineRequestLifetime struct {
	ctx         context.Context
	cancel      context.CancelCauseFunc
	mu          sync.Mutex
	closed      bool
	draining    bool
	drainTimer  *time.Timer
	totalTimer  *time.Timer
	stopParents []func() bool
	grace       time.Duration
}

func newClineRequestLifetime(parent context.Context, grace, ceiling time.Duration) *clineRequestLifetime {
	if parent == nil {
		parent = context.Background()
	}
	if grace <= 0 {
		grace = clineDrainGrace
	}
	if ceiling <= 0 {
		ceiling = clineRequestCeiling
	}
	ctx, cancel := context.WithCancelCause(context.WithoutCancel(parent))
	lifetime := &clineRequestLifetime{ctx: ctx, cancel: cancel, grace: grace}
	lifetime.totalTimer = time.AfterFunc(ceiling, func() { cancel(ErrClineRequestTimeout) })
	lifetime.watch(parent)
	// A parent already canceled at entry must not wait for goroutine scheduling.
	if parent.Err() != nil {
		lifetime.beginDrain()
	}
	return lifetime
}

func (l *clineRequestLifetime) beginDrain() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed || l.draining {
		return
	}
	l.draining = true
	l.drainTimer = time.AfterFunc(l.grace, func() { l.cancel(ErrClineDrainTimeout) })
}
func (l *clineRequestLifetime) close() {
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return
	}
	l.closed = true
	if l.drainTimer != nil {
		l.drainTimer.Stop()
	}
	if l.totalTimer != nil {
		l.totalTimer.Stop()
	}
	stops := l.stopParents
	l.mu.Unlock()
	for _, stop := range stops {
		stop()
	}
	l.cancel(context.Canceled)
}

type clineLifetimeBody struct {
	source    io.ReadCloser
	lifetime  *clineRequestLifetime
	closeOnce sync.Once
	closeErr  error
}

func (b *clineLifetimeBody) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if err := context.Cause(b.lifetime.ctx); err != nil {
		return 0, err
	}
	n, err := b.source.Read(p)
	if err != nil {
		if cause := context.Cause(b.lifetime.ctx); cause != nil {
			return n, cause
		}
	}
	return n, err
}
func (b *clineLifetimeBody) Close() error {
	b.closeOnce.Do(func() { b.lifetime.close(); b.closeErr = b.source.Close() })
	return b.closeErr
}

// Write failures can precede the HTTP server's request-context notification.
func (b *clineLifetimeBody) beginDrain() { b.lifetime.beginDrain() }
func beginClineBodyDrain(body io.ReadCloser) {
	if bounded, ok := body.(interface{ beginDrain() }); ok {
		bounded.beginDrain()
	}
}

func (l *clineRequestLifetime) watch(parent context.Context) {
	if parent == nil {
		return
	}
	stop := context.AfterFunc(parent, l.beginDrain)
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		stop()
		return
	}
	l.stopParents = append(l.stopParents, stop)
	l.mu.Unlock()
	if parent.Err() != nil {
		l.beginDrain()
	}
}
