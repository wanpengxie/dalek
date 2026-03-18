package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"dalek/internal/contracts"
	daemonsvc "dalek/internal/services/daemon"
	pmsvc "dalek/internal/services/pm"
)

type daemonProjectResolver struct {
	home     *Home
	registry *ProjectRegistry
}

func newDaemonProjectResolver(home *Home, registries ...*ProjectRegistry) *daemonProjectResolver {
	var registry *ProjectRegistry
	if len(registries) > 0 {
		registry = registries[0]
	}
	if registry == nil && home != nil {
		registry = NewProjectRegistry(home)
	}
	return &daemonProjectResolver{
		home:     home,
		registry: registry,
	}
}

func (r *daemonProjectResolver) OpenProject(name string) (daemonsvc.ExecutionHostProject, error) {
	if r == nil || r.registry == nil {
		return nil, fmt.Errorf("daemon project resolver 未初始化")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("project 不能为空")
	}
	p, err := r.registry.Open(name)
	if err != nil {
		return nil, err
	}
	return &daemonProjectAdapter{project: p}, nil
}

func (r *daemonProjectResolver) ListProjects() ([]string, error) {
	if r == nil || r.home == nil {
		return nil, fmt.Errorf("daemon project resolver 未初始化")
	}
	projects, err := r.home.ListProjects()
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(projects))
	for _, p := range projects {
		name := strings.TrimSpace(p.Name)
		if name == "" {
			continue
		}
		out = append(out, name)
	}
	return out, nil
}

type daemonProjectAdapter struct {
	project *Project
}

func (p *daemonProjectAdapter) requireExecutionBaseBranch(ctx context.Context, ticketID uint, baseBranch string) error {
	if p == nil || p.project == nil {
		return fmt.Errorf("daemon project 为空")
	}
	view, err := p.project.GetTicketViewByID(ctx, ticketID)
	if err != nil {
		return err
	}
	if view == nil || !strings.EqualFold(strings.TrimSpace(view.Ticket.Label), "integration") {
		return nil
	}
	if strings.TrimSpace(view.Ticket.TargetBranch) == "" {
		return fmt.Errorf("integration ticket t%d 缺少 target_ref", ticketID)
	}
	if strings.TrimSpace(baseBranch) == "" {
		return fmt.Errorf("integration ticket t%d 缺少 base_branch", ticketID)
	}
	return nil
}

func (p *daemonProjectAdapter) StartTicket(ctx context.Context, ticketID uint, opt daemonsvc.StartTicketOptions) (*contracts.Worker, error) {
	if p == nil || p.project == nil {
		return nil, fmt.Errorf("daemon project 为空")
	}
	if err := p.requireExecutionBaseBranch(ctx, ticketID, opt.BaseBranch); err != nil {
		return nil, err
	}
	return p.project.StartTicketWithOptions(ctx, ticketID, StartOptions{
		BaseBranch: strings.TrimSpace(opt.BaseBranch),
	})
}

func (p *daemonProjectAdapter) RunTicketWorker(ctx context.Context, ticketID uint, opt daemonsvc.WorkerRunOptions) (daemonsvc.WorkerRunResult, error) {
	if p == nil || p.project == nil {
		return daemonsvc.WorkerRunResult{}, fmt.Errorf("daemon project 为空")
	}
	if err := p.requireExecutionBaseBranch(ctx, ticketID, opt.BaseBranch); err != nil {
		return daemonsvc.WorkerRunResult{}, err
	}
	res, err := p.project.RunTicketWorker(ctx, ticketID, pmsvc.WorkerRunOptions{
		EntryPrompt: strings.TrimSpace(opt.EntryPrompt),
		AutoStart:   opt.AutoStart,
		BaseBranch:  strings.TrimSpace(opt.BaseBranch),
	})
	if err != nil {
		return daemonsvc.WorkerRunResult{}, err
	}
	return daemonsvc.WorkerRunResult{
		TicketID: res.TicketID,
		WorkerID: res.WorkerID,
		RunID:    res.RunID,
	}, nil
}

