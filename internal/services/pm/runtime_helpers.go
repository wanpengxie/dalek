package pm

import (
	"context"
	"strings"

	"dalek/internal/contracts"
	"dalek/internal/infra"
)

func hasWorkerRuntimeHandle(w contracts.Worker) bool {
	return w.ProcessPID > 0
}

func workerRuntimeHandle(w contracts.Worker) infra.WorkerProcessHandle {
	h := infra.WorkerProcessHandle{
		PID:     w.ProcessPID,
		LogPath: strings.TrimSpace(w.LogPath),
	}
	if w.StartedAt != nil {
		h.StartedAt = *w.StartedAt
	}
	return h
}

func (s *Service) workerDispatchReady(ctx context.Context, w *contracts.Worker) (bool, error) {
	if w == nil {
		return false, nil
	}
	if hasWorkerRuntimeHandle(*w) {
		return true, nil
	}
	return strings.TrimSpace(w.TmuxSession) != "", nil
}

func (s *Service) workerDispatchLive(ctx context.Context, w *contracts.Worker) (bool, error) {
	p, _, err := s.require()
	if err != nil {
		return false, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if w == nil {
		return false, nil
	}
	if !hasWorkerRuntimeHandle(*w) {
		return false, nil
	}
	alive, err := p.WorkerRuntime.IsAlive(ctx, workerRuntimeHandle(*w))
	if err != nil {
		return false, err
	}
	return alive, nil
}
