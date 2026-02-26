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

const (
	defaultExecutionHostConcurrency = 4
	workerRunIDProbeTimeout         = 2 * time.Second
)

// StopTimeoutError 表示 ExecutionHost.Stop 在上下文截止前仍有运行未退出。
// 调用方可通过 errors.Is(err, context.DeadlineExceeded/Canceled) 判断超时原因，
// 也可读取 PendingCount/PendingSummary 获取未退出任务摘要。
type StopTimeoutError struct {
	Cause          error
	PendingCount   int
	PendingSummary []string
}

func (e *StopTimeoutError) Error() string {
	if e == nil {
		return ""
	}
	cause := strings.TrimSpace(fmt.Sprint(e.Cause))
	if cause == "" {
		cause = "unknown"
	}
	return fmt.Sprintf("execution host stop timeout: pending_count=%d cause=%s", e.PendingCount, cause)
}

func (e *StopTimeoutError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

type ExecutionHostResolver interface {
	OpenProject(name string) (ExecutionHostProject, error)
	ListProjects() ([]string, error)
}

type ExecutionHostProject interface {
	SubmitDispatchTicket(ctx context.Context, ticketID uint, opt DispatchSubmitOptions) (DispatchSubmission, error)
	RunDispatchJob(ctx context.Context, jobID uint, opt DispatchRunOptions) error
	DirectDispatchWorker(ctx context.Context, ticketID uint, opt WorkerRunOptions) (WorkerRunResult, error)
	SubmitSubagentRun(ctx context.Context, opt SubagentSubmitOptions) (SubagentSubmission, error)
	RunSubagentJob(ctx context.Context, taskRunID uint, opt SubagentRunOptions) error
	FindLatestWorkerRun(ctx context.Context, ticketID uint, afterRunID uint) (*RunStatus, error)
	AddNote(ctx context.Context, rawText string) (NoteAddResult, error)
	GetTaskStatus(ctx context.Context, runID uint) (*RunStatus, error)
	ListTaskEvents(ctx context.Context, runID uint, limit int) ([]RunEvent, error)
}

type DispatchSubmitOptions struct {
	RequestID string
	AutoStart *bool
}

type DispatchSubmission struct {
	JobID      uint
	TaskRunID  uint
	RequestID  string
	TicketID   uint
	WorkerID   uint
	JobStatus  string
	Dispatched bool
}

type DispatchRunOptions struct {
	RunnerID    string
	EntryPrompt string
}

type DispatchSubmitRequest struct {
	Project   string
	TicketID  uint
	RequestID string
	Prompt    string
	AutoStart *bool
}

type DispatchSubmitReceipt struct {
	Accepted  bool
	Project   string
	RequestID string
	TaskRunID uint
	JobID     uint
	TicketID  uint
	WorkerID  uint
	JobStatus string
}

type WorkerRunOptions struct {
	EntryPrompt string
}

type WorkerRunResult struct {
	TicketID uint
	WorkerID uint
}

type WorkerRunSubmitRequest struct {
	Project   string
	TicketID  uint
	RequestID string
	Prompt    string
}

type WorkerRunSubmitReceipt struct {
	Accepted  bool
	Project   string
	RequestID string
	TaskRunID uint
	TicketID  uint
	WorkerID  uint
}

type SubagentSubmitOptions struct {
	RequestID string
	Provider  string
	Model     string
	Prompt    string
}

type SubagentSubmission struct {
	Accepted bool

	TaskRunID  uint
	RequestID  string
	Provider   string
	Model      string
	RuntimeDir string
}

type SubagentRunOptions struct {
	RunnerID string
}

type SubagentSubmitRequest struct {
	Project string

	RequestID string
	Provider  string
	Model     string
	Prompt    string
}

type SubagentSubmitReceipt struct {
	Accepted bool

	Project    string
	TaskRunID  uint
	RequestID  string
	Provider   string
	Model      string
	RuntimeDir string
}

type NoteAddResult struct {
	NoteID       uint
	ShapedItemID uint
	Deduped      bool
}

type NoteSubmitRequest struct {
	Project string
	Text    string
}

type NoteSubmitReceipt struct {
	Accepted     bool
	Project      string
	NoteID       uint
	ShapedItemID uint
	Deduped      bool
}

type RunStatus struct {
	RunID uint

	Project string

	OwnerType string
	TaskType  string

	TicketID uint
	WorkerID uint

	OrchestrationState string
	RuntimeHealthState string
	RuntimeNeedsUser   bool
	RuntimeSummary     string
	SemanticNextAction string
	SemanticSummary    string

	ErrorCode    string
	ErrorMessage string

	StartedAt  *time.Time
	FinishedAt *time.Time
	UpdatedAt  time.Time
}

type RunEvent struct {
	ID            uint
	TaskRunID     uint
	EventType     string
	FromStateJSON string
	ToStateJSON   string
	Note          string
	PayloadJSON   string
	CreatedAt     time.Time
}

type CancelResult struct {
	Found     bool
	Canceled  bool
	Project   string
	RequestID string
	Reason    string
}

type ExecutionHostOptions struct {
	Logger        *slog.Logger
	MaxConcurrent int
	OnRunSettled  func(project string)
	OnNoteAdded   func(project string)
}

type executionRunKind string

const (
	runKindDispatch executionRunKind = "dispatch"
	runKindWorker   executionRunKind = "worker_run"
	runKindSubagent executionRunKind = "subagent"
)

type executionRunHandle struct {
	kind executionRunKind

	project   string
	requestID string
	runID     uint
	jobID     uint
	jobStatus string
	ticketID  uint
	workerID  uint

	runnerID    string
	entryPrompt string
	provider    string
	model       string

	ctx    context.Context
	cancel context.CancelFunc

	ready     chan struct{}
	readyOnce sync.Once
	done      chan struct{}
	doneOnce  sync.Once
}

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

func (h *ExecutionHost) Name() string {
	return "execution_host"
}

func (h *ExecutionHost) Start(ctx context.Context) error {
	return nil
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
	requestID = strings.TrimSpace(submission.RequestID)
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
			jobStatus:   strings.TrimSpace(submission.JobStatus),
			ticketID:    req.TicketID,
			workerID:    submission.WorkerID,
			runnerID:    "daemon_" + NewRequestID("runner"),
			entryPrompt: strings.TrimSpace(req.Prompt),
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
			existing.jobStatus = strings.TrimSpace(submission.JobStatus)
		}
		if existing.entryPrompt == "" {
			existing.entryPrompt = strings.TrimSpace(req.Prompt)
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
		JobStatus: strings.TrimSpace(submission.JobStatus),
	}, nil
}

