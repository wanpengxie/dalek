package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"dalek/internal/contracts"
	tasksvc "dalek/internal/services/task"
)

func (p *Project) ListTaskStatus(ctx context.Context, opt ListTaskOptions) ([]TaskStatus, error) {
	if p == nil || p.task == nil {
		return nil, fmt.Errorf("project task service 为空")
	}
	return p.task.ListStatus(ctx, contracts.TaskListStatusOptions{
		OwnerType:       opt.OwnerType,
		TaskType:        strings.TrimSpace(opt.TaskType),
		TicketID:        opt.TicketID,
		WorkerID:        opt.WorkerID,
		IncludeTerminal: opt.IncludeTerminal,
		Limit:           opt.Limit,
	})
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
	return v, nil
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
			FromStateJSON: ev.FromStateJSON.String(),
			ToStateJSON:   ev.ToStateJSON.String(),
			Note:          strings.TrimSpace(ev.Note),
			PayloadJSON:   ev.PayloadJSON.String(),
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
			FromStateJSON: ev.FromStateJSON.String(),
			ToStateJSON:   ev.ToStateJSON.String(),
			Note:          strings.TrimSpace(ev.Note),
			PayloadJSON:   ev.PayloadJSON.String(),
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
	return rec, nil
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
	return rec, nil
}

func (p *Project) ListSubagentRuns(ctx context.Context, limit int) ([]SubagentRun, error) {
	if p == nil || p.task == nil {
		return nil, fmt.Errorf("project task service 为空")
	}
	rows, err := p.task.ListSubagentRuns(ctx, strings.TrimSpace(p.Key()), limit)
	if err != nil {
		return nil, err
	}
	return rows, nil
}

func (p *Project) FinishAgentRun(ctx context.Context, runID uint, exitCode int) error {
	if p == nil || p.task == nil {
		return fmt.Errorf("project task service 为空")
	}
	return p.task.FinishAgentRun(ctx, runID, exitCode, time.Now())
}

func (p *Project) CancelTaskRun(ctx context.Context, runID uint) (TaskCancelResult, error) {
	return p.CancelTaskRunWithCause(ctx, runID, contracts.TaskCancelCauseUnknown)
}

func (p *Project) CancelTaskRunWithCause(ctx context.Context, runID uint, cause contracts.TaskCancelCause) (TaskCancelResult, error) {
	if p == nil || p.task == nil {
		return TaskCancelResult{}, fmt.Errorf("project task service 为空")
	}
	res, err := p.task.CancelRunWithCause(ctx, runID, cause, time.Now())
	if err != nil {
		return TaskCancelResult{}, err
	}
	return res, nil
}
