package progresstimeout

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// DefaultTimeout is the shared idle timeout window for agent executions.
var DefaultTimeout = 30 * time.Minute

type TimeoutError struct {
	Subject string
	Timeout time.Duration
}

func (e TimeoutError) Error() string {
	subject := strings.TrimSpace(e.Subject)
	if subject == "" {
		subject = "agent run"
	}
	timeout := normalizeTimeout(e.Timeout)
	return fmt.Sprintf("%s timed out after %s without progress", subject, timeout)
}

func (e TimeoutError) Unwrap() error {
	return context.DeadlineExceeded
}

func Is(err error) bool {
	var timeoutErr TimeoutError
	return errors.As(err, &timeoutErr)
}

type Watchdog struct {
	timeout time.Duration
	cancel  context.CancelCauseFunc
	notify  chan struct{}
	done    chan struct{}

	mu       sync.RWMutex
	deadline time.Time
	timedOut atomic.Bool
}

func New(parent context.Context, timeout time.Duration) (context.Context, *Watchdog) {
	parent = ensureContext(parent)
	timeout = normalizeTimeout(timeout)
	runCtx, cancel := context.WithCancelCause(parent)
	now := time.Now()
	w := &Watchdog{
		timeout:  timeout,
		cancel:   cancel,
		notify:   make(chan struct{}, 1),
		done:     make(chan struct{}),
		deadline: now.Add(timeout),
	}
	go w.run(runCtx)
	return runCtx, w
}

func (w *Watchdog) Timeout() time.Duration {
	if w == nil {
		return normalizeTimeout(0)
	}
	return w.timeout
}

func (w *Watchdog) CurrentDeadline() *time.Time {
	if w == nil {
		return nil
	}
	w.mu.RLock()
	deadline := w.deadline
	w.mu.RUnlock()
	if deadline.IsZero() {
		return nil
	}
	out := deadline
	return &out
}

func (w *Watchdog) Touch() *time.Time {
	if w == nil {
		return nil
	}
	deadline := time.Now().Add(w.timeout)
	w.mu.Lock()
	w.deadline = deadline
	w.mu.Unlock()
	select {
	case w.notify <- struct{}{}:
	default:
	}
	out := deadline
	return &out
}

func (w *Watchdog) Cancel() {
	w.CancelCause(context.Canceled)
}

func (w *Watchdog) CancelCause(cause error) {
	if w == nil || w.cancel == nil {
		return
	}
	w.cancel(cause)
}

func (w *Watchdog) Stop() {
	if w == nil {
		return
	}
	w.Cancel()
	if w.done != nil {
		<-w.done
	}
}

func (w *Watchdog) TimedOut() bool {
	return w != nil && w.timedOut.Load()
}

func (w *Watchdog) TimeoutError(subject string) error {
	if w == nil {
		return TimeoutError{Subject: subject, Timeout: normalizeTimeout(0)}
	}
	return TimeoutError{Subject: subject, Timeout: w.timeout}
}

func (w *Watchdog) run(ctx context.Context) {
	defer close(w.done)
	timer := time.NewTimer(w.timeout)
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			w.timedOut.Store(true)
			w.Cancel()
			return
		case <-w.notify:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(w.timeout)
		}
	}
}

func ensureContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

func normalizeTimeout(timeout time.Duration) time.Duration {
	if timeout > 0 {
		return timeout
	}
	return DefaultTimeout
}
