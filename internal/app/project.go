package app

import (
	"context"
	agentprovider "dalek/internal/agent/provider"
	"dalek/internal/contracts"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"

	channelsvc "dalek/internal/services/channel"
	"dalek/internal/services/core"
	notebooksvc "dalek/internal/services/notebook"
	pmsvc "dalek/internal/services/pm"
	previewsvc "dalek/internal/services/preview"
	subagentsvc "dalek/internal/services/subagent"
	tasksvc "dalek/internal/services/task"
	ticketsvc "dalek/internal/services/ticket"
	workersvc "dalek/internal/services/worker"
	"dalek/internal/store"

	"gorm.io/gorm"
)

// Project 是对“单个已打开项目”的应用层 Facade。
//
// 约束：
// - cmd/tui 只能依赖 app，不应直接依赖下层实现包。
// - Project 不承载业务流程实现，只是把 services 组合成一个尽量稳定的 API。
type Project struct {
	core *core.Project

	ticket      *ticketsvc.Service
	ticketQuery *ticketsvc.QueryService
	worker      *workersvc.Service
	preview     *previewsvc.Service
	notebook    *notebooksvc.Service
	pm          *pmsvc.Service
	subagent    *subagentsvc.Service
	task        *tasksvc.Service
	channel     *channelsvc.Service

	closeOnce sync.Once
	closeErr  error
}

func (p *Project) Name() string {
	if p == nil || p.core == nil {
		return ""
	}
	return strings.TrimSpace(p.core.Name)
}

func (p *Project) Key() string {
	if p == nil || p.core == nil {
		return ""
	}
	return strings.TrimSpace(p.core.Key)
}

func (p *Project) RepoRoot() string {
	if p == nil || p.core == nil {
		return ""
	}
	return strings.TrimSpace(p.core.RepoRoot)
}

func (p *Project) ProjectDir() string {
	if p == nil || p.core == nil {
		return ""
	}
	return p.core.ProjectDir()
}

func (p *Project) TmuxSocket() string {
	if p == nil || p.core == nil {
		return ""
	}
	return strings.TrimSpace(p.core.Config.WithDefaults().TmuxSocket)
}

func (p *Project) RefreshInterval() time.Duration {
	if p == nil || p.core == nil {
		return time.Second
	}
	ms := p.core.Config.WithDefaults().RefreshIntervalMS
	if ms <= 0 {
		return time.Second
	}
	return time.Duration(ms) * time.Millisecond
}

func (p *Project) PMDispatchTimeout() time.Duration {
	if p == nil || p.core == nil {
		return 0
	}
	ms := p.core.Config.WithDefaults().PMDispatchTimeoutMS
	if ms <= 0 {
		return 0
	}
	return time.Duration(ms) * time.Millisecond
}

func (p *Project) GatewayTurnTimeout() time.Duration {
	if p == nil || p.core == nil {
		return 0
	}
	ms := p.core.Config.WithDefaults().GatewayAgent.TurnTimeoutMS
	if ms <= 0 {
		return 0
	}
	return time.Duration(ms) * time.Millisecond
}

func (p *Project) ChannelService() *channelsvc.Service {
	if p == nil {
		return nil
	}
	return p.channel
}

func (p *Project) OpenDBForTest() (*gorm.DB, error) {
	if p == nil || p.core == nil || p.core.DB == nil {
		return nil, fmt.Errorf("project db 为空")
	}
	return p.core.DB, nil
}

func (p *Project) Close() error {
	if p == nil {
		return nil
	}
	p.closeOnce.Do(func() {
		errs := make([]error, 0, 2)
		if p.channel != nil {
			if err := p.channel.Close(); err != nil {
				errs = append(errs, fmt.Errorf("关闭 channel service 失败: %w", err))
			}
		}
		if p.core != nil && p.core.DB != nil {
			sqlDB, err := p.core.DB.DB()
			if err != nil {
				errs = append(errs, fmt.Errorf("获取 db 连接失败: %w", err))
			} else if err := sqlDB.Close(); err != nil {
				errs = append(errs, fmt.Errorf("关闭 db 失败: %w", err))
			}
		}
		p.closeErr = errors.Join(errs...)
	})
	return p.closeErr
}

func (p *Project) ApplyAgentProviderModel(provider, model string) error {
	if p == nil || p.core == nil {
		return fmt.Errorf("project 为空")
	}
	provider = agentprovider.NormalizeProvider(provider)
	model = strings.TrimSpace(model)
	if provider != "" && !agentprovider.IsSupportedProvider(provider) {
		return fmt.Errorf("agent provider 仅支持 codex|claude: %s", provider)
	}
	cfg := applyAgentProviderModel(p.core.Config, provider, model)
	p.core.Config = cfg.WithDefaults()
	return nil
}

