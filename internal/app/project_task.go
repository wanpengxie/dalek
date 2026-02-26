package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"dalek/internal/contracts"
	tasksvc "dalek/internal/services/task"
	"dalek/internal/store"
)

func (p *Project) ListTaskStatus(ctx context.Context, opt ListTaskOptions) ([]TaskStatus, error) {
	if p == nil || p.task == nil {
		return nil, fmt.Errorf("project task service 为空")
	}
	views, err := p.task.ListStatus(ctx, contracts.TaskListStatusOptions{
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
		return p.task.AppendEvent(ctx, contracts.TaskEventInput{
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
	return p.task.AppendEvent(ctx, contracts.TaskEventInput{
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
	if err := p.task.AppendEvent(ctx, contracts.TaskEventInput{
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
