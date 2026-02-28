package worker

import (
	"strings"
	"time"

	"dalek/internal/contracts"
	"dalek/internal/infra"
)

const defaultWorkerProcessStopTimeout = 5 * time.Second

func workerRuntimeHandle(w contracts.Worker) infra.WorkerProcessHandle {
	h := infra.WorkerProcessHandle{
		LogPath: strings.TrimSpace(w.LogPath),
	}
	if w.StartedAt != nil {
		h.StartedAt = *w.StartedAt
	}
	return h
}

func hasWorkerRuntimeHandle(w contracts.Worker) bool {
	return strings.TrimSpace(w.LogPath) != ""
}