func (h *ExecutionHost) lookupDispatchRequest(project string, ticketID uint, requestID string) (DispatchSubmitReceipt, bool) {
	h.mu.RLock()
	handle := h.requests[strings.TrimSpace(requestID)]
	h.mu.RUnlock()
	if handle == nil {
		return DispatchSubmitReceipt{}, false
	}
	if handle.kind != runKindDispatch {
		return DispatchSubmitReceipt{}, false
	}
	if strings.TrimSpace(handle.project) != strings.TrimSpace(project) || handle.ticketID != ticketID {
		return DispatchSubmitReceipt{}, false
	}
	return h.dispatchReceiptFromHandle(handle), true
}

func (h *ExecutionHost) dispatchReceiptFromHandle(handle *executionRunHandle) DispatchSubmitReceipt {
	if handle == nil {
		return DispatchSubmitReceipt{}
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	return DispatchSubmitReceipt{
		Accepted:  true,
		Project:   strings.TrimSpace(handle.project),
		RequestID: strings.TrimSpace(handle.requestID),
		TaskRunID: handle.runID,
		JobID:     handle.jobID,
		TicketID:  handle.ticketID,
		WorkerID:  handle.workerID,
		JobStatus: strings.TrimSpace(handle.jobStatus),
	}
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
		entryPrompt: strings.TrimSpace(req.Prompt),
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
	case <-time.After(workerRunIDProbeTimeout):
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
	submission, err := project.SubmitSubagentRun(ctx, SubagentSubmitOptions{
		RequestID: requestID,
		Provider:  strings.TrimSpace(req.Provider),
		Model:     strings.TrimSpace(req.Model),
		Prompt:    prompt,
	})
	if err != nil {
		return SubagentSubmitReceipt{}, err
	}
	if submission.TaskRunID == 0 {
		return SubagentSubmitReceipt{}, fmt.Errorf("subagent submit 未返回 task_run_id")
	}
	if strings.TrimSpace(submission.RequestID) != "" {
		requestID = strings.TrimSpace(submission.RequestID)
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
			provider:    strings.TrimSpace(submission.Provider),
			model:       strings.TrimSpace(submission.Model),
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
			existing.provider = strings.TrimSpace(submission.Provider)
		}
		if existing.model == "" {
			existing.model = strings.TrimSpace(submission.Model)
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
		Provider:   strings.TrimSpace(submission.Provider),
		Model:      strings.TrimSpace(submission.Model),
		RuntimeDir: strings.TrimSpace(submission.RuntimeDir),
	}, nil
}

func (h *ExecutionHost) SubmitNote(ctx context.Context, req NoteSubmitRequest) (NoteSubmitReceipt, error) {
	if h == nil || h.resolver == nil {
		return NoteSubmitReceipt{}, fmt.Errorf("execution host 未初始化")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	projectName := strings.TrimSpace(req.Project)
	if projectName == "" {
		return NoteSubmitReceipt{}, fmt.Errorf("project 不能为空")
	}
	raw := strings.TrimSpace(req.Text)
	if raw == "" {
		return NoteSubmitReceipt{}, fmt.Errorf("note text 不能为空")
	}
	project, err := h.resolver.OpenProject(projectName)
	if err != nil {
		return NoteSubmitReceipt{}, err
	}
	res, err := project.AddNote(ctx, raw)
	if err != nil {
		return NoteSubmitReceipt{}, err
	}
	h.notifyNoteAdded(projectName)
	return NoteSubmitReceipt{
		Accepted:     true,
		Project:      projectName,
		NoteID:       res.NoteID,
		ShapedItemID: res.ShapedItemID,
		Deduped:      res.Deduped,
	}, nil
}

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

func (h *ExecutionHost) lookupWorkerRequest(project string, ticketID uint, requestID string) (WorkerRunSubmitReceipt, bool) {
	h.mu.RLock()
	handle := h.requests[strings.TrimSpace(requestID)]
	h.mu.RUnlock()
	if handle == nil {
		return WorkerRunSubmitReceipt{}, false
	}
	if handle.kind != runKindWorker {
		return WorkerRunSubmitReceipt{}, false
	}
	if strings.TrimSpace(handle.project) != strings.TrimSpace(project) || handle.ticketID != ticketID {
		return WorkerRunSubmitReceipt{}, false
	}
	return h.workerReceiptFromHandle(handle), true
}

func (h *ExecutionHost) lookupSubagentRequest(project string, requestID string) (SubagentSubmitReceipt, bool) {
	h.mu.RLock()
	handle := h.requests[strings.TrimSpace(requestID)]
	h.mu.RUnlock()
	if handle == nil {
		return SubagentSubmitReceipt{}, false
	}
	if handle.kind != runKindSubagent {
		return SubagentSubmitReceipt{}, false
	}
	if strings.TrimSpace(handle.project) != strings.TrimSpace(project) {
		return SubagentSubmitReceipt{}, false
	}
	return h.subagentReceiptFromHandle(handle), true
}

func (h *ExecutionHost) workerReceiptFromHandle(handle *executionRunHandle) WorkerRunSubmitReceipt {
	if handle == nil {
		return WorkerRunSubmitReceipt{}
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	return WorkerRunSubmitReceipt{
		Accepted:  true,
		Project:   strings.TrimSpace(handle.project),
		RequestID: strings.TrimSpace(handle.requestID),
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
		Project:    strings.TrimSpace(handle.project),
		TaskRunID:  handle.runID,
		RequestID:  strings.TrimSpace(handle.requestID),
		Provider:   strings.TrimSpace(handle.provider),
		Model:      strings.TrimSpace(handle.model),
		RuntimeDir: "",
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
		if strings.TrimSpace(existing) == project {
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
			delete(h.requests, handle.requestID)
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
			strings.TrimSpace(string(handle.kind)),
			strings.TrimSpace(handle.project),
			strings.TrimSpace(handle.requestID),
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

func (h *ExecutionHost) getRunHandle(runID uint) (*executionRunHandle, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	handle := h.runs[runID]
	if handle == nil {
		return nil, false
	}
	return handle, true
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
				if strings.TrimSpace(status.Project) == "" {
					status.Project = strings.TrimSpace(handle.project)
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
		projectName = strings.TrimSpace(handle.project)
	}
	if projectName == "" {
		_, foundProject, err := h.findRunStatusByIndex(ctx, runID)
		if err != nil {
			return nil, err
		}
		projectName = strings.TrimSpace(foundProject)
	}
	if projectName == "" {
		_, foundProject, err := h.findRunStatusByScan(ctx, runID)
		if err != nil {
			return nil, err
		}
		projectName = strings.TrimSpace(foundProject)
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
			Project:   strings.TrimSpace(handle.project),
			RequestID: strings.TrimSpace(handle.requestID),
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
		Project:   strings.TrimSpace(projectName),
		RequestID: "",
		Reason:    "run 不在当前 daemon 执行上下文中（可能已结束或由旧实例启动）",
	}, nil
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
		if strings.TrimSpace(status.Project) == "" {
			status.Project = projectName
		}
		return status, projectName, nil
	}
	return nil, "", nil
}
