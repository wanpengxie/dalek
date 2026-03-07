package daemon

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type ExecutionHost struct {
	resolver     ExecutionHostResolver
	logger       *slog.Logger
	sem          chan struct{}
	onRunSettled func(project string)
	onNoteAdded  func(project string)
	wg           sync.WaitGroup

	mu              sync.RWMutex
	runs            map[uint]*executionRunHandle
	requests        map[string]*executionRunHandle
	runProjectIndex map[uint][]string

	scanFallbackCount atomic.Int64
}

func NewExecutionHost(resolver ExecutionHostResolver, opt ExecutionHostOptions) (*ExecutionHost, error) {
	if resolver == nil {
		return nil, fmt.Errorf("execution host resolver 不能为空")
	}
	maxConcurrent := opt.MaxConcurrent
	if maxConcurrent <= 0 {
		maxConcurrent = defaultExecutionHostConcurrency
	}
	logger := opt.Logger
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &ExecutionHost{
		resolver:        resolver,
		logger:          logger,
		sem:             make(chan struct{}, maxConcurrent),
		runs:            map[uint]*executionRunHandle{},
		requests:        map[string]*executionRunHandle{},
		runProjectIndex: map[uint][]string{},
		onRunSettled:    opt.OnRunSettled,
		onNoteAdded:     opt.OnNoteAdded,
	}, nil
}

func (h *ExecutionHost) Stop(ctx context.Context) error {
	if h == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	h.mu.Lock()
	seen := map[*executionRunHandle]bool{}
	handles := make([]*executionRunHandle, 0, len(h.requests)+len(h.runs))
	for _, handle := range h.requests {
		if handle == nil || seen[handle] {
			continue
		}
		seen[handle] = true
		handles = append(handles, handle)
	}
	for _, handle := range h.runs {
		if handle == nil || seen[handle] {
			continue
		}
		seen[handle] = true
		handles = append(handles, handle)
	}
	h.runs = map[uint]*executionRunHandle{}
	h.requests = map[string]*executionRunHandle{}
	h.runProjectIndex = map[uint][]string{}
	h.mu.Unlock()

	for _, handle := range handles {
		h.markHandleReady(handle)
		if handle.cancel != nil {
			handle.cancel()
		}
	}

	done := make(chan struct{})
	go func() {
		h.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		pendingCount, pendingSummary := h.summarizePendingHandles(handles)
		h.logger.Warn("execution host stop timeout",
			"pending_count", pendingCount,
			"pending_summary", strings.Join(pendingSummary, ", "),
			"error", ctx.Err(),
		)
		return &StopTimeoutError{
			Cause:          ctx.Err(),
			PendingCount:   pendingCount,
			PendingSummary: append([]string(nil), pendingSummary...),
		}
	}
}

