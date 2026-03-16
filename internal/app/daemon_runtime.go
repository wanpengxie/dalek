package app

import (
	"context"
	"fmt"
	"strings"
	"time"

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

func (r *daemonProjectResolver) OpenNodeProject(name string) (daemonsvc.InternalNodeProject, error) {
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

func (p *daemonProjectAdapter) RegisterNode(ctx context.Context, opt daemonsvc.NodeRegisterOptions) (daemonsvc.NodeRegistration, error) {
	if p == nil || p.project == nil {
		return daemonsvc.NodeRegistration{}, fmt.Errorf("daemon project 为空")
	}
	res, err := p.project.RegisterNode(ctx, RegisterNodeOptions{
		Name:                 strings.TrimSpace(opt.Name),
		Endpoint:             strings.TrimSpace(opt.Endpoint),
		AuthMode:             strings.TrimSpace(opt.AuthMode),
		Status:               strings.TrimSpace(opt.Status),
		Version:              strings.TrimSpace(opt.Version),
		ProtocolVersion:      strings.TrimSpace(opt.ProtocolVersion),
		RoleCapabilities:     append([]string(nil), opt.RoleCapabilities...),
		ProviderModes:        append([]string(nil), opt.ProviderModes...),
		DefaultProvider:      strings.TrimSpace(opt.DefaultProvider),
		ProviderCapabilities: copyStringAnyMap(opt.ProviderCapabilities),
		SessionAffinity:      strings.TrimSpace(opt.SessionAffinity),
		LastSeenAt:           opt.LastSeenAt,
	})
	if err != nil {
		return daemonsvc.NodeRegistration{}, err
	}
	return daemonsvc.NodeRegistration{
		ID:                   res.ID,
		Name:                 res.Name,
		Endpoint:             res.Endpoint,
		AuthMode:             res.AuthMode,
		Status:               res.Status,
		Version:              res.Version,
		ProtocolVersion:      res.ProtocolVersion,
		RoleCapabilities:     append([]string(nil), []string(res.RoleCapabilities)...),
		ProviderModes:        append([]string(nil), []string(res.ProviderModes)...),
		DefaultProvider:      res.DefaultProvider,
		ProviderCapabilities: copyStringAnyMap(map[string]any(res.ProviderCapabilities)),
		SessionAffinity:      res.SessionAffinity,
		SessionEpoch:         res.SessionEpoch,
		LastSeenAt:           res.LastSeenAt,
	}, nil
}

func (p *daemonProjectAdapter) BeginNodeSession(ctx context.Context, name string, observedAt *time.Time) (daemonsvc.NodeSessionLease, error) {
	if p == nil || p.project == nil {
		return daemonsvc.NodeSessionLease{}, fmt.Errorf("daemon project 为空")
	}
	res, err := p.project.BeginNodeSession(ctx, name, observedAt)
	if err != nil {
		return daemonsvc.NodeSessionLease{}, err
	}
	return daemonsvc.NodeSessionLease{
		Name:         res.Name,
		SessionEpoch: res.SessionEpoch,
		LastSeenAt:   res.LastSeenAt,
	}, nil
}

func (p *daemonProjectAdapter) HeartbeatNodeWithEpoch(ctx context.Context, name string, sessionEpoch int, observedAt *time.Time) error {
	if p == nil || p.project == nil {
		return fmt.Errorf("daemon project 为空")
	}
	return p.project.HeartbeatNodeWithEpoch(ctx, name, sessionEpoch, observedAt)
}

func (p *daemonProjectAdapter) SubmitRun(ctx context.Context, opt daemonsvc.NodeRunSubmitOptions) (daemonsvc.NodeRunSubmission, error) {
	if p == nil || p.project == nil {
		return daemonsvc.NodeRunSubmission{}, fmt.Errorf("daemon project 为空")
	}
	res, err := p.project.SubmitRun(ctx, SubmitRunOptions{
		RequestID:    strings.TrimSpace(opt.RequestID),
		TicketID:     opt.TicketID,
		VerifyTarget: strings.TrimSpace(opt.VerifyTarget),
		SnapshotID:   strings.TrimSpace(opt.SnapshotID),
		BaseCommit:   strings.TrimSpace(opt.BaseCommit),
	})
	if err != nil {
		return daemonsvc.NodeRunSubmission{}, err
	}
	return daemonsvc.NodeRunSubmission{
		Accepted:     res.Accepted,
		RunID:        res.RunID,
		TaskRunID:    res.TaskRunID,
		RequestID:    res.RequestID,
		RunStatus:    string(res.RunStatus),
		VerifyTarget: res.VerifyTarget,
		SnapshotID:   res.SnapshotID,
		BaseCommit:   res.BaseCommit,
	}, nil
}

func (p *daemonProjectAdapter) GetRun(ctx context.Context, runID uint) (*daemonsvc.NodeRunView, error) {
	if p == nil || p.project == nil {
		return nil, fmt.Errorf("daemon project 为空")
	}
	view, err := p.project.GetRun(ctx, runID)
	if err != nil {
		return nil, err
	}
	if view == nil {
		return nil, nil
	}
	out := &daemonsvc.NodeRunView{
		RunID:        view.RunID,
		TaskRunID:    view.TaskRunID,
		ProjectKey:   view.ProjectKey,
		RequestID:    view.RequestID,
		TicketID:     view.TicketID,
		WorkerID:     view.WorkerID,
		RunStatus:    string(view.RunStatus),
		VerifyTarget: view.VerifyTarget,
		SnapshotID:   view.SnapshotID,
		BaseCommit:   view.BaseCommit,
		CreatedAt:    view.CreatedAt,
		UpdatedAt:    view.UpdatedAt,
	}
	status, err := p.project.GetTaskStatus(ctx, runID)
	if err != nil {
		return nil, err
	}
	if status != nil {
		out.Summary = strings.TrimSpace(status.RuntimeSummary)
		if out.Summary == "" {
			out.Summary = strings.TrimSpace(status.SemanticSummary)
		}
		out.LastEventType = strings.TrimSpace(status.LastEventType)
		out.LastEventNote = strings.TrimSpace(status.LastEventNote)
	}
	out.LifecycleStage = inferNodeRunLifecycleStage(out.RunStatus, out.LastEventType)
	artifacts, err := p.ListRunArtifacts(ctx, runID)
	if err != nil {
		return nil, err
	}
	if artifacts.Found {
		out.ArtifactCount = len(artifacts.Artifacts)
	}
	return out, nil
}

func (p *daemonProjectAdapter) GetRunByRequestID(ctx context.Context, requestID string) (*daemonsvc.NodeRunView, error) {
	if p == nil || p.project == nil {
		return nil, fmt.Errorf("daemon project 为空")
	}
	view, err := p.project.GetRunByRequestID(ctx, requestID)
	if err != nil {
		return nil, err
	}
	if view == nil {
		return nil, nil
	}
	return p.GetRun(ctx, view.RunID)
}

func inferNodeRunLifecycleStage(runStatus, lastEventType string) string {
	status := strings.TrimSpace(runStatus)
	eventType := strings.TrimSpace(lastEventType)
	switch {
	case strings.HasPrefix(eventType, "run_snapshot_"):
		return "snapshot"
	case eventType == "run_preflight_accepted" || eventType == "run_bootstrap_accepted":
		return "prepare"
	case eventType == "run_verify_prepared":
		return "ready"
	case eventType == "run_verify_started":
		return "execute"
	case eventType == "run_verify_succeeded":
		return "completed"
	case eventType == "run_verify_failed":
		return "failed"
	case eventType == "run_canceled":
		return "canceled"
	}
	switch status {
	case "requested", "queued", "dispatching":
		return "queued"
	case "snapshot_preparing", "snapshot_ready":
		return "snapshot"
	case "env_preparing":
		return "prepare"
	case "ready_to_run":
		return "ready"
	case "running":
		return "execute"
	case "waiting_approval":
		return "approval"
	case "node_offline", "reconciling":
		return "recovery"
	case "succeeded":
		return "completed"
	case "failed", "timed_out":
		return "failed"
	case "canceling", "canceled":
		return "canceled"
	default:
		return ""
	}
}

func (p *daemonProjectAdapter) CancelRun(ctx context.Context, runID uint) (daemonsvc.NodeRunCancelResult, error) {
	if p == nil || p.project == nil {
		return daemonsvc.NodeRunCancelResult{}, fmt.Errorf("daemon project 为空")
	}
	res, err := p.project.CancelRun(ctx, runID)
	if err != nil {
		return daemonsvc.NodeRunCancelResult{}, err
	}
	return daemonsvc.NodeRunCancelResult{
		Found:    res.Found,
		Canceled: res.Canceled,
		Reason:   strings.TrimSpace(res.Reason),
	}, nil
}

func (p *daemonProjectAdapter) GetRunLogs(ctx context.Context, runID uint, lines int) (daemonsvc.NodeRunLogs, error) {
	if p == nil || p.project == nil {
		return daemonsvc.NodeRunLogs{}, fmt.Errorf("daemon project 为空")
	}
	view, err := p.project.GetRun(ctx, runID)
	if err != nil {
		return daemonsvc.NodeRunLogs{}, err
	}
	if view == nil {
		return daemonsvc.NodeRunLogs{Found: false, RunID: runID}, nil
	}
	events, err := p.project.ListTaskEvents(ctx, runID, lines)
	if err != nil {
		return daemonsvc.NodeRunLogs{}, err
	}
	if lines > 0 && len(events) > lines {
		events = events[len(events)-lines:]
	}
	parts := make([]string, 0, len(events))
	for _, ev := range events {
		parts = append(parts, formatTaskEventLogLine(ev.EventType, ev.Note, ev.CreatedAt))
	}
	return daemonsvc.NodeRunLogs{
		Found: true,
		RunID: runID,
		Tail:  strings.Join(parts, "\n"),
	}, nil
}

func (p *daemonProjectAdapter) ListRunArtifacts(ctx context.Context, runID uint) (daemonsvc.NodeRunArtifacts, error) {
	if p == nil || p.project == nil {
		return daemonsvc.NodeRunArtifacts{}, fmt.Errorf("daemon project 为空")
	}
	view, err := p.project.GetRun(ctx, runID)
	if err != nil {
		return daemonsvc.NodeRunArtifacts{}, err
	}
	if view == nil {
		return daemonsvc.NodeRunArtifacts{Found: false, RunID: runID}, nil
	}
	events, err := p.project.ListTaskEvents(ctx, runID, 50)
	if err != nil {
		return daemonsvc.NodeRunArtifacts{}, err
	}
	artifacts := buildRunArtifactsFromEvents(events)
	out := make([]daemonsvc.NodeRunArtifact, 0, len(artifacts))
	for _, art := range artifacts {
		out = append(out, daemonsvc.NodeRunArtifact{
			Name: strings.TrimSpace(art.Name),
			Kind: strings.TrimSpace(art.Kind),
			Size: art.Size,
			Ref:  strings.TrimSpace(art.Ref),
		})
	}
	issues := buildRunArtifactIssuesFromEvents(events)
	outIssues := make([]daemonsvc.NodeRunArtifactIssue, 0, len(issues))
	for _, issue := range issues {
		outIssues = append(outIssues, daemonsvc.NodeRunArtifactIssue{
			Name:   strings.TrimSpace(issue.Name),
			Status: strings.TrimSpace(issue.Status),
			Reason: strings.TrimSpace(issue.Reason),
		})
	}
	return daemonsvc.NodeRunArtifacts{
		Found:     true,
		RunID:     runID,
		Artifacts: out,
		Issues:    outIssues,
	}, nil
}

func (p *daemonProjectAdapter) UploadSnapshot(ctx context.Context, opt daemonsvc.NodeSnapshotUploadOptions) (daemonsvc.NodeSnapshotUploadResult, error) {
	if p == nil || p.project == nil {
		return daemonsvc.NodeSnapshotUploadResult{}, fmt.Errorf("daemon project 为空")
	}
	res, err := p.project.UploadSnapshotManifest(ctx, SnapshotUploadOptions{
		SnapshotID:          strings.TrimSpace(opt.SnapshotID),
		NodeName:            strings.TrimSpace(opt.NodeName),
		BaseCommit:          strings.TrimSpace(opt.BaseCommit),
		WorkspaceGeneration: strings.TrimSpace(opt.WorkspaceGeneration),
		ManifestJSON:        strings.TrimSpace(opt.ManifestJSON),
		ExpiresAt:           opt.ExpiresAt,
	})
	if err != nil {
		return daemonsvc.NodeSnapshotUploadResult{}, err
	}
	return daemonsvc.NodeSnapshotUploadResult{
		SnapshotID:          res.SnapshotID,
		Status:              res.Status,
		ManifestDigest:      res.ManifestDigest,
		ManifestJSON:        res.ManifestJSON,
		ArtifactPath:        res.ArtifactPath,
		BaseCommit:          res.BaseCommit,
		WorkspaceGeneration: res.WorkspaceGeneration,
	}, nil
}

func (p *daemonProjectAdapter) UploadSnapshotChunk(ctx context.Context, opt daemonsvc.NodeSnapshotChunkUploadOptions) (daemonsvc.NodeSnapshotChunkUploadResult, error) {
	if p == nil || p.project == nil {
		return daemonsvc.NodeSnapshotChunkUploadResult{}, fmt.Errorf("daemon project 为空")
	}
	res, err := p.project.UploadSnapshotManifestChunk(ctx, SnapshotUploadChunkOptions{
		SnapshotID:          strings.TrimSpace(opt.SnapshotID),
		NodeName:            strings.TrimSpace(opt.NodeName),
		BaseCommit:          strings.TrimSpace(opt.BaseCommit),
		WorkspaceGeneration: strings.TrimSpace(opt.WorkspaceGeneration),
		ChunkIndex:          opt.ChunkIndex,
		ChunkData:           opt.ChunkData,
		IsFinal:             opt.IsFinal,
		ExpiresAt:           opt.ExpiresAt,
	})
	if err != nil {
		return daemonsvc.NodeSnapshotChunkUploadResult{}, err
	}
	return daemonsvc.NodeSnapshotChunkUploadResult{
		Accepted:            res.Accepted,
		SnapshotID:          res.SnapshotID,
		Status:              res.Status,
		NextIndex:           res.NextIndex,
		ManifestDigest:      res.ManifestDigest,
		ManifestJSON:        res.ManifestJSON,
		ArtifactPath:        res.ArtifactPath,
		BaseCommit:          res.BaseCommit,
		WorkspaceGeneration: res.WorkspaceGeneration,
	}, nil
}

func (p *daemonProjectAdapter) DownloadSnapshot(ctx context.Context, snapshotID string) (daemonsvc.NodeSnapshotDownloadResult, error) {
	if p == nil || p.project == nil {
		return daemonsvc.NodeSnapshotDownloadResult{}, fmt.Errorf("daemon project 为空")
	}
	res, err := p.project.DownloadSnapshotManifest(ctx, strings.TrimSpace(snapshotID))
	if err != nil {
		return daemonsvc.NodeSnapshotDownloadResult{}, err
	}
	return daemonsvc.NodeSnapshotDownloadResult{
		Found:               res.Found,
		SnapshotID:          res.SnapshotID,
		Status:              res.Status,
		ManifestDigest:      res.ManifestDigest,
		ManifestJSON:        res.ManifestJSON,
		ArtifactPath:        res.ArtifactPath,
		BaseCommit:          res.BaseCommit,
		WorkspaceGeneration: res.WorkspaceGeneration,
	}, nil
}

func copyStringAnyMap(in map[string]any) map[string]any {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		key := strings.TrimSpace(k)
		if key == "" {
			continue
		}
		out[key] = v
	}
	if len(out) == 0 {
		return nil
	}
	return out
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
