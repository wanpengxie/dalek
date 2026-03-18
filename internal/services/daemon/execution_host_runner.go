package daemon

import (
	"context"
	"fmt"
	"strings"
	"time"

	pmsvc "dalek/internal/services/pm"
)

func (h *ExecutionHost) executeTicketRun(handle *executionRunHandle) {
	if handle == nil {
		return
	}
	var project ExecutionHostProject
	defer h.wg.Done()
	defer h.finalizeHandle(handle)
	defer func() {
		h.maybeTerminateTicketRun(handle, project)
	}()
	defer h.notifyRunSettled(handle.project)

	runLabel := "worker run"
	logLabel := strings.ReplaceAll(runLabel, " ", "_")
	sink := executionTicketLoopControlSink{host: h, handle: handle}

	if !h.acquireSlot(handle.ctx) {
		if handle.ctx.Err() != nil {
			sink.LoopCancelRequested()
			h.logger.Info("execution host ticket run canceled before start",
				"request_id", handle.requestID,
				"run_kind", logLabel,
			)
		}
		return
	}
	defer h.releaseSlot()

	var err error
	project, err = h.resolver.OpenProject(handle.project)
	if err != nil {
		sink.LoopErrored(err)
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
	observedRunID := baselineRunID

	attachLatestRun := func(probeCtx context.Context) {
		status, serr := project.FindLatestWorkerRun(probeCtx, handle.ticketID, observedRunID)
		if serr != nil || status == nil {
			return
		}
		if status.RunID > observedRunID {
			observedRunID = status.RunID
		}
		h.attachHandleRun(handle, status.RunID, status.WorkerID)
	}

	resCh := make(chan WorkerRunResult, 1)
	errCh := make(chan error, 1)
	go func() {
		loopCtx := pmsvc.WithWorkerLoopControlSink(handle.ctx, sink)
		res, runErr := project.RunTicketWorker(loopCtx, handle.ticketID, WorkerRunOptions{
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

	probeTicker := time.NewTicker(100 * time.Millisecond)
	defer probeTicker.Stop()

	for {
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
				attachLatestRun(context.Background())
			}
			h.setHandlePhase(handle, pmsvc.WorkerLoopPhaseClosing)
			h.logger.Info("execution host ticket run completed",
				"run_id", handle.runID,
				"project", handle.project,
				"request_id", handle.requestID,
				"run_kind", logLabel,
			)
			return
		case runErr := <-errCh:
			if handle.runID == 0 {
				attachLatestRun(context.Background())
			}
			if handle.ctx.Err() != nil {
				sink.LoopCancelRequested()
				h.logger.Info("execution host ticket run canceled",
					"request_id", handle.requestID,
					"project", handle.project,
					"run_kind", logLabel,
				)
				return
			}
			sink.LoopErrored(runErr)
			h.logger.Warn("execution host ticket run failed",
				"request_id", handle.requestID,
				"project", handle.project,
				"run_kind", logLabel,
				"error", runErr,
			)
			return
		case <-probeTicker.C:
			attachLatestRun(context.Background())
		}
	}
}

func (h *ExecutionHost) maybeTerminateTicketRun(handle *executionRunHandle, project ExecutionHostProject) {
	if h == nil || handle == nil || handle.kind != runKindWorker {
		return
	}
	queryBase := context.Background()
	if handle.ctx != nil {
		queryBase = context.WithoutCancel(handle.ctx)
	}
	ctx, cancel := context.WithTimeout(queryBase, 5*time.Second)
	defer cancel()

	h.mu.RLock()
	runID := handle.runID
	projectName := handle.project
	ticketID := handle.ticketID
	requestID := handle.requestID
	phase := strings.TrimSpace(handle.phase)
	h.mu.RUnlock()
	if runID == 0 {
		return
	}
	var err error
	if project == nil {
		if h.resolver == nil {
			return
		}
		project, err = h.resolver.OpenProject(projectName)
		if err != nil {
			h.logger.Warn("execution host ticket run terminal probe open project failed",
				"run_id", runID,
				"project", projectName,
				"request_id", requestID,
				"error", err,
			)
			return
		}
	}

	status, err := project.GetTaskStatus(ctx, runID)
	if err != nil {
		h.logger.Warn("execution host ticket run terminal probe failed",
			"run_id", runID,
			"project", projectName,
			"request_id", requestID,
			"error", err,
		)
		return
	}
	if executionHostRunHasTerminalFact(status) {
		return
	}
	terminator, ok := project.(executionHostTaskRunTerminator)
	if !ok || terminator == nil {
		h.logger.Warn("execution host ticket run missing terminalizer",
			"run_id", runID,
			"project", projectName,
			"request_id", requestID,
		)
		return
	}
	reason := fmt.Sprintf("execution host terminated ticket loop before terminal closure: project=%s ticket=%d run=%d request_id=%s phase=%s",
		projectName, ticketID, runID, requestID, phase)
	result, err := terminator.TerminateTaskRun(ctx, runID, reason)
	if err != nil {
		h.logger.Warn("execution host ticket run terminalize failed",
			"run_id", runID,
			"project", projectName,
			"request_id", requestID,
			"error", err,
		)
		return
	}
	if !result.Found || !result.Terminated {
		return
	}
	h.logger.Info("execution host ticket run terminalized",
		"run_id", runID,
		"project", projectName,
		"ticket_id", ticketID,
		"request_id", requestID,
		"event_type", strings.TrimSpace(result.EventType),
	)
}

func executionHostRunHasTerminalFact(status *RunStatus) bool {
	if status == nil {
		return false
	}
	switch strings.TrimSpace(strings.ToLower(status.OrchestrationState)) {
	case "succeeded", "failed":
		return true
	case "canceled":
		return strings.TrimSpace(strings.ToLower(status.ErrorCode)) != "agent_canceled"
	}
	switch strings.TrimSpace(strings.ToLower(status.SemanticNextAction)) {
	case "done", "wait_user":
		return true
	default:
		return false
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