func (p *Project) StartTicket(ctx context.Context, ticketID uint) (*contracts.Worker, error) {
	return p.StartTicketWithOptions(ctx, ticketID, StartOptions{})
}

func (p *Project) StartTicketWithOptions(ctx context.Context, ticketID uint, opt StartOptions) (*contracts.Worker, error) {
	if p == nil || p.pm == nil {
		return nil, fmt.Errorf("project pm service 为空")
	}
	return p.pm.StartTicketWithOptions(ctx, ticketID, pmsvc.StartOptions{
		BaseBranch: strings.TrimSpace(opt.BaseBranch),
	})
}

func (p *Project) DispatchTicket(ctx context.Context, ticketID uint) (DispatchResult, error) {
	return p.DispatchTicketWithOptions(ctx, ticketID, DispatchOptions{})
}

func (p *Project) SubmitDispatchTicket(ctx context.Context, ticketID uint, opt DispatchSubmitOptions) (DispatchSubmission, error) {
	if p == nil || p.pm == nil {
		return DispatchSubmission{}, fmt.Errorf("project pm service 为空")
	}
	r, err := p.pm.SubmitDispatchTicket(ctx, ticketID, pmsvc.DispatchSubmitOptions{
		RequestID: strings.TrimSpace(opt.RequestID),
		AutoStart: opt.AutoStart,
	})
	if err != nil {
		return DispatchSubmission{}, err
	}
	return DispatchSubmission{
		JobID:      r.JobID,
		TaskRunID:  r.TaskRunID,
		RequestID:  strings.TrimSpace(r.RequestID),
		TicketID:   r.TicketID,
		WorkerID:   r.WorkerID,
		JobStatus:  strings.TrimSpace(string(r.JobStatus)),
		Dispatched: r.Dispatched,
	}, nil
}

func (p *Project) RunDispatchJob(ctx context.Context, jobID uint, opt DispatchRunOptions) error {
	if p == nil || p.pm == nil {
		return fmt.Errorf("project pm service 为空")
	}
	return p.pm.RunDispatchJob(ctx, jobID, pmsvc.DispatchRunOptions{
		RunnerID:    strings.TrimSpace(opt.RunnerID),
		EntryPrompt: strings.TrimSpace(opt.EntryPrompt),
	})
}

func (p *Project) DispatchTicketWithOptions(ctx context.Context, ticketID uint, opt DispatchOptions) (DispatchResult, error) {
	if p == nil || p.pm == nil {
		return DispatchResult{}, fmt.Errorf("project pm service 为空")
	}
	r, err := p.pm.DispatchTicketWithOptions(ctx, ticketID, pmsvc.DispatchOptions{
		EntryPrompt: strings.TrimSpace(opt.EntryPrompt),
		AutoStart:   opt.AutoStart,
	})
	if err != nil {
		return DispatchResult{}, err
	}
	return DispatchResult{
		TicketID:      r.TicketID,
		WorkerID:      r.WorkerID,
		TaskRunID:     r.TaskRunID,
		TmuxSocket:    r.TmuxSocket,
		TmuxSession:   r.TmuxSession,
		WorkerCommand: r.WorkerCommand,
		InjectedCmd:   r.InjectedCmd,
	}, nil
}

func (p *Project) DirectDispatchWorker(ctx context.Context, ticketID uint, opt DirectDispatchOptions) (DirectDispatchResult, error) {
	if p == nil || p.pm == nil {
		return DirectDispatchResult{}, fmt.Errorf("project pm service 为空")
	}
	r, err := p.pm.DirectDispatchWorker(ctx, ticketID, pmsvc.DirectDispatchOptions{
		EntryPrompt: strings.TrimSpace(opt.EntryPrompt),
		AutoStart:   opt.AutoStart,
	})
	if err != nil {
		return DirectDispatchResult{}, err
	}
	return DirectDispatchResult{
		TicketID:       r.TicketID,
		WorkerID:       r.WorkerID,
		Stages:         r.Stages,
		LastNextAction: strings.TrimSpace(r.LastNextAction),
		LastRunID:      r.LastRunID,
	}, nil
}

