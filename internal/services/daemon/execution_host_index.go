package daemon

import (
	"context"
	"fmt"
	"strings"
)

func (h *ExecutionHost) lookupTicketRunHandle(kind executionRunKind, project string, ticketID uint, requestID string) (*executionRunHandle, bool) {
	h.mu.RLock()
	handle := h.requests[requestID]
	h.mu.RUnlock()
	if handle == nil {
		return nil, false
	}
	if handle.kind != kind {
		return nil, false
	}
	if handle.project != project || handle.ticketID != ticketID {
		return nil, false
	}
	return handle, true
}

func (h *ExecutionHost) lookupDispatchRequest(project string, ticketID uint, requestID string) (DispatchSubmitReceipt, bool) {
	receipt, ok := h.lookupWorkerRequest(project, ticketID, requestID)
	if !ok {
		return DispatchSubmitReceipt{}, false
	}
	return dispatchReceiptFromWorkerReceipt(receipt), true
}

func (h *ExecutionHost) dispatchReceiptFromHandle(handle *executionRunHandle) DispatchSubmitReceipt {
	return dispatchReceiptFromWorkerReceipt(h.workerReceiptFromHandle(handle))
}

func dispatchReceiptFromWorkerReceipt(receipt WorkerRunSubmitReceipt) DispatchSubmitReceipt {
	return DispatchSubmitReceipt{
		Accepted:  receipt.Accepted,
		Project:   receipt.Project,
		RequestID: receipt.RequestID,
		TaskRunID: receipt.TaskRunID,
		TicketID:  receipt.TicketID,
		WorkerID:  receipt.WorkerID,
	}
}

func (h *ExecutionHost) lookupWorkerRequest(project string, ticketID uint, requestID string) (WorkerRunSubmitReceipt, bool) {
	handle, ok := h.lookupTicketRunHandle(runKindWorker, project, ticketID, requestID)
	if !ok {
		return WorkerRunSubmitReceipt{}, false
	}
	return h.workerReceiptFromHandle(handle), true
}

func (h *ExecutionHost) lookupSubagentRequest(project string, requestID string) (SubagentSubmitReceipt, bool) {
	h.mu.RLock()
	handle := h.requests[requestID]
	h.mu.RUnlock()
	if handle == nil {
		return SubagentSubmitReceipt{}, false
	}
	if handle.kind != runKindSubagent {
		return SubagentSubmitReceipt{}, false
	}
	if handle.project != project {
		return SubagentSubmitReceipt{}, false
	}
	return h.subagentReceiptFromHandle(handle), true
}

func (h *ExecutionHost) lookupPlannerRequest(project string, requestID string) (PlannerSubmitReceipt, bool) {
	h.mu.RLock()
	handle := h.requests[requestID]
	h.mu.RUnlock()
	if handle == nil {
		return PlannerSubmitReceipt{}, false
	}
	if handle.kind != runKindPlanner {
		return PlannerSubmitReceipt{}, false
	}
	if handle.project != project {
		return PlannerSubmitReceipt{}, false
	}
	return h.plannerReceiptFromHandle(handle), true
}

