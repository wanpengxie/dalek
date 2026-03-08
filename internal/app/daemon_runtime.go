package app

import (
	"context"
	"fmt"
	"strings"

	daemonsvc "dalek/internal/services/daemon"
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

func (p *daemonProjectAdapter) SubmitDispatchTicket(ctx context.Context, ticketID uint, opt daemonsvc.DispatchSubmitOptions) (daemonsvc.DispatchSubmission, error) {
	if p == nil || p.project == nil {
		return daemonsvc.DispatchSubmission{}, fmt.Errorf("daemon project 为空")
	}
	opt.RequestID = strings.TrimSpace(opt.RequestID)
	res, err := p.project.SubmitDispatchTicket(ctx, ticketID, opt)
	if err != nil {
		return daemonsvc.DispatchSubmission{}, err
	}
	res.RequestID = strings.TrimSpace(res.RequestID)
	return res, nil
}

func (p *daemonProjectAdapter) RunDispatchJob(ctx context.Context, jobID uint, opt daemonsvc.DispatchRunOptions) error {
	if p == nil || p.project == nil {
		return fmt.Errorf("daemon project 为空")
	}
	opt.RunnerID = strings.TrimSpace(opt.RunnerID)
	opt.EntryPrompt = strings.TrimSpace(opt.EntryPrompt)
	return p.project.RunDispatchJob(ctx, jobID, opt)
}

func (p *daemonProjectAdapter) DirectDispatchWorker(ctx context.Context, ticketID uint, opt daemonsvc.WorkerRunOptions) (daemonsvc.WorkerRunResult, error) {
	if p == nil || p.project == nil {
		return daemonsvc.WorkerRunResult{}, fmt.Errorf("daemon project 为空")
	}
	res, err := p.project.DirectDispatchWorker(ctx, ticketID, DirectDispatchOptions{
		EntryPrompt: strings.TrimSpace(opt.EntryPrompt),
	})
	if err != nil {
		return daemonsvc.WorkerRunResult{}, err
	}
	return daemonsvc.WorkerRunResult{
		TicketID: res.TicketID,
		WorkerID: res.WorkerID,
		RunID:    res.LastRunID,
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

func (p *daemonProjectAdapter) RunPlannerJob(ctx context.Context, taskRunID uint, opt daemonsvc.PlannerRunOptions) error {
	if p == nil || p.project == nil {
		return fmt.Errorf("daemon project 为空")
	}
	opt.RunnerID = strings.TrimSpace(opt.RunnerID)
	opt.Prompt = strings.TrimSpace(opt.Prompt)
	return p.project.RunPlannerJob(ctx, taskRunID, PlannerRunOptions{
		RunnerID: opt.RunnerID,
		Prompt:   opt.Prompt,
	})
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