func (p *Project) FindLatestWorkerRun(ctx context.Context, ticketID uint, afterRunID uint) (*TaskStatus, error) {
	if p == nil || p.task == nil {
		return nil, fmt.Errorf("project task service 为空")
	}
	if ticketID == 0 {
		return nil, fmt.Errorf("ticket_id 不能为空")
	}
	statuses, err := p.ListTaskStatus(ctx, ListTaskOptions{
		OwnerType:       TaskOwnerWorker,
		TicketID:        ticketID,
		IncludeTerminal: true,
		Limit:           20,
	})
	if err != nil {
		return nil, err
	}
	for _, st := range statuses {
		if st.RunID > afterRunID {
			v := st
			return &v, nil
		}
	}
	return nil, nil
}

func (p *Project) InterruptTicket(ctx context.Context, ticketID uint) (InterruptResult, error) {
	if p == nil || p.worker == nil {
		return InterruptResult{}, fmt.Errorf("project worker service 为空")
	}
	r, err := p.worker.InterruptTicket(ctx, ticketID)
	if err != nil {
		return InterruptResult{}, err
	}
	return InterruptResult{
		TicketID:    r.TicketID,
		WorkerID:    r.WorkerID,
		TmuxSocket:  r.TmuxSocket,
		TmuxSession: r.TmuxSession,
		TargetPane:  r.TargetPane,
	}, nil
}

func (p *Project) InterruptWorker(ctx context.Context, workerID uint) (InterruptResult, error) {
	if p == nil || p.worker == nil {
		return InterruptResult{}, fmt.Errorf("project worker service 为空")
	}
	r, err := p.worker.InterruptWorker(ctx, workerID)
	if err != nil {
		return InterruptResult{}, err
	}
	return InterruptResult{
		TicketID:    r.TicketID,
		WorkerID:    r.WorkerID,
		TmuxSocket:  r.TmuxSocket,
		TmuxSession: r.TmuxSession,
		TargetPane:  r.TargetPane,
	}, nil
}

func (p *Project) StopWorker(ctx context.Context, workerID uint) error {
	if p == nil || p.worker == nil {
		return fmt.Errorf("project worker service 为空")
	}
	return p.worker.StopWorker(ctx, workerID)
}

func (p *Project) StopTicket(ctx context.Context, ticketID uint) error {
	if p == nil || p.worker == nil {
		return fmt.Errorf("project worker service 为空")
	}
	stopErr := p.worker.StopTicket(ctx, ticketID)

	var dispatchErr error
	if p.pm != nil {
		_, dispatchErr = p.pm.ForceFailActiveDispatchesForTicket(ctx, ticketID, "ticket stop: force fail active dispatch")
	} else if stopErr == nil {
		dispatchErr = fmt.Errorf("project pm service 为空")
	}

	if stopErr != nil && dispatchErr != nil {
		return fmt.Errorf("%w；另外 dispatch 终结失败: %v", stopErr, dispatchErr)
	}
	if stopErr != nil {
		return stopErr
	}
	return dispatchErr
}

func (p *Project) CleanupTicketWorktree(ctx context.Context, ticketID uint, opt WorktreeCleanupOptions) (WorktreeCleanupResult, error) {
	if p == nil || p.worker == nil {
		return WorktreeCleanupResult{}, fmt.Errorf("project worker service 为空")
	}
	r, err := p.worker.CleanupTicketWorktree(ctx, ticketID, workersvc.CleanupWorktreeOptions{
		Force:  opt.Force,
		DryRun: opt.DryRun,
	})
	if err != nil {
		return WorktreeCleanupResult{}, err
	}
	return WorktreeCleanupResult{
		TicketID:    r.TicketID,
		WorkerID:    r.WorkerID,
		Worktree:    strings.TrimSpace(r.Worktree),
		Branch:      strings.TrimSpace(r.Branch),
		RequestedAt: r.RequestedAt,
		CleanedAt:   r.CleanedAt,
		DryRun:      r.DryRun,
		Pending:     r.Pending,
		Cleaned:     r.Cleaned,
		Dirty:       r.Dirty,
		SessionLive: r.SessionLive,
		Message:     strings.TrimSpace(r.Message),
	}, nil
}

func (p *Project) CountPendingWorktreeCleanup(ctx context.Context) (int64, error) {
	if p == nil || p.worker == nil {
		return 0, fmt.Errorf("project worker service 为空")
	}
	return p.worker.CountPendingWorktreeCleanup(ctx)
}

func (p *Project) KillAllTmuxSessions(ctx context.Context) error {
	if p == nil || p.worker == nil {
		return fmt.Errorf("project worker service 为空")
	}
	return p.worker.KillAllTmuxSessions(ctx)
}

func (p *Project) ReconcileRunningWorkersAfterKillAll(ctx context.Context, socket string) (int64, error) {
	if p == nil || p.worker == nil {
		return 0, fmt.Errorf("project worker service 为空")
	}
	return p.worker.ReconcileRunningWorkersAfterKillAll(ctx, socket)
}