func (h *ExecutionHost) workerReceiptFromHandle(handle *executionRunHandle) WorkerRunSubmitReceipt {
	if handle == nil {
		return WorkerRunSubmitReceipt{}
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	return WorkerRunSubmitReceipt{
		Accepted:  true,
		Project:   handle.project,
		RequestID: handle.requestID,
		TaskRunID: handle.runID,
		TicketID:  handle.ticketID,
		WorkerID:  handle.workerID,
	}
}

func (h *ExecutionHost) subagentReceiptFromHandle(handle *executionRunHandle) SubagentSubmitReceipt {
	if handle == nil {
		return SubagentSubmitReceipt{}
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	return SubagentSubmitReceipt{
		Accepted:   true,
		Project:    handle.project,
		TaskRunID:  handle.runID,
		RequestID:  handle.requestID,
		Provider:   handle.provider,
		Model:      handle.model,
		RuntimeDir: "",
	}
}

func (h *ExecutionHost) plannerReceiptFromHandle(handle *executionRunHandle) PlannerSubmitReceipt {
	if handle == nil {
		return PlannerSubmitReceipt{}
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	return PlannerSubmitReceipt{
		Accepted:  true,
		Project:   handle.project,
		TaskRunID: handle.runID,
		RequestID: handle.requestID,
	}
}

func (h *ExecutionHost) attachHandleRun(handle *executionRunHandle, runID, workerID uint) {
	if handle == nil || runID == 0 {
		return
	}
	h.mu.Lock()
	handle.runID = runID
	if workerID != 0 {
		handle.workerID = workerID
	}
	h.runs[runID] = handle
	h.addRunProjectIndexLocked(runID, handle.project)
	h.mu.Unlock()
	h.markHandleReady(handle)
}

func (h *ExecutionHost) addRunProjectIndex(runID uint, project string) {
	if h == nil || runID == 0 {
		return
	}
	project = strings.TrimSpace(project)
	if project == "" {
		return
	}
	h.mu.Lock()
	h.addRunProjectIndexLocked(runID, project)
	h.mu.Unlock()
}

func (h *ExecutionHost) WarmupRunProjectIndex(project string, runIDs []uint) int {
	if h == nil {
		return 0
	}
	project = strings.TrimSpace(project)
	if project == "" || len(runIDs) == 0 {
		return 0
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.runProjectIndex == nil {
		h.runProjectIndex = map[uint][]string{}
	}
	indexed := 0
	for _, runID := range runIDs {
		if runID == 0 {
			continue
		}
		before := len(h.runProjectIndex[runID])
		h.addRunProjectIndexLocked(runID, project)
		after := len(h.runProjectIndex[runID])
		if after > before {
			indexed++
		}
	}
	if indexed > 0 {
		h.logger.Info("execution host run index warmup",
			"project", project,
			"indexed_runs", indexed,
		)
	}
	return indexed
}

func (h *ExecutionHost) addRunProjectIndexLocked(runID uint, project string) {
	if runID == 0 {
		return
	}
	project = strings.TrimSpace(project)
	if project == "" {
		return
	}
	if h.runProjectIndex == nil {
		h.runProjectIndex = map[uint][]string{}
	}
	projects := h.runProjectIndex[runID]
	for _, existing := range projects {
		if existing == project {
			return
		}
	}
	h.runProjectIndex[runID] = append(projects, project)
}

func (h *ExecutionHost) lookupRunProject(runID uint) []string {
	if h == nil || runID == 0 {
		return nil
	}
	h.mu.RLock()
	projects := h.runProjectIndex[runID]
	if len(projects) == 0 {
		h.mu.RUnlock()
		return nil
	}
	copied := make([]string, len(projects))
	copy(copied, projects)
	h.mu.RUnlock()
	return copied
}

func (h *ExecutionHost) findRunStatusByIndex(ctx context.Context, runID uint) (*RunStatus, string, error) {
	projects := h.lookupRunProject(runID)
	if len(projects) == 0 {
		return nil, "", nil
	}
	return h.findRunStatusInProjects(ctx, runID, projects, "index")
}

func (h *ExecutionHost) markHandleReady(handle *executionRunHandle) {
	if handle == nil {
		return
	}
	handle.readyOnce.Do(func() {
		if handle.ready != nil {
			close(handle.ready)
		}
	})
}

func (h *ExecutionHost) finalizeHandle(handle *executionRunHandle) {
	if handle == nil {
		return
	}
	h.markHandleReady(handle)
	h.mu.Lock()
	if handle.runID != 0 {
		if cur := h.runs[handle.runID]; cur == handle {
			delete(h.runs, handle.runID)
			delete(h.runProjectIndex, handle.runID)
		}
	}
	if handle.requestID != "" {
		if cur := h.requests[handle.requestID]; cur == handle {
			if handle.retainRequest {
				h.requests[handle.requestID] = handle
			} else {
				delete(h.requests, handle.requestID)
			}
		}
	}
	h.mu.Unlock()
	if handle.cancel != nil {
		handle.cancel()
	}
	handle.doneOnce.Do(func() {
		if handle.done != nil {
			close(handle.done)
		}
	})
}

func (h *ExecutionHost) summarizePendingHandles(handles []*executionRunHandle) (int, []string) {
	if len(handles) == 0 {
		return 0, nil
	}
	const maxSummary = 8
	pending := make([]string, 0, len(handles))
	total := 0
	for _, handle := range handles {
		if handle == nil {
			continue
		}
		if handle.done != nil {
			select {
			case <-handle.done:
				continue
			default:
			}
		}
		total++
		runID := handle.runID
		ticketID := handle.ticketID
		workerID := handle.workerID
		entry := fmt.Sprintf("kind=%s project=%s request=%s run=%d ticket=%d worker=%d",
			string(handle.kind),
			handle.project,
			handle.requestID,
			runID,
			ticketID,
			workerID,
		)
		if len(pending) < maxSummary {
			pending = append(pending, entry)
		}
	}
	return total, pending
}

func (h *ExecutionHost) getRunHandle(runID uint) (*executionRunHandle, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	handle := h.runs[runID]
	if handle == nil {
		return nil, false
	}
	return handle, true
}

func (h *ExecutionHost) findRunStatusByScan(ctx context.Context, runID uint) (*RunStatus, string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	count := h.scanFallbackCount.Add(1)
	h.logger.Info("execution host run status fallback scan", "run_id", runID, "count", count)
	projects, err := h.resolver.ListProjects()
	if err != nil {
		return nil, "", err
	}
	status, projectName, err := h.findRunStatusInProjects(ctx, runID, projects, "scan")
	if err != nil {
		return nil, "", err
	}
	if status != nil && projectName != "" {
		h.addRunProjectIndex(runID, projectName)
	}
	return status, projectName, nil
}

func (h *ExecutionHost) findRunStatusInProjects(ctx context.Context, runID uint, projects []string, source string) (*RunStatus, string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	for _, name := range projects {
		projectName := strings.TrimSpace(name)
		if projectName == "" {
			continue
		}
		project, err := h.resolver.OpenProject(projectName)
		if err != nil {
			h.logger.Warn("execution host open project for lookup failed",
				"source", source,
				"project", projectName,
				"run_id", runID,
				"error", err,
			)
			continue
		}
		status, err := project.GetTaskStatus(ctx, runID)
		if err != nil {
			h.logger.Warn("execution host status lookup failed",
				"source", source,
				"project", projectName,
				"run_id", runID,
				"error", err,
			)
			continue
		}
		if status == nil {
			continue
		}
		if status.Project == "" {
			status.Project = projectName
		}
		return status, projectName, nil
	}
	return nil, "", nil
}