func (p *daemonProjectAdapter) SubmitSubagentRun(ctx context.Context, opt daemonsvc.SubagentSubmitOptions) (daemonsvc.SubagentSubmission, error) {
	if p == nil || p.project == nil {
		return daemonsvc.SubagentSubmission{}, fmt.Errorf("daemon project 为空")
	}
	opt.RequestID = strings.TrimSpace(opt.RequestID)
	opt.Provider = strings.TrimSpace(opt.Provider)
	opt.Model = strings.TrimSpace(opt.Model)
	opt.Prompt = strings.TrimSpace(opt.Prompt)
	res, err := p.project.SubmitSubagentRun(ctx, opt)
	if err != nil {
		return daemonsvc.SubagentSubmission{}, err
	}
	res.RequestID = strings.TrimSpace(res.RequestID)
	res.Provider = strings.TrimSpace(res.Provider)
	res.Model = strings.TrimSpace(res.Model)
	res.RuntimeDir = strings.TrimSpace(res.RuntimeDir)
	return res, nil
}

func (p *daemonProjectAdapter) RunSubagentJob(ctx context.Context, taskRunID uint, opt daemonsvc.SubagentRunOptions) error {
	if p == nil || p.project == nil {
		return fmt.Errorf("daemon project 为空")
	}
	opt.RunnerID = strings.TrimSpace(opt.RunnerID)
	return p.project.RunSubagentJob(ctx, taskRunID, opt)
}

func (p *daemonProjectAdapter) FindLatestWorkerRun(ctx context.Context, ticketID uint, afterRunID uint) (*daemonsvc.RunStatus, error) {
	if p == nil || p.project == nil {
		return nil, fmt.Errorf("daemon project 为空")
	}
	status, err := p.project.FindLatestWorkerRun(ctx, ticketID, afterRunID)
	if err != nil {
		return nil, err
	}
	if status == nil {
		return nil, nil
	}
	return &daemonsvc.RunStatus{
		RunID:              status.RunID,
		Project:            strings.TrimSpace(p.project.Name()),
		OwnerType:          strings.TrimSpace(status.OwnerType),
		TaskType:           strings.TrimSpace(status.TaskType),
		TicketID:           status.TicketID,
		WorkerID:           status.WorkerID,
		OrchestrationState: strings.TrimSpace(status.OrchestrationState),
		RuntimeHealthState: strings.TrimSpace(status.RuntimeHealthState),
		RuntimeNeedsUser:   status.RuntimeNeedsUser,
		RuntimeSummary:     strings.TrimSpace(status.RuntimeSummary),
		SemanticNextAction: strings.TrimSpace(status.SemanticNextAction),
		SemanticSummary:    strings.TrimSpace(status.SemanticSummary),
		ErrorCode:          strings.TrimSpace(status.ErrorCode),
		ErrorMessage:       strings.TrimSpace(status.ErrorMessage),
		StartedAt:          status.StartedAt,
		FinishedAt:         status.FinishedAt,
		UpdatedAt:          TaskStatusUpdatedAt(*status),
	}, nil
}

func (p *daemonProjectAdapter) ListTicketViews(ctx context.Context) ([]daemonsvc.TicketView, error) {
	if p == nil || p.project == nil {
		return nil, fmt.Errorf("daemon project 为空")
	}
	return p.project.ListTicketViews(ctx)
}

func (p *daemonProjectAdapter) GetTicketViewByID(ctx context.Context, ticketID uint) (*daemonsvc.TicketView, error) {
	if p == nil || p.project == nil {
		return nil, fmt.Errorf("daemon project 为空")
	}
	if ticketID == 0 {
		return nil, fmt.Errorf("ticket_id 不能为空")
	}
	return p.project.GetTicketViewByID(ctx, ticketID)
}

func (p *daemonProjectAdapter) FocusStart(ctx context.Context, in contracts.FocusStartInput) (contracts.FocusStartResult, error) {
	if p == nil || p.project == nil {
		return contracts.FocusStartResult{}, fmt.Errorf("daemon project 为空")
	}
	return p.project.FocusStart(ctx, in)
}

func (p *daemonProjectAdapter) FocusGet(ctx context.Context, focusID uint) (contracts.FocusRunView, error) {
	if p == nil || p.project == nil {
		return contracts.FocusRunView{}, fmt.Errorf("daemon project 为空")
	}
	return p.project.FocusGet(ctx, focusID)
}

func (p *daemonProjectAdapter) FocusPoll(ctx context.Context, focusID, sinceEventID uint) (contracts.FocusPollResult, error) {
	if p == nil || p.project == nil {
		return contracts.FocusPollResult{}, fmt.Errorf("daemon project 为空")
	}
	return p.project.FocusPoll(ctx, focusID, sinceEventID)
}

