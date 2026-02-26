package daemon

import (
	"context"
	"fmt"
	"strings"
	"time"
)

func (h *ExecutionHost) executeDispatch(handle *executionRunHandle) {
	if handle == nil {
		return
	}
	defer h.wg.Done()
	defer h.finalizeHandle(handle)
	defer h.notifyRunSettled(strings.TrimSpace(handle.project))

	if !h.acquireSlot(handle.ctx) {
		if handle.ctx.Err() != nil {
			h.logger.Info("execution host dispatch canceled before start",
				"request_id", strings.TrimSpace(handle.requestID),
			)
		}
		return
	}
	defer h.releaseSlot()

	project, err := h.resolver.OpenProject(handle.project)
	if err != nil {
		h.logger.Warn("execution host open project failed",
			"project", handle.project,
			"run_id", handle.runID,
			"error", err,
		)
		return
	}
	err = project.RunDispatchJob(handle.ctx, handle.jobID, DispatchRunOptions{
		RunnerID:    strings.TrimSpace(handle.runnerID),
		EntryPrompt: strings.TrimSpace(handle.entryPrompt),
	})
	if err != nil {
		if handle.ctx.Err() != nil {
			h.logger.Info("execution host dispatch canceled",
				"run_id", handle.runID,
				"project", handle.project,
			)
		} else {
			h.logger.Warn("execution host dispatch failed",
				"run_id", handle.runID,
				"project", handle.project,
				"error", err,
			)
		}
		return
	}
	h.logger.Info("execution host dispatch completed",
		"run_id", handle.runID,
		"project", handle.project,
	)
}

func (h *ExecutionHost) executeWorkerRun(handle *executionRunHandle) {
	if handle == nil {
		return
	}
	defer h.wg.Done()
	defer h.finalizeHandle(handle)
	defer h.notifyRunSettled(strings.TrimSpace(handle.project))

	if !h.acquireSlot(handle.ctx) {
		if handle.ctx.Err() != nil {
			h.logger.Info("execution host worker run canceled before start",
				"request_id", strings.TrimSpace(handle.requestID),
			)
		}
		return
	}
	defer h.releaseSlot()

	project, err := h.resolver.OpenProject(handle.project)
	if err != nil {
		h.logger.Warn("execution host worker run open project failed",
			"project", handle.project,
			"request_id", handle.requestID,
			"error", err,
		)
		return
	}

	baselineRunID := uint(0)
	if latest, lerr := project.FindLatestWorkerRun(handle.ctx, handle.ticketID, 0); lerr == nil && latest != nil {
		baselineRunID = latest.RunID
	}

	resCh := make(chan WorkerRunResult, 1)
	errCh := make(chan error, 1)
	go func() {
		res, runErr := project.DirectDispatchWorker(handle.ctx, handle.ticketID, WorkerRunOptions{EntryPrompt: strings.TrimSpace(handle.entryPrompt)})
		if runErr != nil {
			errCh <- runErr
			return
		}
		resCh <- res
	}()

	foundRun := false
	if status, serr := h.probeWorkerRunID(handle, project, baselineRunID); serr == nil && status != nil {
		h.attachHandleRun(handle, status.RunID, status.WorkerID)
		foundRun = true
	}

	select {
	case res := <-resCh:
		if handle.workerID == 0 {
			h.mu.Lock()
			handle.workerID = res.WorkerID
			h.mu.Unlock()
		}
		if !foundRun {
			if status, serr := project.FindLatestWorkerRun(context.Background(), handle.ticketID, baselineRunID); serr == nil && status != nil {
				h.attachHandleRun(handle, status.RunID, status.WorkerID)
			}
		}
		h.logger.Info("execution host worker run completed",
			"run_id", handle.runID,
			"project", handle.project,
			"request_id", handle.requestID,
		)
	case runErr := <-errCh:
		if !foundRun {
			if status, serr := project.FindLatestWorkerRun(context.Background(), handle.ticketID, baselineRunID); serr == nil && status != nil {
				h.attachHandleRun(handle, status.RunID, status.WorkerID)
			}
		}
		if handle.ctx.Err() != nil {
			h.logger.Info("execution host worker run canceled",
				"request_id", handle.requestID,
				"project", handle.project,
			)
			return
		}
		h.logger.Warn("execution host worker run failed",
			"request_id", handle.requestID,
			"project", handle.project,
			"error", runErr,
		)
	}
}

func (h *ExecutionHost) executeSubagentRun(handle *executionRunHandle) {
	if handle == nil {
		return
	}
	defer h.wg.Done()
	defer h.finalizeHandle(handle)
	defer h.notifyRunSettled(strings.TrimSpace(handle.project))

	if !h.acquireSlot(handle.ctx) {
		if handle.ctx.Err() != nil {
			h.logger.Info("execution host subagent canceled before start",
				"request_id", strings.TrimSpace(handle.requestID),
			)
		}
		return
	}
	defer h.releaseSlot()

	project, err := h.resolver.OpenProject(handle.project)
	if err != nil {
		h.logger.Warn("execution host subagent open project failed",
			"project", handle.project,
			"run_id", handle.runID,
			"error", err,
		)
		return
	}
	err = project.RunSubagentJob(handle.ctx, handle.runID, SubagentRunOptions{
		RunnerID: strings.TrimSpace(handle.runnerID),
	})
	if err != nil {
		if handle.ctx.Err() != nil {
			h.logger.Info("execution host subagent canceled",
				"run_id", handle.runID,
				"project", handle.project,
			)
		} else {
			h.logger.Warn("execution host subagent failed",
				"run_id", handle.runID,
				"project", handle.project,
				"request_id", handle.requestID,
				"error", err,
			)
		}
		return
	}
	h.logger.Info("execution host subagent completed",
		"run_id", handle.runID,
		"project", handle.project,
		"request_id", handle.requestID,
	)
}

func (h *ExecutionHost) probeWorkerRunID(handle *executionRunHandle, project ExecutionHostProject, baselineRunID uint) (*RunStatus, error) {
	if handle == nil || project == nil {
		return nil, fmt.Errorf("probe worker run 参数无效")
	}
	deadline := time.Now().Add(workerRunIDProbeTimeout)
	for {
		if time.Now().After(deadline) {
			return nil, nil
		}
		if handle.ctx.Err() != nil {
			return nil, handle.ctx.Err()
		}
		status, err := project.FindLatestWorkerRun(handle.ctx, handle.ticketID, baselineRunID)
		if err == nil && status != nil {
			return status, nil
		}
		time.Sleep(80 * time.Millisecond)
	}
}

func (h *ExecutionHost) notifyRunSettled(project string) {
	if h == nil || h.onRunSettled == nil {
		return
	}
	project = strings.TrimSpace(project)
	if project == "" {
		return
	}
	go h.onRunSettled(project)
}

func (h *ExecutionHost) notifyNoteAdded(project string) {
	if h == nil || h.onNoteAdded == nil {
		return
	}
	project = strings.TrimSpace(project)
	if project == "" {
		return
	}
	go h.onNoteAdded(project)
}

func (h *ExecutionHost) acquireSlot(ctx context.Context) bool {
	if h == nil {
		return false
	}
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case h.sem <- struct{}{}:
		return true
	case <-ctx.Done():
		return false
	}
}

func (h *ExecutionHost) releaseSlot() {
	if h == nil {
		return
	}
	select {
	case <-h.sem:
	default:
	}
}
