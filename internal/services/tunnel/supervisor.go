package tunnel

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

const (
	MinBackoff             = 2 * time.Second
	MaxBackoff             = 60 * time.Second
	StableThreshold        = 30 * time.Second
	MaxConsecutiveFailures = 5
)

type ProcessHandle interface {
	Done() <-chan error
	Stop(timeout time.Duration) error
}

type StartFunc func(RuntimeConfig) (ProcessHandle, error)
type WaitFunc func(context.Context, time.Duration) bool
type NowFunc func() time.Time

type Supervisor struct {
	RuntimeConfig RuntimeConfig
	Logger        *slog.Logger

	StartFn                StartFunc
	WaitFn                 WaitFunc
	NowFn                  NowFunc
	MaxConsecutiveFailures int

	mu                  sync.Mutex
	current             ProcessHandle
	stopped             bool
	consecutiveFailures int
	circuitOpen         bool
}

func (s *Supervisor) Run(ctx context.Context) {
	if s == nil {
		return
	}
	startFn := s.StartFn
	if startFn == nil {
		startFn = func(cfg RuntimeConfig) (ProcessHandle, error) {
			return StartCloudflaredProcess(cfg)
		}
	}
	waitFn := s.WaitFn
	if waitFn == nil {
		waitFn = WaitBackoff
	}
	nowFn := s.NowFn
	if nowFn == nil {
		nowFn = time.Now
	}

	backoff := MinBackoff
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		proc, err := startFn(s.RuntimeConfig)
		if err != nil {
			tripped, failures, maxFailures := s.recordFailure()
			if tripped {
				s.logf("cloudflared 熔断: 连续失败 %d 次，停止重试。最后错误: %v", failures, err)
				return
			}
			s.logf("cloudflared 启动失败: err=%v failures=%d/%d retry_in=%s", err, failures, maxFailures, backoff)
			if !waitFn(ctx, backoff) {
				return
			}
			backoff = min(backoff*2, MaxBackoff)
			continue
		}
		if proc == nil {
			return
		}

		s.mu.Lock()
		if s.stopped {
			s.mu.Unlock()
			_ = proc.Stop(DefaultStopTimeout)
			return
		}
		s.current = proc
		s.mu.Unlock()

		startedAt := nowFn()
		s.logf("cloudflare tunnel 已连接: https://%s", s.RuntimeConfig.Hostname)
		select {
		case <-ctx.Done():
			return
		case err := <-proc.Done():
			s.mu.Lock()
			if s.current == proc {
				s.current = nil
			}
			s.mu.Unlock()
			uptime := nowFn().Sub(startedAt)
			if uptime >= StableThreshold {
				s.resetFailures()
				backoff = MinBackoff
				s.logf("cloudflared 进程退出: uptime=%s err=%v stable=true retry_in=%s", uptime.Round(time.Second), err, backoff)
			} else {
				tripped, failures, maxFailures := s.recordFailure()
				if tripped {
					s.logf("cloudflared 熔断: 连续失败 %d 次，停止重试。最后错误: %v", failures, err)
					return
				}
				s.logf("cloudflared 进程退出: uptime=%s err=%v failures=%d/%d retry_in=%s", uptime.Round(time.Second), err, failures, maxFailures, backoff)
			}
		}

		if !waitFn(ctx, backoff) {
			return
		}
		backoff = min(backoff*2, MaxBackoff)
	}
}

func (s *Supervisor) Stop(timeout time.Duration) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.stopped = true
	proc := s.current
	s.current = nil
	s.mu.Unlock()
	if proc != nil {
		_ = proc.Stop(timeout)
	}
}

func (s *Supervisor) CircuitOpen() bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.circuitOpen
}

func (s *Supervisor) ConsecutiveFailures() int {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.consecutiveFailures
}

func (s *Supervisor) recordFailure() (tripped bool, failures int, maxFailures int) {
	if s == nil {
		return false, 0, MaxConsecutiveFailures
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	maxFailures = s.MaxConsecutiveFailures
	if maxFailures <= 0 {
		maxFailures = MaxConsecutiveFailures
	}
	s.consecutiveFailures++
	failures = s.consecutiveFailures
	if failures >= maxFailures {
		s.circuitOpen = true
		tripped = true
	}
	return tripped, failures, maxFailures
}

func (s *Supervisor) resetFailures() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.consecutiveFailures = 0
}

func WaitBackoff(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		d = MinBackoff
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func (s *Supervisor) logf(format string, args ...any) {
	if s == nil || s.Logger == nil {
		return
	}
	s.Logger.Info(fmt.Sprintf(format, args...))
}