func (p *daemonProjectAdapter) FocusStop(ctx context.Context, focusID uint, requestID string) error {
	if p == nil || p.project == nil {
		return fmt.Errorf("daemon project 为空")
	}
	return p.project.FocusStop(ctx, focusID, requestID)
}

func (p *daemonProjectAdapter) FocusCancel(ctx context.Context, focusID uint, requestID string) error {
	if p == nil || p.project == nil {
		return fmt.Errorf("daemon project 为空")
	}
	return p.project.FocusCancel(ctx, focusID, requestID)
}

func (p *daemonProjectAdapter) AddNote(ctx context.Context, rawText string) (daemonsvc.NoteAddResult, error) {
	if p == nil || p.project == nil {
		return daemonsvc.NoteAddResult{}, fmt.Errorf("daemon project 为空")
	}
	res, err := p.project.AddNote(ctx, strings.TrimSpace(rawText))
	if err != nil {
		return daemonsvc.NoteAddResult{}, err
	}
	return res, nil
}

func (p *daemonProjectAdapter) GetTaskStatus(ctx context.Context, runID uint) (*daemonsvc.RunStatus, error) {
	if p == nil || p.project == nil {
		return nil, fmt.Errorf("daemon project 为空")
	}
	status, err := p.project.GetTaskStatus(ctx, runID)
	if err != nil {
		return nil, err
	}
	if status == nil {
		return nil, nil
	}
	return &daemonsvc.RunStatus{
		RunID:              status.RunID,
		Project:            strings.TrimSpace(p.project.Name()),
		OwnerType:          strings.TrimSpace(status.OwnerType),
		TaskType:           strings.TrimSpace(status.TaskType),
		TicketID:           status.TicketID,
		WorkerID:           status.WorkerID,
		OrchestrationState: strings.TrimSpace(status.OrchestrationState),
		RuntimeHealthState: strings.TrimSpace(status.RuntimeHealthState),
		RuntimeNeedsUser:   status.RuntimeNeedsUser,
		RuntimeSummary:     strings.TrimSpace(status.RuntimeSummary),
		SemanticNextAction: strings.TrimSpace(status.SemanticNextAction),
		SemanticSummary:    strings.TrimSpace(status.SemanticSummary),
		ErrorCode:          strings.TrimSpace(status.ErrorCode),
		ErrorMessage:       strings.TrimSpace(status.ErrorMessage),
		StartedAt:          status.StartedAt,
		FinishedAt:         status.FinishedAt,
		UpdatedAt:          TaskStatusUpdatedAt(*status),
	}, nil
}