func (h *ExecutionHost) SubmitDispatch(ctx context.Context, req DispatchSubmitRequest) (DispatchSubmitReceipt, error) {
	if h == nil || h.resolver == nil {
		return DispatchSubmitReceipt{}, fmt.Errorf("execution host 未初始化")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	projectName := strings.TrimSpace(req.Project)
	if projectName == "" {
		return DispatchSubmitReceipt{}, fmt.Errorf("project 不能为空")
	}
	if req.TicketID == 0 {
		return DispatchSubmitReceipt{}, fmt.Errorf("ticket_id 不能为空")
	}
	requestID := strings.TrimSpace(req.RequestID)
	if requestID != "" {
		if receipt, ok := h.lookupDispatchRequest(projectName, req.TicketID, requestID); ok {
			return receipt, nil
		}
	}
	project, err := h.resolver.OpenProject(projectName)
	if err != nil {
		return DispatchSubmitReceipt{}, err
	}
	submission, err := project.SubmitDispatchTicket(ctx, req.TicketID, DispatchSubmitOptions{
		RequestID: requestID,
		AutoStart: req.AutoStart,
	})
	if err != nil {
		return DispatchSubmitReceipt{}, err
	}
	if submission.TaskRunID == 0 {
		return DispatchSubmitReceipt{}, fmt.Errorf("dispatch submit 未返回 task_run_id")
	}

	runID := submission.TaskRunID
	requestID = submission.RequestID
	if requestID == "" {
		requestID = NewRequestID("dsp")
	}

	h.mu.Lock()
	existing := h.runs[runID]
	if existing == nil {
		runCtx, cancel := context.WithCancel(context.Background())
		handle := &executionRunHandle{
			kind:        runKindDispatch,
			project:     projectName,
			requestID:   requestID,
			runID:       runID,
			jobID:       submission.JobID,
			jobStatus:   submission.JobStatus,
			ticketID:    req.TicketID,
			workerID:    submission.WorkerID,
			runnerID:    "daemon_" + NewRequestID("runner"),
			entryPrompt: req.Prompt,
			ctx:         runCtx,
			cancel:      cancel,
			ready:       make(chan struct{}),
			done:        make(chan struct{}),
		}
		h.runs[runID] = handle
		h.requests[requestID] = handle
		h.addRunProjectIndexLocked(runID, projectName)
		h.wg.Add(1)
		h.mu.Unlock()
		go h.executeDispatch(handle)
	} else {
		if existing.project == "" {
			existing.project = projectName
		}
		if existing.requestID == "" {
			existing.requestID = requestID
		}
		if existing.ticketID == 0 {
			existing.ticketID = req.TicketID
		}
		if existing.workerID == 0 {
			existing.workerID = submission.WorkerID
		}
		if existing.jobID == 0 {
			existing.jobID = submission.JobID
		}
		if existing.jobStatus == "" {
			existing.jobStatus = submission.JobStatus
		}
		if existing.entryPrompt == "" {
			existing.entryPrompt = req.Prompt
		}
		h.requests[requestID] = existing
		h.addRunProjectIndexLocked(runID, projectName)
		h.mu.Unlock()
	}

	return DispatchSubmitReceipt{
		Accepted:  true,
		Project:   projectName,
		RequestID: requestID,
		TaskRunID: submission.TaskRunID,
		JobID:     submission.JobID,
		TicketID:  submission.TicketID,
		WorkerID:  submission.WorkerID,
		JobStatus: submission.JobStatus,
	}, nil
}

func (h *ExecutionHost) SubmitWorkerRun(ctx context.Context, req WorkerRunSubmitRequest) (WorkerRunSubmitReceipt, error) {
	if h == nil || h.resolver == nil {
		return WorkerRunSubmitReceipt{}, fmt.Errorf("execution host 未初始化")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	projectName := strings.TrimSpace(req.Project)
	if projectName == "" {
		return WorkerRunSubmitReceipt{}, fmt.Errorf("project 不能为空")
	}
	if req.TicketID == 0 {
		return WorkerRunSubmitReceipt{}, fmt.Errorf("ticket_id 不能为空")
	}
	requestID := strings.TrimSpace(req.RequestID)
	if requestID == "" {
		requestID = NewRequestID("wrk")
	}

	if receipt, ok := h.lookupWorkerRequest(projectName, req.TicketID, requestID); ok {
		return receipt, nil
	}

	runCtx, cancel := context.WithCancel(context.Background())
	handle := &executionRunHandle{
		kind:        runKindWorker,
		project:     projectName,
		requestID:   requestID,
		ticketID:    req.TicketID,
		entryPrompt: req.Prompt,
		ctx:         runCtx,
		cancel:      cancel,
		ready:       make(chan struct{}),
		done:        make(chan struct{}),
	}

	h.mu.Lock()
	if existing := h.requests[requestID]; existing != nil {
		h.mu.Unlock()
		cancel()
		return h.workerReceiptFromHandle(existing), nil
	}
	h.requests[requestID] = handle
	h.wg.Add(1)
	h.mu.Unlock()

	go h.executeWorkerRun(handle)

	select {
	case <-handle.ready:
	case <-time.After(workerRunReadyTimeout):
	}
	return h.workerReceiptFromHandle(handle), nil
}

func (h *ExecutionHost) SubmitSubagentRun(ctx context.Context, req SubagentSubmitRequest) (SubagentSubmitReceipt, error) {
	if h == nil || h.resolver == nil {
		return SubagentSubmitReceipt{}, fmt.Errorf("execution host 未初始化")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	projectName := strings.TrimSpace(req.Project)
	if projectName == "" {
		return SubagentSubmitReceipt{}, fmt.Errorf("project 不能为空")
	}
	prompt := strings.TrimSpace(req.Prompt)
	if prompt == "" {
		return SubagentSubmitReceipt{}, fmt.Errorf("prompt 不能为空")
	}
	requestID := strings.TrimSpace(req.RequestID)
	if requestID == "" {
		requestID = NewRequestID("sub")
	}
	if receipt, ok := h.lookupSubagentRequest(projectName, requestID); ok {
		return receipt, nil
	}
	project, err := h.resolver.OpenProject(projectName)
	if err != nil {
		return SubagentSubmitReceipt{}, err
	}
	provider := strings.TrimSpace(req.Provider)
	model := strings.TrimSpace(req.Model)
	submission, err := project.SubmitSubagentRun(ctx, SubagentSubmitOptions{
		RequestID: requestID,
		Provider:  provider,
		Model:     model,
		Prompt:    prompt,
	})
	if err != nil {
		return SubagentSubmitReceipt{}, err
	}
	if submission.TaskRunID == 0 {
		return SubagentSubmitReceipt{}, fmt.Errorf("subagent submit 未返回 task_run_id")
	}
	if submission.RequestID != "" {
		requestID = submission.RequestID
	}

	runID := submission.TaskRunID
	h.mu.Lock()
	existing := h.runs[runID]
	if existing == nil {
		runCtx, cancel := context.WithCancel(context.Background())
		handle := &executionRunHandle{
			kind:        runKindSubagent,
			project:     projectName,
			requestID:   requestID,
			runID:       runID,
			entryPrompt: prompt,
			runnerID:    "daemon_" + NewRequestID("runner"),
			provider:    submission.Provider,
			model:       submission.Model,
			ctx:         runCtx,
			cancel:      cancel,
			ready:       make(chan struct{}),
			done:        make(chan struct{}),
		}
		h.runs[runID] = handle
		h.requests[requestID] = handle
		h.addRunProjectIndexLocked(runID, projectName)
		h.wg.Add(1)
		h.mu.Unlock()
		go h.executeSubagentRun(handle)
	} else {
		if existing.project == "" {
			existing.project = projectName
		}
		if existing.requestID == "" {
			existing.requestID = requestID
		}
		if existing.entryPrompt == "" {
			existing.entryPrompt = prompt
		}
		if existing.provider == "" {
			existing.provider = submission.Provider
		}
		if existing.model == "" {
			existing.model = submission.Model
		}
		if existing.runnerID == "" {
			existing.runnerID = "daemon_" + NewRequestID("runner")
		}
		h.requests[requestID] = existing
		h.addRunProjectIndexLocked(runID, projectName)
		h.mu.Unlock()
	}

	return SubagentSubmitReceipt{
		Accepted:   true,
		Project:    projectName,
		TaskRunID:  submission.TaskRunID,
		RequestID:  requestID,
		Provider:   submission.Provider,
		Model:      submission.Model,
		RuntimeDir: submission.RuntimeDir,
	}, nil
}

func (h *ExecutionHost) SubmitPlannerRun(ctx context.Context, req PlannerSubmitRequest) (PlannerSubmitReceipt, error) {
	if h == nil || h.resolver == nil {
		return PlannerSubmitReceipt{}, fmt.Errorf("execution host 未初始化")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	projectName := strings.TrimSpace(req.Project)
	if projectName == "" {
		return PlannerSubmitReceipt{}, fmt.Errorf("project 不能为空")
	}
	if req.TaskRunID == 0 {
		return PlannerSubmitReceipt{}, fmt.Errorf("task_run_id 不能为空")
	}

	requestID := strings.TrimSpace(req.RequestID)
	if requestID != "" {
		if receipt, ok := h.lookupPlannerRequest(projectName, requestID); ok {
			return receipt, nil
		}
	}
	prompt := strings.TrimSpace(req.Prompt)
	runID := req.TaskRunID

	h.mu.Lock()
	existing := h.runs[runID]
	if existing == nil {
		if requestID == "" {
			requestID = NewRequestID("pln")
		}
		runCtx, cancel := context.WithCancel(context.Background())
		handle := &executionRunHandle{
			kind:        runKindPlanner,
			project:     projectName,
			requestID:   requestID,
			runID:       runID,
			entryPrompt: prompt,
			runnerID:    "daemon_" + NewRequestID("runner"),
			ctx:         runCtx,
			cancel:      cancel,
			ready:       make(chan struct{}),
			done:        make(chan struct{}),
		}
		h.runs[runID] = handle
		h.requests[requestID] = handle
		h.addRunProjectIndexLocked(runID, projectName)
		h.wg.Add(1)
		h.mu.Unlock()
		go h.executePlannerRun(handle)
		return h.plannerReceiptFromHandle(handle), nil
	}
	if existing.project == "" {
		existing.project = projectName
	}
	if existing.requestID == "" {
		if requestID == "" {
			requestID = NewRequestID("pln")
		}
		existing.requestID = requestID
	}
	if existing.entryPrompt == "" {
		existing.entryPrompt = prompt
	}
	if existing.runnerID == "" {
		existing.runnerID = "daemon_" + NewRequestID("runner")
	}
	if existing.requestID != "" {
		h.requests[existing.requestID] = existing
	}
	if requestID != "" {
		h.requests[requestID] = existing
	}
	h.addRunProjectIndexLocked(runID, projectName)
	h.mu.Unlock()
	return h.plannerReceiptFromHandle(existing), nil
}

func (h *ExecutionHost) GetRunStatus(ctx context.Context, runID uint) (*RunStatus, error) {
	if h == nil || h.resolver == nil {
		return nil, fmt.Errorf("execution host 未初始化")
	}
	if runID == 0 {
		return nil, fmt.Errorf("run_id 不能为空")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if handle, ok := h.getRunHandle(runID); ok {
		project, err := h.resolver.OpenProject(handle.project)
		if err == nil {
			status, serr := project.GetTaskStatus(ctx, runID)
			if serr == nil && status != nil {
				if status.Project == "" {
					status.Project = handle.project
				}
				return status, nil
			}
		}
	}
	status, _, err := h.findRunStatusByIndex(ctx, runID)
	if err != nil {
		return nil, err
	}
	if status != nil {
		return status, nil
	}
	status, _, err = h.findRunStatusByScan(ctx, runID)
	return status, err
}

func (h *ExecutionHost) ListRunEvents(ctx context.Context, runID uint, limit int) ([]RunEvent, error) {
	if h == nil || h.resolver == nil {
		return nil, fmt.Errorf("execution host 未初始化")
	}
	if runID == 0 {
		return nil, fmt.Errorf("run_id 不能为空")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if limit <= 0 {
		limit = 100
	}

	projectName := ""
	if handle, ok := h.getRunHandle(runID); ok {
		projectName = handle.project
	}
	if projectName == "" {
		_, foundProject, err := h.findRunStatusByIndex(ctx, runID)
		if err != nil {
			return nil, err
		}
		projectName = foundProject
	}
	if projectName == "" {
		_, foundProject, err := h.findRunStatusByScan(ctx, runID)
		if err != nil {
			return nil, err
		}
		projectName = foundProject
	}
	if projectName == "" {
		return nil, nil
	}
	project, err := h.resolver.OpenProject(projectName)
	if err != nil {
		return nil, err
	}
	return project.ListTaskEvents(ctx, runID, limit)
}

func (h *ExecutionHost) CancelRun(runID uint) (CancelResult, error) {
	if h == nil || h.resolver == nil {
		return CancelResult{}, fmt.Errorf("execution host 未初始化")
	}
	if runID == 0 {
		return CancelResult{}, fmt.Errorf("run_id 不能为空")
	}
	h.mu.RLock()
	handle := h.runs[runID]
	h.mu.RUnlock()
	if handle != nil {
		if handle.cancel != nil {
			handle.cancel()
		}
		return CancelResult{
			Found:     true,
			Canceled:  true,
			Project:   handle.project,
			RequestID: handle.requestID,
			Reason:    "cancel signal sent",
		}, nil
	}
	status, projectName, err := h.findRunStatusByIndex(context.Background(), runID)
	if err != nil {
		return CancelResult{}, err
	}
	if status == nil {
		status, projectName, err = h.findRunStatusByScan(context.Background(), runID)
	}
	if err != nil {
		return CancelResult{}, err
	}
	if status == nil {
		return CancelResult{Found: false, Canceled: false}, nil
	}
	return CancelResult{
		Found:     true,
		Canceled:  false,
		Project:   projectName,
		RequestID: "",
		Reason:    "run 不在当前 daemon 执行上下文中（可能已结束或由旧实例启动）",
	}, nil
}
