package app

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

func (p *Project) SubmitRun(ctx context.Context, opt SubmitRunOptions) (RunSubmission, error) {
	if p == nil || p.run == nil {
		return RunSubmission{}, fmt.Errorf("project run service 为空")
	}
	opt.ProjectKey = strings.TrimSpace(p.Key())
	return p.run.Submit(ctx, opt)
}

func (p *Project) GetRun(ctx context.Context, runID uint) (*RunView, error) {
	if p == nil || p.run == nil {
		return nil, fmt.Errorf("project run service 为空")
	}
	if err := p.reconcileRemoteRunIfNeeded(ctx, runID); err != nil {
		return nil, err
	}
	return p.run.Get(ctx, runID)
}

func (p *Project) GetRunByRequestID(ctx context.Context, requestID string) (*RunView, error) {
	if p == nil || p.run == nil {
		return nil, fmt.Errorf("project run service 为空")
	}
	view, err := p.run.GetByRequestID(ctx, strings.TrimSpace(requestID))
	if err != nil || view == nil {
		return view, err
	}
	if rerr := p.reconcileRemoteRunIfNeeded(ctx, view.RunID); rerr != nil {
		return nil, rerr
	}
	return p.run.GetByRequestID(ctx, strings.TrimSpace(requestID))
}

func (p *Project) ListRuns(ctx context.Context, limit int) ([]RunView, error) {
	if p == nil || p.run == nil {
		return nil, fmt.Errorf("project run service 为空")
	}
	items, err := p.run.List(ctx, limit)
	if err != nil {
		return nil, err
	}
	out := make([]RunView, 0, len(items))
	for _, item := range items {
		view, err := p.run.Get(ctx, item.RunID)
		if err != nil {
			return nil, err
		}
		if view != nil {
			out = append(out, *view)
		}
	}
	return out, nil
}

func (p *Project) CancelRun(ctx context.Context, runID uint) (TaskCancelResult, error) {
	if p == nil || p.run == nil {
		return TaskCancelResult{}, fmt.Errorf("project run service 为空")
	}
	_ = p.cancelRemoteRunIfNeeded(ctx, runID)
	return p.run.Cancel(ctx, runID)
}

func (p *Project) GetRunLogs(ctx context.Context, runID uint, lines int) (RunLogs, error) {
	if p == nil || p.run == nil {
		return RunLogs{}, fmt.Errorf("project run service 为空")
	}
	view, err := p.GetRun(ctx, runID)
	if err != nil {
		return RunLogs{}, err
	}
	if view == nil {
		return RunLogs{Found: false, RunID: runID}, nil
	}
	if payload, ok := p.lookupRemoteRunPayload(ctx, runID); ok {
		remote, err := NewDaemonRemoteProjectFromBaseURL(payload.RemoteBaseURL, payload.RemoteProject)
		if err != nil {
			return RunLogs{}, err
		}
		logs, err := remote.GetRunLogs(ctx, payload.RemoteRunID, lines)
		if err != nil {
			return RunLogs{}, err
		}
		return RunLogs{Found: logs.Found, RunID: runID, Tail: strings.TrimSpace(logs.Tail)}, nil
	}
	events, err := p.ListTaskEvents(ctx, runID, lines)
	if err != nil {
		return RunLogs{}, err
	}
	if lines > 0 && len(events) > lines {
		events = events[len(events)-lines:]
	}
	parts := make([]string, 0, len(events))
	for _, ev := range events {
		parts = append(parts, formatTaskEventLogLine(ev.EventType, ev.Note, ev.CreatedAt))
	}
	return RunLogs{
		Found: true,
		RunID: runID,
		Tail:  strings.Join(parts, "\n"),
	}, nil
}

