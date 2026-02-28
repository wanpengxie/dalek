package pm

import (
	"context"
	"dalek/internal/contracts"
	"errors"
	"fmt"
	"strings"
	"time"
)

type workerReadyTimeoutError struct {
	TicketID   uint
	WorkerID   uint
	LastStatus contracts.WorkerStatus
	Waited     time.Duration
}

func (e *workerReadyTimeoutError) Error() string {
	if e == nil {
		return "等待 worker 就绪超时"
	}
	status := strings.TrimSpace(string(e.LastStatus))
	if status == "" {
		status = "unknown"
	}
	waited := e.Waited
	if waited <= 0 {
		waited = defaultWorkerReadyTimeout
	}
	return fmt.Sprintf("等待 worker 就绪超时（t%d w%d status=%s waited=%s）", e.TicketID, e.WorkerID, status, waited.Round(time.Millisecond))
}

func isWorkerReadyTimeout(err error) bool {
	var target *workerReadyTimeoutError
	return errors.As(err, &target)
}

func (s *Service) dispatchWorkerReadyTimeout() time.Duration {
	if s == nil || s.workerReadyTimeout <= 0 {
		return defaultWorkerReadyTimeout
	}
	return s.workerReadyTimeout
}

func (s *Service) dispatchWorkerReadyPollInterval() time.Duration {
	if s == nil || s.workerReadyPollInterval <= 0 {
		return defaultWorkerReadyPollInterval
	}
	return s.workerReadyPollInterval
}

func (s *Service) workerNotRunningError(w *contracts.Worker) error {
	if w == nil {
		return fmt.Errorf("该 ticket 的最新 worker 不在 running（status=unknown），请重新启动")
	}
	return fmt.Errorf("该 ticket 的最新 worker 不在 running（w%d status=%s），请重新启动", w.ID, w.Status)
}

func (s *Service) workerMissingSessionError() error {
	return fmt.Errorf("该 ticket 尚未启动（没有 worker/session），或缺少可用 runtime，请先按 s 或运行 start")
}

func (s *Service) waitWorkerReadyForDispatch(ctx context.Context, ticketID uint, initial *contracts.Worker) (*contracts.Worker, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if initial == nil {
		return nil, s.workerMissingSessionError()
	}
	if initial.Status == contracts.WorkerRunning {
		ready, err := s.workerDispatchReady(ctx, initial)
		if err != nil {
			return nil, err
		}
		if !ready {
			return nil, s.workerMissingSessionError()
		}
		return initial, nil
	}
	if initial.Status != contracts.WorkerCreating {
		return nil, s.workerNotRunningError(initial)
	}

	timeout := s.dispatchWorkerReadyTimeout()
	poll := s.dispatchWorkerReadyPollInterval()
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	startAt := time.Now()
	current := initial
	ticker := time.NewTicker(poll)
	defer ticker.Stop()

	for {
		select {
		case <-waitCtx.Done():
			if errors.Is(waitCtx.Err(), context.DeadlineExceeded) {
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
				return nil, &workerReadyTimeoutError{
					TicketID:   ticketID,
					WorkerID:   current.ID,
					LastStatus: current.Status,
					Waited:     time.Since(startAt),
				}
			}
			return nil, waitCtx.Err()
		case <-ticker.C:
		}

		w, err := s.worker.LatestWorker(waitCtx, ticketID)
		if err != nil {
			if errors.Is(waitCtx.Err(), context.DeadlineExceeded) {
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
				return nil, &workerReadyTimeoutError{
					TicketID:   ticketID,
					WorkerID:   current.ID,
					LastStatus: current.Status,
					Waited:     time.Since(startAt),
				}
			}
			return nil, err
		}
		if w == nil {
			return nil, s.workerMissingSessionError()
		}
		current = w
		if current.Status == contracts.WorkerRunning {
			ready, err := s.workerDispatchReady(waitCtx, current)
			if err != nil {
				return nil, err
			}
			if !ready {
				return nil, s.workerMissingSessionError()
			}
			return current, nil
		}
		if current.Status != contracts.WorkerCreating {
			return nil, s.workerNotRunningError(current)
		}
	}
}
