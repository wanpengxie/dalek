package pm

import (
	"context"
	"strings"

	"dalek/internal/contracts"
	"dalek/internal/infra"
)

func hasWorkerRuntimeHandle(w contracts.Worker) bool {
	return strings.TrimSpace(w.LogPath) != ""
}

func workerRuntimeHandle(w contracts.Worker) infra.WorkerProcessHandle {
	h := infra.WorkerProcessHandle{
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
	return hasWorkerRuntimeHandle(*w), nil
}

func (s *Service) workerDispatchLive(ctx context.Context, w *contracts.Worker) (bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if w == nil {
		return false, nil
	}
	if !hasWorkerRuntimeHandle(*w) {
		return false, nil
	}
	if w.Status == contracts.WorkerRunning {
		return true, nil
	}
	rt, err := s.taskRuntime()
	if err != nil {
		return false, err
	}
	run, err := rt.LatestActiveWorkerRun(ctx, w.ID)
	if err != nil {
		return false, err
	}
	return run != nil, nil
}
