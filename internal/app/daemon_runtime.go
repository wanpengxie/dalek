package app

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	daemonsvc "dalek/internal/services/daemon"
)

type daemonProjectResolverCacheEntry struct {
	project   *daemonProjectAdapter
	expiresAt time.Time
}

type daemonProjectResolver struct {
	home *Home
	ttl  time.Duration

	mu    sync.Mutex
	cache map[string]*daemonProjectResolverCacheEntry
}

func newDaemonProjectResolver(home *Home) *daemonProjectResolver {
	return &daemonProjectResolver{
		home:  home,
		ttl:   defaultResolverCacheTTL,
		cache: map[string]*daemonProjectResolverCacheEntry{},
	}
}

func (r *daemonProjectResolver) OpenProject(name string) (daemonsvc.ExecutionHostProject, error) {
	if r == nil || r.home == nil {
		return nil, fmt.Errorf("daemon project resolver 未初始化")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("project 不能为空")
	}
	now := time.Now()

	r.mu.Lock()
	if cached := r.cache[name]; cached != nil && cached.project != nil && now.Before(cached.expiresAt) {
		r.mu.Unlock()
		return cached.project, nil
	}
	r.mu.Unlock()

	p, err := r.home.OpenProjectByName(name)
	if err != nil {
		return nil, err
	}
	adapter := &daemonProjectAdapter{project: p}
	ttl := r.ttl
	if ttl <= 0 {
		ttl = defaultResolverCacheTTL
	}

	r.mu.Lock()
	r.cache[name] = &daemonProjectResolverCacheEntry{
		project:   adapter,
		expiresAt: time.Now().Add(ttl),
	}
	r.mu.Unlock()
	return adapter, nil
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
	res, err := p.project.SubmitDispatchTicket(ctx, ticketID, DispatchSubmitOptions{
		RequestID: strings.TrimSpace(opt.RequestID),
		AutoStart: opt.AutoStart,
	})
	if err != nil {
		return daemonsvc.DispatchSubmission{}, err
	}
	return daemonsvc.DispatchSubmission{
		JobID:      res.JobID,
		TaskRunID:  res.TaskRunID,
		RequestID:  strings.TrimSpace(res.RequestID),
		TicketID:   res.TicketID,
		WorkerID:   res.WorkerID,
		JobStatus:  strings.TrimSpace(res.JobStatus),
		Dispatched: res.Dispatched,
	}, nil
}

func (p *daemonProjectAdapter) RunDispatchJob(ctx context.Context, jobID uint, opt daemonsvc.DispatchRunOptions) error {
	if p == nil || p.project == nil {
		return fmt.Errorf("daemon project 为空")
	}
	return p.project.RunDispatchJob(ctx, jobID, DispatchRunOptions{
		RunnerID:    strings.TrimSpace(opt.RunnerID),
		EntryPrompt: strings.TrimSpace(opt.EntryPrompt),
	})
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
	}, nil
}

func (p *daemonProjectAdapter) SubmitSubagentRun(ctx context.Context, opt daemonsvc.SubagentSubmitOptions) (daemonsvc.SubagentSubmission, error) {
	if p == nil || p.project == nil {
		return daemonsvc.SubagentSubmission{}, fmt.Errorf("daemon project 为空")
	}
	res, err := p.project.SubmitSubagentRun(ctx, SubagentSubmitOptions{
		RequestID: strings.TrimSpace(opt.RequestID),
		Provider:  strings.TrimSpace(opt.Provider),
		Model:     strings.TrimSpace(opt.Model),
		Prompt:    strings.TrimSpace(opt.Prompt),
	})
	if err != nil {
		return daemonsvc.SubagentSubmission{}, err
	}
	return daemonsvc.SubagentSubmission{
		TaskRunID:  res.TaskRunID,
		RequestID:  strings.TrimSpace(res.RequestID),
		Provider:   strings.TrimSpace(res.Provider),
		Model:      strings.TrimSpace(res.Model),
		RuntimeDir: strings.TrimSpace(res.RuntimeDir),
		Accepted:   res.Accepted,
	}, nil
}

func (p *daemonProjectAdapter) RunSubagentJob(ctx context.Context, taskRunID uint, opt daemonsvc.SubagentRunOptions) error {
	if p == nil || p.project == nil {
		return fmt.Errorf("daemon project 为空")
	}
	return p.project.RunSubagentJob(ctx, taskRunID, SubagentRunOptions{
		RunnerID: strings.TrimSpace(opt.RunnerID),
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
		UpdatedAt:          daemonTaskStatusUpdatedAt(*status),
	}, nil
}

func (p *daemonProjectAdapter) AddNote(ctx context.Context, rawText string) (daemonsvc.NoteAddResult, error) {
	if p == nil || p.project == nil {
		return daemonsvc.NoteAddResult{}, fmt.Errorf("daemon project 为空")
	}
	res, err := p.project.AddNote(ctx, strings.TrimSpace(rawText))
	if err != nil {
		return daemonsvc.NoteAddResult{}, err
	}
	return daemonsvc.NoteAddResult{
		NoteID:       res.NoteID,
		ShapedItemID: res.ShapedItemID,
		Deduped:      res.Deduped,
	}, nil
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
		UpdatedAt:          daemonTaskStatusUpdatedAt(*status),
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

func daemonTaskStatusUpdatedAt(status TaskStatus) time.Time {
	latest := status.UpdatedAt
	for _, v := range []*time.Time{status.RuntimeObservedAt, status.SemanticReportedAt, status.LastEventAt} {
		if v != nil && v.After(latest) {
			latest = *v
		}
	}
	return latest
}