func (p *daemonProjectAdapter) ListTaskEvents(ctx context.Context, runID uint, limit int) ([]daemonsvc.RunEvent, error) {
	if p == nil || p.project == nil {
		return nil, fmt.Errorf("daemon project 为空")
	}
	events, err := p.project.ListTaskEvents(ctx, runID, limit)
	if err != nil {
		return nil, err
	}
	out := make([]daemonsvc.RunEvent, 0, len(events))
	for _, ev := range events {
		out = append(out, daemonsvc.RunEvent{
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

func (p *daemonProjectAdapter) CancelTaskRun(ctx context.Context, runID uint) (daemonsvc.TaskRunCancelResult, error) {
	return p.CancelTaskRunWithCause(ctx, runID, contracts.TaskCancelCauseUnknown)
}

func (p *daemonProjectAdapter) CancelTaskRunWithCause(ctx context.Context, runID uint, cause contracts.TaskCancelCause) (daemonsvc.TaskRunCancelResult, error) {
	if p == nil || p.project == nil {
		return daemonsvc.TaskRunCancelResult{}, fmt.Errorf("daemon project 为空")
	}
	result, err := p.project.CancelTaskRunWithCause(ctx, runID, cause)
	if err != nil {
		return daemonsvc.TaskRunCancelResult{}, err
	}
	return daemonsvc.TaskRunCancelResult{
		RunID:     result.RunID,
		Found:     result.Found,
		Canceled:  result.Canceled,
		Reason:    strings.TrimSpace(result.Reason),
		FromState: strings.TrimSpace(result.FromState),
		ToState:   strings.TrimSpace(result.ToState),
	}, nil
}

func (p *daemonProjectAdapter) TerminateTaskRun(ctx context.Context, runID uint, reason string) (daemonsvc.TaskRunTerminalResult, error) {
	if p == nil || p.project == nil || p.project.task == nil {
		return daemonsvc.TaskRunTerminalResult{}, fmt.Errorf("daemon project 为空")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if runID == 0 {
		return daemonsvc.TaskRunTerminalResult{}, fmt.Errorf("run_id 不能为空")
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = fmt.Sprintf("execution host terminated task run %d", runID)
	}

	status, err := p.project.task.GetStatusByRunID(ctx, runID)
	if err != nil {
		return daemonsvc.TaskRunTerminalResult{}, err
	}
	if status == nil {
		return daemonsvc.TaskRunTerminalResult{
			RunID:  runID,
			Found:  false,
			Reason: fmt.Sprintf("task run #%d 不存在", runID),
		}, nil
	}

	fromState := strings.TrimSpace(status.OrchestrationState)
	if fromState == "" {
		fromState = string(contracts.TaskRunning)
	}
	normalizedState := strings.TrimSpace(strings.ToLower(fromState))
	normalizedError := strings.TrimSpace(strings.ToLower(status.ErrorCode))
	if status.OwnerType != string(contracts.TaskOwnerWorker) || strings.TrimSpace(status.TaskType) != contracts.TaskTypeDeliverTicket {
		return daemonsvc.TaskRunTerminalResult{
			RunID:      runID,
			Found:      true,
			Terminated: false,
			Reason:     fmt.Sprintf("task run 不是 worker deliver run: owner=%s task_type=%s", strings.TrimSpace(status.OwnerType), strings.TrimSpace(status.TaskType)),
			FromState:  fromState,
			ToState:    fromState,
		}, nil
	}
	switch strings.TrimSpace(strings.ToLower(status.SemanticNextAction)) {
	case string(contracts.NextDone), string(contracts.NextWaitUser):
		return daemonsvc.TaskRunTerminalResult{
			RunID:      runID,
			Found:      true,
			Terminated: false,
			Reason:     fmt.Sprintf("task run 已有语义终态 next_action=%s", strings.TrimSpace(status.SemanticNextAction)),
			FromState:  fromState,
			ToState:    fromState,
		}, nil
	}
	switch normalizedState {
	case string(contracts.TaskSucceeded), string(contracts.TaskFailed):
		return daemonsvc.TaskRunTerminalResult{
			RunID:      runID,
			Found:      true,
			Terminated: false,
			Reason:     fmt.Sprintf("task run 已结束，当前状态=%s", fromState),
			FromState:  fromState,
			ToState:    fromState,
		}, nil
	case string(contracts.TaskCanceled):
		if normalizedError != "agent_canceled" {
			return daemonsvc.TaskRunTerminalResult{
				RunID:      runID,
				Found:      true,
				Terminated: false,
				Reason:     fmt.Sprintf("task run 已取消，error_code=%s", strings.TrimSpace(status.ErrorCode)),
				FromState:  fromState,
				ToState:    fromState,
			}, nil
		}
	}

	now := time.Now()
	if p.project.worker != nil && status.WorkerID != 0 {
		worker, werr := p.project.worker.WorkerByID(ctx, status.WorkerID)
		if werr != nil {
			return daemonsvc.TaskRunTerminalResult{}, werr
		}
		if worker != nil {
			if err := p.project.worker.MarkWorkerRuntimeNotAlive(ctx, *worker, now); err != nil {
				return daemonsvc.TaskRunTerminalResult{}, err
			}
		}
	}
	allowedStates := []contracts.TaskOrchestrationState{contracts.TaskPending, contracts.TaskRunning}
	if normalizedState == string(contracts.TaskCanceled) {
		allowedStates = append(allowedStates, contracts.TaskCanceled)
	}
	res := p.project.core.DB.WithContext(ctx).Model(&contracts.TaskRun{}).
		Where("id = ? AND orchestration_state IN ?", runID, allowedStates).
		Updates(map[string]any{
			"orchestration_state": contracts.TaskFailed,
			"error_code":          "worker_loop_terminated",
			"error_message":       reason,
			"runner_id":           "",
			"lease_expires_at":    nil,
			"finished_at":         &now,
		})
	if res.Error != nil {
		return daemonsvc.TaskRunTerminalResult{}, res.Error
	}
	if res.RowsAffected == 0 {
		return daemonsvc.TaskRunTerminalResult{
			RunID:      runID,
			Found:      true,
			Terminated: false,
			Reason:     "task run 未发生可覆盖的状态变更",
			FromState:  fromState,
			ToState:    fromState,
		}, nil
	}
	if err := p.project.task.AppendRuntimeSample(ctx, contracts.TaskRuntimeSampleInput{
		TaskRunID:  runID,
		State:      contracts.TaskHealthDead,
		NeedsUser:  false,
		Summary:    reason,
		Source:     "daemon.execution_host",
		ObservedAt: now,
		Metrics: map[string]any{
			"ticket_id": status.TicketID,
			"worker_id": status.WorkerID,
			"source":    "daemon.execution_host",
			"reason":    reason,
		},
	}); err != nil {
		return daemonsvc.TaskRunTerminalResult{}, err
	}
	if err := p.project.task.AppendEvent(ctx, contracts.TaskEventInput{
		TaskRunID: runID,
		EventType: "worker_loop_terminated",
		FromState: map[string]any{
			"orchestration_state": fromState,
		},
		ToState: map[string]any{
			"orchestration_state":  contracts.TaskFailed,
			"runtime_health_state": contracts.TaskHealthDead,
		},
		Note: reason,
		Payload: map[string]any{
			"source":           "daemon.execution_host",
			"failure_code":     "worker_loop_failed",
			"observation_kind": "unexpected_exit",
			"summary":          reason,
			"ticket_id":        status.TicketID,
			"worker_id":        status.WorkerID,
		},
		CreatedAt: now,
	}); err != nil {
		return daemonsvc.TaskRunTerminalResult{}, err
	}
	return daemonsvc.TaskRunTerminalResult{
		RunID:      runID,
		Found:      true,
		Terminated: true,
		EventType:  "worker_loop_terminated",
		Reason:     reason,
		FromState:  fromState,
		ToState:    string(contracts.TaskFailed),
	}, nil
}

func (p *daemonProjectAdapter) Dashboard(ctx context.Context) (daemonsvc.DashboardResult, error) {
	if p == nil || p.project == nil {
		return daemonsvc.DashboardResult{}, fmt.Errorf("daemon project 为空")
	}
	result, err := p.project.Dashboard(ctx)
	if err != nil {
		return daemonsvc.DashboardResult{}, err
	}
	return daemonsvc.DashboardResult{
		TicketCounts: cloneDashboardMap(result.TicketCounts),
		WorkerStats: daemonsvc.DashboardWorkerStats{
			Running:    result.WorkerStats.Running,
			MaxRunning: result.WorkerStats.MaxRunning,
			Blocked:    result.WorkerStats.Blocked,
		},
		MergeCounts: cloneDashboardMap(result.MergeCounts),
		InboxCounts: daemonsvc.DashboardInboxCounts{
			Open:     result.InboxCounts.Open,
			Snoozed:  result.InboxCounts.Snoozed,
			Blockers: result.InboxCounts.Blockers,
		},
	}, nil
}

func (p *daemonProjectAdapter) GetPMState(ctx context.Context) (contracts.PMState, error) {
	if p == nil || p.project == nil {
		return contracts.PMState{}, fmt.Errorf("daemon project 为空")
	}
	return p.project.GetPMState(ctx)
}

func (p *daemonProjectAdapter) ListMergeItems(ctx context.Context, opt daemonsvc.ListMergeItemsOptions) ([]contracts.MergeItem, error) {
	if p == nil || p.project == nil {
		return nil, fmt.Errorf("daemon project 为空")
	}
	items, err := p.project.ListMergeItems(ctx, ListMergeOptions{
		Status: opt.Status,
		Limit:  opt.Limit,
	})
	if err != nil {
		return nil, err
	}
	out := make([]contracts.MergeItem, len(items))
	copy(out, items)
	return out, nil
}

func (p *daemonProjectAdapter) ListInbox(ctx context.Context, opt daemonsvc.ListInboxOptions) ([]contracts.InboxItem, error) {
	if p == nil || p.project == nil {
		return nil, fmt.Errorf("daemon project 为空")
	}
	items, err := p.project.ListInbox(ctx, ListInboxOptions{
		Status: opt.Status,
		Limit:  opt.Limit,
	})
	if err != nil {
		return nil, err
	}
	out := make([]contracts.InboxItem, len(items))
	copy(out, items)
	return out, nil
}

func cloneDashboardMap(src map[string]int) map[string]int {
	if len(src) == 0 {
		return map[string]int{}
	}
	dst := make(map[string]int, len(src))
	for key, value := range src {
		dst[strings.TrimSpace(key)] = value
	}
	return dst
}