func (p *Project) AttachCmd(ctx context.Context, ticketID uint) (*exec.Cmd, error) {
	if p == nil || p.worker == nil {
		return nil, fmt.Errorf("project worker service 为空")
	}
	return p.worker.AttachCmd(ctx, ticketID)
}

func (p *Project) ListTicketViews(ctx context.Context) ([]TicketView, error) {
	if p == nil || p.ticketQuery == nil {
		return nil, fmt.Errorf("project ticket query service 为空")
	}
	views, err := p.ticketQuery.ListTicketViews(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]TicketView, 0, len(views))
	for _, v := range views {
		out = append(out, TicketView{
			Ticket:             v.Ticket,
			LatestWorker:       v.LatestWorker,
			SessionAlive:       v.SessionAlive,
			SessionProbeFailed: v.SessionProbeFailed,
			DerivedStatus:      v.DerivedStatus,
			Capability:         v.Capability,
			TaskRunID:          v.TaskRunID,
			RuntimeHealthState: v.RuntimeHealthState,
			RuntimeNeedsUser:   v.RuntimeNeedsUser,
			RuntimeSummary:     v.RuntimeSummary,
			RuntimeObservedAt:  v.RuntimeObservedAt,
			SemanticPhase:      v.SemanticPhase,
			SemanticNextAction: v.SemanticNextAction,
			SemanticSummary:    v.SemanticSummary,
			SemanticReportedAt: v.SemanticReportedAt,
			LastEventType:      v.LastEventType,
			LastEventNote:      v.LastEventNote,
			LastEventAt:        v.LastEventAt,
		})
	}
	return out, nil
}

func (p *Project) ListTickets(ctx context.Context, includeArchived bool) ([]contracts.Ticket, error) {
	if p == nil || p.ticket == nil {
		return nil, fmt.Errorf("project ticket service 为空")
	}
	return p.ticket.List(ctx, includeArchived)
}

func (p *Project) ListTaskStatus(ctx context.Context, opt ListTaskOptions) ([]TaskStatus, error) {
	if p == nil || p.task == nil {
		return nil, fmt.Errorf("project task service 为空")
	}
	views, err := p.task.ListStatus(ctx, tasksvc.ListStatusOptions{
		OwnerType:       opt.OwnerType,
		TaskType:        strings.TrimSpace(opt.TaskType),
		TicketID:        opt.TicketID,
		WorkerID:        opt.WorkerID,
		IncludeTerminal: opt.IncludeTerminal,
		Limit:           opt.Limit,
	})
	if err != nil {
		return nil, err
	}
	out := make([]TaskStatus, 0, len(views))
	for _, v := range views {
		out = append(out, mapTaskStatus(v))
	}
	return out, nil
}

func (p *Project) GetTaskStatus(ctx context.Context, runID uint) (*TaskStatus, error) {
	if p == nil || p.task == nil {
		return nil, fmt.Errorf("project task service 为空")
	}
	v, err := p.task.GetStatusByRunID(ctx, runID)
	if err != nil {
		return nil, err
	}
	if v == nil {
		return nil, nil
	}
	out := mapTaskStatus(*v)
	return &out, nil
}

func (p *Project) ListTaskEvents(ctx context.Context, runID uint, limit int) ([]TaskEvent, error) {
	if p == nil || p.task == nil {
		return nil, fmt.Errorf("project task service 为空")
	}
	evs, err := p.task.ListEvents(ctx, runID, limit)
	if err != nil {
		return nil, err
	}
	out := make([]TaskEvent, 0, len(evs))
	for _, ev := range evs {
		out = append(out, TaskEvent{
			ID:            ev.ID,
			TaskRunID:     ev.TaskRunID,
			EventType:     strings.TrimSpace(ev.EventType),
			FromStateJSON: strings.TrimSpace(ev.FromStateJSON),
			ToStateJSON:   strings.TrimSpace(ev.ToStateJSON),
			Note:          strings.TrimSpace(ev.Note),
			PayloadJSON:   strings.TrimSpace(ev.PayloadJSON),
			CreatedAt:     ev.CreatedAt,
		})
	}
	return out, nil
}

