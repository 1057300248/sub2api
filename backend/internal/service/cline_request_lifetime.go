package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"
)

const clineDrainGrace = 30 * time.Second
const clineRequestCeiling = 30 * time.Minute

// Preserve the existing client-cancellation classification after bounded drain.
// The independent absolute ceiling remains a distinct upstream timeout.
var ErrClineDrainTimeout = fmt.Errorf("cline disconnected response drain deadline exceeded: %w", context.Canceled)
var ErrClineRequestTimeout = errors.New("cline upstream request lifetime exceeded")
var ErrClineCallerDeadline = fmt.Errorf("cline caller deadline exceeded: %w", context.DeadlineExceeded)

// Each request owns its timers and cancellation. Parent cancellation starts a
// grace period, not an unbounded detached read. The overall ceiling also covers
// transports waiting for response headers and lost cancellation notifications.
// This does not extend the downstream client's timeout or lower first-token time.
type clineRequestLifetime struct {
	ctx            context.Context
	cancel         context.CancelCauseFunc
	mu             sync.Mutex
	closed         bool
	draining       bool
	drainTimer     *time.Timer
	stopParents    []func() bool
	parents        []context.Context
	deadlineCancel context.CancelFunc
	grace          time.Duration
}

func newClineRequestLifetime(parent context.Context, grace, ceiling time.Duration, otherParents ...context.Context) *clineRequestLifetime {
	if parent == nil {
		parent = context.Background()
	}
	if grace <= 0 {
		grace = clineDrainGrace
	}
	if ceiling <= 0 {
		ceiling = clineRequestCeiling
	}
	parents := []context.Context{parent}
	for _, other := range otherParents {
		if other != nil {
			parents = append(parents, other)
		}
	}
	deadline, deadlineCause := time.Now().Add(ceiling), ErrClineRequestTimeout
	for _, p := range parents {
		if d, ok := p.Deadline(); ok && !d.After(deadline) {
			deadline, deadlineCause = d, ErrClineCallerDeadline
		}
	}
	// Detach cancellation only: reattach the earliest explicit deadline before
	// constructing the request. It remains active even if a parent is canceled
	// early and its original deadline timer is consequently stopped.
	deadlineCtx, release := context.WithDeadlineCause(context.WithoutCancel(parent), deadline, deadlineCause)
	ctx, cancel := context.WithCancelCause(deadlineCtx)
	lifetime := &clineRequestLifetime{ctx: ctx, cancel: cancel, grace: grace, parents: parents, deadlineCancel: release}
	for _, p := range parents {
		lifetime.watch(p)
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
	stops := l.stopParents
	l.mu.Unlock()
	for _, stop := range stops {
		stop()
	}
	l.cancel(context.Canceled)
	l.deadlineCancel()
}

type clineLifetimeBody struct {
	source    io.ReadCloser
	lifetime  *clineRequestLifetime
	closeOnce sync.Once
	closeErr  error
}

// Completion is supplied by the validating SSE guard, never inferred from usage
// or finish_reason. All accesses happen on the response-reader goroutine.
func (b *clineLifetimeBody) ClineSSEComplete() bool {
	completed, ok := b.source.(interface{ ClineSSEComplete() bool })
	return ok && completed.ClineSSEComplete()
}

func (b *clineLifetimeBody) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if b.ClineSSEComplete() {
		return 0, io.EOF
	}
	if err := context.Cause(b.lifetime.ctx); err != nil {
		return 0, err
	}

	n, err := b.source.Read(p)
	if b.ClineSSEComplete() {
		return n, io.EOF
	}
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
	stop := context.AfterFunc(parent, func() { l.parentStopped(parent) })
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		stop()
		return
	}
	l.stopParents = append(l.stopParents, stop)
	l.mu.Unlock()
	if parent.Err() != nil {
		l.parentStopped(parent)
	}
}

func (l *clineRequestLifetime) parentStopped(parent context.Context) {
	if parent.Err() == context.DeadlineExceeded {
		// The independent deadline context owns this cancellation so Err(), as
		// well as Cause(), stays DeadlineExceeded rather than Canceled.
		return
	}
	cause := context.Cause(parent)
	if cause == context.Canceled {
		l.beginDrain()
	} else if cause != nil {
		// A service-defined abort cause is not evidence of client disconnect.
		l.cancel(cause)
	}
}

// These helpers are deliberately scoped to bodies owned by the Cline send
// boundary; other providers keep their existing streaming behavior.
func clineBodyClientDisconnected(body io.ReadCloser, writeFailed bool) bool {
	bounded, ok := body.(*clineLifetimeBody)
	if !ok {
		return writeFailed
	}
	l := bounded.lifetime
	cause := context.Cause(l.ctx)
	if errors.Is(cause, context.DeadlineExceeded) || errors.Is(cause, ErrClineRequestTimeout) {
		return false
	}
	if writeFailed {
		return true
	}
	l.mu.Lock()
	draining := l.draining
	l.mu.Unlock()
	if draining {
		return true
	}
	for _, parent := range l.parents {
		if parent.Err() == context.Canceled && context.Cause(parent) == context.Canceled {
			l.beginDrain()
			return true
		}
	}
	return errors.Is(cause, ErrClineDrainTimeout)
}

func clineBodyDisconnectResult(body io.ReadCloser, writeFailed bool) bool {
	_, owned := body.(*clineLifetimeBody)
	return owned && clineBodyClientDisconnected(body, writeFailed)
}

func clineBodyReadError(body io.ReadCloser, readErr error) error {
	if bounded, ok := body.(*clineLifetimeBody); ok {
		// A late caller deadline cannot turn an already validated terminal
		// event into a failed response. Preserve genuine parser/read errors.
		if bounded.ClineSSEComplete() && (readErr == nil || errors.Is(readErr, io.EOF)) {
			return readErr
		}
		if cause := context.Cause(bounded.lifetime.ctx); cause != nil {
			return cause
		}
	}
	return readErr
}
