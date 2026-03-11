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

	"dalek/internal/contracts"
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
	receipt, err := h.SubmitWorkerRun(ctx, WorkerRunSubmitRequest{
		Project:    req.Project,
		TicketID:   req.TicketID,
		RequestID:  req.RequestID,
		Prompt:     req.Prompt,
		AutoStart:  req.AutoStart,
		BaseBranch: req.BaseBranch,
	})
	if err != nil {
		return DispatchSubmitReceipt{}, err
	}
	return dispatchReceiptFromWorkerReceipt(receipt), nil
}

func (h *ExecutionHost) StartTicket(ctx context.Context, req StartTicketRequest) (StartTicketReceipt, error) {
	if h == nil || h.resolver == nil {
		return StartTicketReceipt{}, fmt.Errorf("execution host 未初始化")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	projectName := strings.TrimSpace(req.Project)
	if projectName == "" {
		return StartTicketReceipt{}, fmt.Errorf("project 不能为空")
	}
	if req.TicketID == 0 {
		return StartTicketReceipt{}, fmt.Errorf("ticket_id 不能为空")
	}
	project, err := h.resolver.OpenProject(projectName)
	if err != nil {
		return StartTicketReceipt{}, err
	}
	worker, err := project.StartTicket(ctx, req.TicketID, StartTicketOptions{
		BaseBranch: strings.TrimSpace(req.BaseBranch),
	})
	if err != nil {
		return StartTicketReceipt{}, err
	}
	receipt := StartTicketReceipt{
		Started:  true,
		Project:  projectName,
		TicketID: req.TicketID,
	}
	if worker != nil {
		receipt.WorkerID = worker.ID
		receipt.WorktreePath = strings.TrimSpace(worker.WorktreePath)
		receipt.Branch = strings.TrimSpace(worker.Branch)
		receipt.LogPath = strings.TrimSpace(worker.LogPath)
	}
	if view, verr := project.GetTicketViewByID(ctx, req.TicketID); verr == nil && view != nil {
		receipt.WorkflowStatus = view.Ticket.WorkflowStatus
	}
	return receipt, nil
}

func (h *ExecutionHost) SubmitWorkerRun(ctx context.Context, req WorkerRunSubmitRequest) (WorkerRunSubmitReceipt, error) {
	handle, err := h.submitTicketRun(ctx, ticketRunSubmitRequest{
		kind:          runKindWorker,
		project:       req.Project,
		ticketID:      req.TicketID,
		requestID:     req.RequestID,
		prompt:        req.Prompt,
		autoStart:     req.AutoStart,
		baseBranch:    req.BaseBranch,
		requestPrefix: "wrk",
	})
	if err != nil {
		return WorkerRunSubmitReceipt{}, err
	}
	return h.workerReceiptFromHandle(handle), nil
}

type ticketRunSubmitRequest struct {
	kind          executionRunKind
	project       string
	ticketID      uint
	requestID     string
	prompt        string
	autoStart     *bool
	baseBranch    string
	requestPrefix string
}

func (h *ExecutionHost) submitTicketRun(ctx context.Context, req ticketRunSubmitRequest) (*executionRunHandle, error) {
	if h == nil || h.resolver == nil {
		return nil, fmt.Errorf("execution host 未初始化")
	}
	_ = ctx
	projectName := strings.TrimSpace(req.project)
	if projectName == "" {
		return nil, fmt.Errorf("project 不能为空")
	}
	if req.ticketID == 0 {
		return nil, fmt.Errorf("ticket_id 不能为空")
	}
	requestID := strings.TrimSpace(req.requestID)
	retainRequest := requestID != ""
	if requestID != "" {
		if handle, ok := h.lookupTicketRunHandle(req.kind, projectName, req.ticketID, requestID); ok {
			return handle, nil
		}
	}
	if requestID == "" {
		requestID = NewRequestID(req.requestPrefix)
	}

	runCtx, cancel := context.WithCancel(context.Background())
	handle := &executionRunHandle{
		kind:          req.kind,
		project:       projectName,
		requestID:     requestID,
		retainRequest: retainRequest,
		ticketID:      req.ticketID,
		entryPrompt:   req.prompt,
		autoStart:     copyBoolPtr(req.autoStart),
		baseBranch:    strings.TrimSpace(req.baseBranch),
		ctx:           runCtx,
		cancel:        cancel,
		ready:         make(chan struct{}),
		done:          make(chan struct{}),
	}

	h.mu.Lock()
	if existing := h.requests[requestID]; existing != nil {
		h.mu.Unlock()
		cancel()
		if existing.kind == req.kind && existing.project == projectName && existing.ticketID == req.ticketID {
			return existing, nil
		}
		return nil, fmt.Errorf("request_id 已绑定其他运行：%s", requestID)
	}
	h.requests[requestID] = handle
	h.wg.Add(1)
	h.mu.Unlock()

	go h.executeTicketRun(handle)

	select {
	case <-handle.ready:
	case <-time.After(workerRunReadyTimeout):
	}
	return handle, nil
}

func copyBoolPtr(v *bool) *bool {
	if v == nil {
		return nil
	}
	b := *v
	return &b
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

func (h *ExecutionHost) GetProjectDashboard(ctx context.Context, projectName string) (DashboardResult, error) {
	project, err := h.openDashboardProject(projectName)
	if err != nil {
		return DashboardResult{}, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return project.Dashboard(ctx)
}

func (h *ExecutionHost) GetProjectPlannerState(ctx context.Context, projectName string) (DashboardPlannerInfo, error) {
	project, err := h.openDashboardProject(projectName)
	if err != nil {
		return DashboardPlannerInfo{}, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	state, err := project.GetPMState(ctx)
	if err != nil {
		return DashboardPlannerInfo{}, err
	}
	return DashboardPlannerInfo{
		Dirty:           state.PlannerDirty,
		WakeVersion:     state.PlannerWakeVersion,
		ActiveTaskRunID: cloneDashboardUint(state.PlannerActiveTaskRunID),
		CooldownUntil:   cloneDashboardTime(state.PlannerCooldownUntil),
		LastRunAt:       cloneDashboardTime(state.PlannerLastRunAt),
		LastError:       strings.TrimSpace(state.PlannerLastError),
	}, nil
}

func (h *ExecutionHost) ListProjectMerges(ctx context.Context, projectName string, status contracts.MergeStatus, limit int) ([]contracts.MergeItem, error) {
	project, err := h.openDashboardProject(projectName)
	if err != nil {
		return nil, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return project.ListMergeItems(ctx, ListMergeItemsOptions{
		Status: status,
		Limit:  limit,
	})
}

func (h *ExecutionHost) ListProjectInbox(ctx context.Context, projectName string, status contracts.InboxStatus, limit int) ([]contracts.InboxItem, error) {
	project, err := h.openDashboardProject(projectName)
	if err != nil {
		return nil, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return project.ListInbox(ctx, ListInboxOptions{
		Status: status,
		Limit:  limit,
	})
}

func (h *ExecutionHost) openDashboardProject(projectName string) (DashboardProject, error) {
	if h == nil || h.resolver == nil {
		return nil, fmt.Errorf("execution host 未初始化")
	}
	projectName = strings.TrimSpace(projectName)
	if projectName == "" {
		return nil, fmt.Errorf("project 不能为空")
	}
	project, err := h.resolver.OpenProject(projectName)
	if err != nil {
		return nil, err
	}
	dashboardProject, ok := project.(DashboardProject)
	if !ok {
		return nil, fmt.Errorf("project %s 不支持 dashboard 查询", projectName)
	}
	return dashboardProject, nil
}

func cloneDashboardUint(src *uint) *uint {
	if src == nil {
		return nil
	}
	out := *src
	return &out
}

func cloneDashboardTime(src *time.Time) *time.Time {
	if src == nil {
		return nil
	}
	out := *src
	return &out
}