func (p *Project) CreateSubagentRun(ctx context.Context, opt CreateSubagentRunOptions) (SubagentRun, error) {
	if p == nil || p.task == nil {
		return SubagentRun{}, fmt.Errorf("project task service 为空")
	}
	rec, err := p.task.CreateSubagentRun(ctx, tasksvc.CreateSubagentRunInput{
		ProjectKey: strings.TrimSpace(p.Key()),
		TaskRunID:  opt.TaskRunID,
		RequestID:  strings.TrimSpace(opt.RequestID),
		Provider:   strings.TrimSpace(opt.Provider),
		Model:      strings.TrimSpace(opt.Model),
		Prompt:     strings.TrimSpace(opt.Prompt),
		CWD:        strings.TrimSpace(opt.CWD),
		RuntimeDir: strings.TrimSpace(opt.RuntimeDir),
	})
	if err != nil {
		return SubagentRun{}, err
	}
	return mapSubagentRun(rec), nil
}

func (p *Project) GetSubagentRun(ctx context.Context, runID uint) (*SubagentRun, error) {
	if p == nil || p.task == nil {
		return nil, fmt.Errorf("project task service 为空")
	}
	rec, err := p.task.FindSubagentRunByTaskRunID(ctx, runID)
	if err != nil {
		return nil, err
	}
	if rec == nil {
		return nil, nil
	}
	out := mapSubagentRun(*rec)
	return &out, nil
}

func (p *Project) ListSubagentRuns(ctx context.Context, limit int) ([]SubagentRun, error) {
	if p == nil || p.task == nil {
		return nil, fmt.Errorf("project task service 为空")
	}
	rows, err := p.task.ListSubagentRuns(ctx, strings.TrimSpace(p.Key()), limit)
	if err != nil {
		return nil, err
	}
	out := make([]SubagentRun, 0, len(rows))
	for _, row := range rows {
		out = append(out, mapSubagentRun(row))
	}
	return out, nil
}

