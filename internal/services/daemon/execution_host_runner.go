package daemon

import (
	"context"
	"strings"
)

func (h *ExecutionHost) executeTicketRun(handle *executionRunHandle) {
	if handle == nil {
		return
	}
	defer h.wg.Done()
	defer h.finalizeHandle(handle)
	defer h.notifyRunSettled(handle.project)

	runLabel := "worker run"
	logLabel := strings.ReplaceAll(runLabel, " ", "_")

	if !h.acquireSlot(handle.ctx) {
		if handle.ctx.Err() != nil {
			h.logger.Info("execution host ticket run canceled before start",
				"request_id", handle.requestID,
				"run_kind", logLabel,
			)
		}
		return
	}
	defer h.releaseSlot()

	project, err := h.resolver.OpenProject(handle.project)
	if err != nil {
		h.logger.Warn("execution host ticket run open project failed",
			"project", handle.project,
			"request_id", handle.requestID,
			"run_kind", logLabel,
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
		res, runErr := project.DirectDispatchWorker(handle.ctx, handle.ticketID, WorkerRunOptions{
			EntryPrompt: handle.entryPrompt,
			AutoStart:   handle.autoStart,
			BaseBranch:  handle.baseBranch,
		})
		if runErr != nil {
			errCh <- runErr
			return
		}
		resCh <- res
	}()

	select {
	case res := <-resCh:
		if res.RunID != 0 {
			h.attachHandleRun(handle, res.RunID, res.WorkerID)
		} else if res.WorkerID != 0 {
			h.mu.Lock()
			if handle.workerID == 0 {
				handle.workerID = res.WorkerID
			}
			h.mu.Unlock()
		}
		if handle.runID == 0 {
			if status, serr := project.FindLatestWorkerRun(context.Background(), handle.ticketID, baselineRunID); serr == nil && status != nil {
				h.attachHandleRun(handle, status.RunID, status.WorkerID)
			}
		}
		h.logger.Info("execution host ticket run completed",
			"run_id", handle.runID,
			"project", handle.project,
			"request_id", handle.requestID,
			"run_kind", logLabel,
		)
	case runErr := <-errCh:
		if handle.runID == 0 {
			if status, serr := project.FindLatestWorkerRun(context.Background(), handle.ticketID, baselineRunID); serr == nil && status != nil {
				h.attachHandleRun(handle, status.RunID, status.WorkerID)
			}
		}
		if handle.ctx.Err() != nil {
			h.logger.Info("execution host ticket run canceled",
				"request_id", handle.requestID,
				"project", handle.project,
				"run_kind", logLabel,
			)
			return
		}
		h.logger.Warn("execution host ticket run failed",
			"request_id", handle.requestID,
			"project", handle.project,
			"run_kind", logLabel,
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
	defer h.notifyRunSettled(handle.project)

	if !h.acquireSlot(handle.ctx) {
		if handle.ctx.Err() != nil {
			h.logger.Info("execution host subagent canceled before start",
				"request_id", handle.requestID,
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
		RunnerID: handle.runnerID,
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

func (h *ExecutionHost) executePlannerRun(handle *executionRunHandle) {
	if handle == nil {
		return
	}
	defer h.wg.Done()
	defer h.finalizeHandle(handle)
	defer h.notifyRunSettled(handle.project)

	if !h.acquireSlot(handle.ctx) {
		if handle.ctx.Err() != nil {
			h.logger.Info("execution host planner canceled before start",
				"request_id", handle.requestID,
			)
		}
		return
	}
	defer h.releaseSlot()

	project, err := h.resolver.OpenProject(handle.project)
	if err != nil {
		h.logger.Warn("execution host planner open project failed",
			"project", handle.project,
			"run_id", handle.runID,
			"error", err,
		)
		return
	}
	err = project.RunPlannerJob(handle.ctx, handle.runID, PlannerRunOptions{
		RunnerID: handle.runnerID,
		Prompt:   handle.entryPrompt,
	})
	if err != nil {
		if handle.ctx.Err() != nil {
			h.logger.Info("execution host planner canceled",
				"run_id", handle.runID,
				"project", handle.project,
			)
		} else {
			h.logger.Warn("execution host planner failed",
				"run_id", handle.runID,
				"project", handle.project,
				"request_id", handle.requestID,
				"error", err,
			)
		}
		return
	}
	h.logger.Info("execution host planner completed",
		"run_id", handle.runID,
		"project", handle.project,
		"request_id", handle.requestID,
	)
}

func (h *ExecutionHost) notifyRunSettled(project string) {
	if h == nil || h.onRunSettled == nil {
		return
	}
	if project == "" {
		return
	}
	go h.onRunSettled(project)
}

func (h *ExecutionHost) notifyNoteAdded(project string) {
	if h == nil || h.onNoteAdded == nil {
		return
	}
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