func (p *Project) ListRunArtifacts(ctx context.Context, runID uint) (RunArtifacts, error) {
	if p == nil || p.run == nil {
		return RunArtifacts{}, fmt.Errorf("project run service 为空")
	}
	view, err := p.GetRun(ctx, runID)
	if err != nil {
		return RunArtifacts{}, err
	}
	if view == nil {
		return RunArtifacts{Found: false, RunID: runID}, nil
	}
	if payload, ok := p.lookupRemoteRunPayload(ctx, runID); ok {
		remote, err := NewDaemonRemoteProjectFromBaseURL(payload.RemoteBaseURL, payload.RemoteProject)
		if err != nil {
			return RunArtifacts{}, err
		}
		artifacts, err := remote.GetRunArtifacts(ctx, payload.RemoteRunID)
		if err != nil {
			return RunArtifacts{}, err
		}
		out := make([]RunArtifact, 0, len(artifacts.Artifacts))
		for _, item := range artifacts.Artifacts {
			out = append(out, RunArtifact{Name: item.Name, Kind: item.Kind, Size: item.Size, Ref: item.Ref})
		}
		issues := make([]RunArtifactIssue, 0, len(artifacts.Issues))
		for _, item := range artifacts.Issues {
			issues = append(issues, RunArtifactIssue{Name: item.Name, Status: item.Status, Reason: item.Reason})
		}
		return RunArtifacts{Found: artifacts.Found, RunID: runID, Artifacts: out, Issues: issues}, nil
	}
	events, err := p.ListTaskEvents(ctx, runID, 50)
	if err != nil {
		return RunArtifacts{}, err
	}
	return RunArtifacts{
		Found:     true,
		RunID:     runID,
		Artifacts: buildRunArtifactsFromEvents(events),
		Issues:    buildRunArtifactIssuesFromEvents(events),
	}, nil
}

func (p *Project) lookupRemoteRunPayload(ctx context.Context, runID uint) (remoteTaskProxyPayload, bool) {
	if p == nil || p.task == nil || runID == 0 {
		return remoteTaskProxyPayload{}, false
	}
	record, err := p.task.FindRunByID(ctx, runID)
	if err != nil || record == nil {
		return remoteTaskProxyPayload{}, false
	}
	payload, ok := parseRemoteTaskProxyPayload(record.RequestPayloadJSON)
	if !ok || payload.Role != string(TaskRequestRoleRun) || payload.RemoteRunID == 0 || strings.TrimSpace(payload.RemoteBaseURL) == "" {
		return remoteTaskProxyPayload{}, false
	}
	return payload, true
}

func buildRunArtifactsFromEvents(events []TaskEvent) []RunArtifact {
	out := make([]RunArtifact, 0, 2)
	seen := map[string]struct{}{}
	for _, ev := range events {
		if strings.TrimSpace(ev.EventType) != "run_snapshot_apply_accepted" {
			continue
		}
		if strings.TrimSpace(ev.PayloadJSON) == "" {
			continue
		}
		var payload map[string]any
		if err := json.Unmarshal([]byte(ev.PayloadJSON), &payload); err != nil {
			continue
		}
		ref := strings.TrimSpace(fmt.Sprint(payload["plan_path"]))
		if ref == "" {
			continue
		}
		if _, ok := seen[ref]; ok {
			continue
		}
		seen[ref] = struct{}{}
		out = append(out, RunArtifact{
			Name: "apply-plan.json",
			Kind: "plan",
			Ref:  ref,
		})
	}
	return out
}

func buildRunArtifactIssuesFromEvents(events []TaskEvent) []RunArtifactIssue {
	out := make([]RunArtifactIssue, 0, 2)
	for _, ev := range events {
		if strings.TrimSpace(ev.EventType) != "run_artifact_upload_failed" {
			continue
		}
		var payload map[string]any
		if strings.TrimSpace(ev.PayloadJSON) != "" {
			_ = json.Unmarshal([]byte(ev.PayloadJSON), &payload)
		}
		name := strings.TrimSpace(fmt.Sprint(payload["artifact_name"]))
		reason := strings.TrimSpace(fmt.Sprint(payload["reason"]))
		if reason == "" {
			reason = strings.TrimSpace(ev.Note)
		}
		out = append(out, RunArtifactIssue{
			Name:   name,
			Status: "upload_failed",
			Reason: reason,
		})
	}
	return out
}

func formatTaskEventLogLine(eventType, note string, createdAt time.Time) string {
	line := strings.TrimSpace(eventType)
	if trimmed := strings.TrimSpace(note); trimmed != "" {
		line += " " + trimmed
	}
	if !createdAt.IsZero() {
		line = createdAt.Format(time.RFC3339) + " " + line
	}
	return strings.TrimSpace(line)
}