func (p *Project) FinishAgentRun(ctx context.Context, runID uint, exitCode int) error {
	if p == nil || p.task == nil {
		return fmt.Errorf("project task service 为空")
	}
	if runID == 0 {
		return fmt.Errorf("run_id 不能为空")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	now := time.Now()
	if exitCode == 0 {
		if err := p.task.MarkRunSucceeded(ctx, runID, "", now); err != nil {
			return err
		}
		return p.task.AppendEvent(ctx, tasksvc.TaskEventInput{
			TaskRunID: runID,
			EventType: "task_succeeded",
			FromState: map[string]any{"orchestration_state": "running"},
			ToState:   map[string]any{"orchestration_state": "succeeded"},
			Note:      "agent finish exit_code=0",
			CreatedAt: now,
		})
	}
	msg := fmt.Sprintf("agent_exit code=%d", exitCode)
	if err := p.task.MarkRunFailed(ctx, runID, "agent_exit", msg, now); err != nil {
		return err
	}
	return p.task.AppendEvent(ctx, tasksvc.TaskEventInput{
		TaskRunID: runID,
		EventType: "task_failed",
		FromState: map[string]any{"orchestration_state": "running"},
		ToState:   map[string]any{"orchestration_state": "failed"},
		Note:      msg,
		CreatedAt: now,
	})
}

func (p *Project) CancelTaskRun(ctx context.Context, runID uint) (TaskCancelResult, error) {
	if p == nil || p.task == nil {
		return TaskCancelResult{}, fmt.Errorf("project task service 为空")
	}
	if runID == 0 {
		return TaskCancelResult{}, fmt.Errorf("run_id 不能为空")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	st, err := p.task.GetStatusByRunID(ctx, runID)
	if err != nil {
		return TaskCancelResult{}, err
	}
	if st == nil {
		return TaskCancelResult{
			RunID:    runID,
			Found:    false,
			Canceled: false,
			Reason:   fmt.Sprintf("task run #%d 不存在", runID),
		}, nil
	}

	fromState := strings.TrimSpace(string(st.OrchestrationState))
	if fromState == "" {
		fromState = "unknown"
	}
	result := TaskCancelResult{
		RunID:     runID,
		Found:     true,
		FromState: fromState,
		ToState:   fromState,
	}

	switch strings.ToLower(strings.TrimSpace(st.OrchestrationState)) {
	case string(contracts.TaskSucceeded), string(contracts.TaskFailed), string(contracts.TaskCanceled):
		result.Canceled = false
		result.Reason = fmt.Sprintf("task run 已结束，当前状态=%s", fromState)
		return result, nil
	}

	now := time.Now()
	reason := "canceled by task cancel command"
	if err := p.task.MarkRunCanceled(ctx, runID, "manual_cancel", reason, now); err != nil {
		return TaskCancelResult{}, err
	}
	if err := p.task.AppendEvent(ctx, tasksvc.TaskEventInput{
		TaskRunID: runID,
		EventType: "task_canceled",
		FromState: map[string]any{
			"orchestration_state": fromState,
		},
		ToState: map[string]any{
			"orchestration_state": contracts.TaskCanceled,
		},
		Note:      reason,
		Payload:   map[string]any{"source": "dalek task cancel"},
		CreatedAt: now,
	}); err != nil {
		return TaskCancelResult{}, err
	}

	result.Canceled = true
	result.ToState = string(contracts.TaskCanceled)
	return result, nil
}

func (p *Project) CreateTicket(ctx context.Context, title string) (*contracts.Ticket, error) {
	return p.CreateTicketWithDescription(ctx, title, "")
}

func (p *Project) CreateTicketWithDescription(ctx context.Context, title, description string) (*contracts.Ticket, error) {
	if p == nil || p.ticket == nil {
		return nil, fmt.Errorf("project ticket service 为空")
	}
	return p.ticket.CreateWithDescription(ctx, title, description)
}

func mapTaskStatus(v store.TaskStatusView) TaskStatus {
	return TaskStatus{
		RunID: v.RunID,

		OwnerType:  strings.TrimSpace(v.OwnerType),
		TaskType:   strings.TrimSpace(v.TaskType),
		ProjectKey: strings.TrimSpace(v.ProjectKey),

		TicketID: v.TicketID,
		WorkerID: v.WorkerID,

		SubjectType: strings.TrimSpace(v.SubjectType),
		SubjectID:   strings.TrimSpace(v.SubjectID),

		OrchestrationState: strings.TrimSpace(v.OrchestrationState),
		RunnerID:           strings.TrimSpace(v.RunnerID),
		LeaseExpiresAt:     v.LeaseExpiresAt,
		Attempt:            v.Attempt,

		ErrorCode:    strings.TrimSpace(v.ErrorCode),
		ErrorMessage: strings.TrimSpace(v.ErrorMessage),

		StartedAt:  v.StartedAt,
		FinishedAt: v.FinishedAt,
		CreatedAt:  v.CreatedAt,
		UpdatedAt:  v.UpdatedAt,

		RuntimeHealthState: strings.TrimSpace(v.RuntimeHealthState),
		RuntimeNeedsUser:   v.RuntimeNeedsUser,
		RuntimeSummary:     strings.TrimSpace(v.RuntimeSummary),
		RuntimeObservedAt:  v.RuntimeObservedAt,

		SemanticPhase:      strings.TrimSpace(v.SemanticPhase),
		SemanticMilestone:  strings.TrimSpace(v.SemanticMilestone),
		SemanticNextAction: strings.TrimSpace(v.SemanticNextAction),
		SemanticSummary:    strings.TrimSpace(v.SemanticSummary),
		SemanticReportedAt: v.SemanticReportedAt,

		LastEventType: strings.TrimSpace(v.LastEventType),
		LastEventNote: strings.TrimSpace(v.LastEventNote),
		LastEventAt:   v.LastEventAt,
	}
}

func mapSubagentRun(v contracts.SubagentRun) SubagentRun {
	return SubagentRun{
		ID:         v.ID,
		TaskRunID:  v.TaskRunID,
		ProjectKey: strings.TrimSpace(v.ProjectKey),
		RequestID:  strings.TrimSpace(v.RequestID),
		Provider:   strings.TrimSpace(v.Provider),
		Model:      strings.TrimSpace(v.Model),
		Prompt:     strings.TrimSpace(v.Prompt),
		CWD:        strings.TrimSpace(v.CWD),
		RuntimeDir: strings.TrimSpace(v.RuntimeDir),
		CreatedAt:  v.CreatedAt,
		UpdatedAt:  v.UpdatedAt,
	}
}

func (p *Project) ArchiveTicket(ctx context.Context, ticketID uint) error {
	if p == nil || p.pm == nil {
		return fmt.Errorf("project pm service 为空")
	}
	return p.pm.ArchiveTicket(ctx, ticketID)
}

func (p *Project) SetTicketWorkflowStatus(ctx context.Context, ticketID uint, status TicketWorkflowStatus) error {
	if p == nil || p.pm == nil {
		return fmt.Errorf("project pm service 为空")
	}
	return p.pm.SetTicketWorkflowStatus(ctx, ticketID, status)
}

func (p *Project) BumpTicketPriority(ctx context.Context, ticketID uint, delta int) (int, error) {
	if p == nil || p.ticket == nil {
		return 0, fmt.Errorf("project ticket service 为空")
	}
	return p.ticket.BumpPriority(ctx, ticketID, delta)
}

func (p *Project) UpdateTicketText(ctx context.Context, ticketID uint, title, description string) error {
	if p == nil || p.ticket == nil {
		return fmt.Errorf("project ticket service 为空")
	}
	return p.ticket.UpdateText(ctx, ticketID, title, description)
}

func (p *Project) ApplyWorkerReport(ctx context.Context, r WorkerReport, source string) error {
	if p == nil || p.pm == nil {
		return fmt.Errorf("project pm service 为空")
	}
	return p.pm.ApplyWorkerReport(ctx, r, source)
}

func (p *Project) LatestWorker(ctx context.Context, ticketID uint) (*Worker, error) {
	if p == nil || p.worker == nil {
		return nil, fmt.Errorf("project worker service 为空")
	}
	return p.worker.LatestWorker(ctx, ticketID)
}

func (p *Project) WorkerByID(ctx context.Context, workerID uint) (*Worker, error) {
	if p == nil || p.worker == nil {
		return nil, fmt.Errorf("project worker service 为空")
	}
	return p.worker.WorkerByID(ctx, workerID)
}

func (p *Project) CaptureTicketTail(ctx context.Context, ticketID uint, lastLines int) (TailPreview, error) {
	if p == nil || p.preview == nil {
		return TailPreview{}, fmt.Errorf("project preview service 为空")
	}
	return p.preview.CaptureTicketTail(ctx, ticketID, lastLines)
}

func (p *Project) ListTaskEventsByScope(ctx context.Context, ticketID, workerID uint, limit int) ([]TaskEvent, error) {
	if p == nil || p.task == nil {
		return nil, fmt.Errorf("project task service 为空")
	}
	rows, err := p.task.ListEventsByScope(ctx, ticketID, workerID, limit)
	if err != nil {
		return nil, err
	}
	out := make([]TaskEvent, 0, len(rows))
	for _, ev := range rows {
		out = append(out, TaskEvent{
			ID:            ev.ID,
			TaskRunID:     ev.TaskRunID,
			EventType:     strings.TrimSpace(ev.EventType),
			FromStateJSON: strings.TrimSpace(ev.FromStateJSON),
			ToStateJSON:   strings.TrimSpace(ev.ToStateJSON),
			Note:          strings.TrimSpace(ev.Note),
			PayloadJSON:   strings.TrimSpace(ev.PayloadJSON),
			CreatedAt:     ev.CreatedAt,
		})
	}
	return out, nil
}

func (p *Project) ListRunningWorkers(ctx context.Context) ([]contracts.Worker, error) {
	if p == nil || p.worker == nil {
		return nil, fmt.Errorf("project worker service 为空")
	}
	return p.worker.ListRunningWorkers(ctx)
}

// ----- manager/pm facade -----

func (p *Project) ManagerSessionName() string {
	if p == nil || p.pm == nil {
		return ""
	}
	return strings.TrimSpace(p.pm.ManagerSessionName())
}

func (p *Project) GetPMState(ctx context.Context) (contracts.PMState, error) {
	if p == nil || p.pm == nil {
		return contracts.PMState{}, fmt.Errorf("project pm service 为空")
	}
	return p.pm.GetState(ctx)
}

func (p *Project) SetAutopilotEnabled(ctx context.Context, enabled bool) (contracts.PMState, error) {
	if p == nil || p.pm == nil {
		return contracts.PMState{}, fmt.Errorf("project pm service 为空")
	}
	return p.pm.SetAutopilotEnabled(ctx, enabled)
}

func (p *Project) SetMaxRunningWorkers(ctx context.Context, n int) (contracts.PMState, error) {
	if p == nil || p.pm == nil {
		return contracts.PMState{}, fmt.Errorf("project pm service 为空")
	}
	return p.pm.SetMaxRunningWorkers(ctx, n)
}

func (p *Project) ListInbox(ctx context.Context, opt ListInboxOptions) ([]contracts.InboxItem, error) {
	if p == nil || p.pm == nil {
		return nil, fmt.Errorf("project pm service 为空")
	}
	return p.pm.ListInbox(ctx, pmsvc.ListInboxOptions{
		Status: opt.Status,
		Limit:  opt.Limit,
	})
}

func (p *Project) GetInboxItem(ctx context.Context, id uint) (*contracts.InboxItem, error) {
	if p == nil || p.pm == nil {
		return nil, fmt.Errorf("project pm service 为空")
	}
	return p.pm.GetInboxItem(ctx, id)
}

func (p *Project) CloseInboxItem(ctx context.Context, id uint) error {
	if p == nil || p.pm == nil {
		return fmt.Errorf("project pm service 为空")
	}
	return p.pm.CloseInboxItem(ctx, id)
}

func (p *Project) SnoozeInboxItem(ctx context.Context, id uint, until time.Time) error {
	if p == nil || p.pm == nil {
		return fmt.Errorf("project pm service 为空")
	}
	return p.pm.SnoozeInboxItem(ctx, id, until)
}

func (p *Project) UnsnoozeInboxItem(ctx context.Context, id uint) error {
	if p == nil || p.pm == nil {
		return fmt.Errorf("project pm service 为空")
	}
	return p.pm.UnsnoozeInboxItem(ctx, id)
}

func (p *Project) DeleteInboxItem(ctx context.Context, id uint) error {
	if p == nil || p.pm == nil {
		return fmt.Errorf("project pm service 为空")
	}
	return p.pm.DeleteInboxItem(ctx, id)
}

func (p *Project) ListMergeItems(ctx context.Context, opt ListMergeOptions) ([]contracts.MergeItem, error) {
	if p == nil || p.pm == nil {
		return nil, fmt.Errorf("project pm service 为空")
	}
	return p.pm.ListMergeItems(ctx, pmsvc.ListMergeOptions{
		Status: opt.Status,
		Limit:  opt.Limit,
	})
}

func (p *Project) ProposeMerge(ctx context.Context, ticketID uint) (*contracts.MergeItem, error) {
	if p == nil || p.pm == nil {
		return nil, fmt.Errorf("project pm service 为空")
	}
	return p.pm.ProposeMerge(ctx, ticketID)
}

func (p *Project) ApproveMerge(ctx context.Context, mergeItemID uint, approvedBy string) error {
	if p == nil || p.pm == nil {
		return fmt.Errorf("project pm service 为空")
	}
	return p.pm.ApproveMerge(ctx, mergeItemID, approvedBy)
}

func (p *Project) DiscardMerge(ctx context.Context, mergeItemID uint, reason string) error {
	if p == nil || p.pm == nil {
		return fmt.Errorf("project pm service 为空")
	}
	return p.pm.DiscardMerge(ctx, mergeItemID, reason)
}

func (p *Project) MarkMergeMerged(ctx context.Context, mergeItemID uint) error {
	if p == nil || p.pm == nil {
		return fmt.Errorf("project pm service 为空")
	}
	return p.pm.MarkMergeMerged(ctx, mergeItemID)
}

func (p *Project) EnsureManagerSession(ctx context.Context) (string, error) {
	if p == nil || p.pm == nil {
		return "", fmt.Errorf("project pm service 为空")
	}
	return p.pm.EnsureManagerSession(ctx)
}

func (p *Project) SendManagerLine(ctx context.Context, line string) error {
	if p == nil || p.pm == nil {
		return fmt.Errorf("project pm service 为空")
	}
	return p.pm.SendManagerLine(ctx, line)
}

func (p *Project) ManagerAttachCmd(ctx context.Context) (*exec.Cmd, error) {
	if p == nil || p.pm == nil {
		return nil, fmt.Errorf("project pm service 为空")
	}
	return p.pm.ManagerAttachCmd(ctx)
}

func (p *Project) CaptureManagerTailPreview(ctx context.Context, lastLines int) (TailPreview, error) {
	if p == nil || p.pm == nil {
		return TailPreview{}, fmt.Errorf("project pm service 为空")
	}
	return p.pm.CaptureManagerTailPreview(ctx, lastLines)
}

func (p *Project) ManagerTick(ctx context.Context, opt ManagerTickOptions) (ManagerTickResult, error) {
	if p == nil || p.pm == nil {
		return ManagerTickResult{}, fmt.Errorf("project pm service 为空")
	}
	res, err := p.pm.ManagerTick(ctx, pmsvc.ManagerTickOptions{
		MaxRunningWorkers: opt.MaxRunningWorkers,
		DryRun:            opt.DryRun,
		SyncDispatch:      opt.SyncDispatch,
		DispatchTimeout:   opt.DispatchTimeout,
	})
	if err != nil {
		return ManagerTickResult{}, err
	}
	return ManagerTickResult{
		At:                res.At,
		AutopilotEnabled:  res.AutopilotEnabled,
		MaxRunning:        res.MaxRunning,
		Running:           res.Running,
		RunningBlocked:    res.RunningBlocked,
		Capacity:          res.Capacity,
		EventsConsumed:    res.EventsConsumed,
		InboxUpserts:      res.InboxUpserts,
		StartedTickets:    res.StartedTickets,
		DispatchedTickets: res.DispatchedTickets,
		MergeProposed:     res.MergeProposed,
		Errors:            res.Errors,
	}, nil
}
