package app

import (
	"context"
	"fmt"
	"strings"
)

// RemoteProject 抽象远程 daemon 访问的项目 facade。
// 先提供最小接口，后续再扩展成和 LocalProject 对齐的完整能力集。
type RemoteProject interface {
	Project() string
	Health(ctx context.Context) error
	SubmitDispatch(ctx context.Context, req DaemonDispatchSubmitRequest) (DaemonDispatchSubmitReceipt, error)
	SubmitWorkerRun(ctx context.Context, req DaemonWorkerRunSubmitRequest) (DaemonWorkerRunSubmitReceipt, error)
	SubmitRun(ctx context.Context, req DaemonRunSubmitRequest) (DaemonRunSubmitReceipt, error)
	SubmitSubagentRun(ctx context.Context, req DaemonSubagentSubmitRequest) (DaemonSubagentSubmitReceipt, error)
	SubmitNote(ctx context.Context, req DaemonNoteSubmitRequest) (DaemonNoteSubmitReceipt, error)
	SendProjectText(ctx context.Context, req DaemonGatewaySendRequest) (DaemonGatewaySendResponse, error)
	CancelRun(ctx context.Context, runID uint) (DaemonRunCancelResult, error)
	GetRun(ctx context.Context, runID uint) (*DaemonRunStatus, error)
	ListRunEvents(ctx context.Context, runID uint, limit int) ([]DaemonRunEvent, error)
	GetRunLogs(ctx context.Context, runID uint, lines int) (DaemonRunLogs, error)
	GetRunArtifacts(ctx context.Context, runID uint) (DaemonRunArtifacts, error)
}

type DaemonRemoteProject struct {
	project string
	client  *DaemonAPIClient
}

func NewDaemonRemoteProject(client *DaemonAPIClient, project string) (*DaemonRemoteProject, error) {
	if client == nil {
		return nil, fmt.Errorf("daemon client 为空")
	}
	project = strings.TrimSpace(project)
	if project == "" {
		return nil, fmt.Errorf("project 不能为空")
	}
	return &DaemonRemoteProject{project: project, client: client}, nil
}

func NewDaemonRemoteProjectFromHome(h *Home, project string) (*DaemonRemoteProject, error) {
	client, err := NewDaemonAPIClientFromHome(h)
	if err != nil {
		return nil, err
	}
	return NewDaemonRemoteProject(client, project)
}

func NewDaemonRemoteProjectFromBaseURL(baseURL, project string) (*DaemonRemoteProject, error) {
	client, err := NewDaemonAPIClient(DaemonAPIClientConfig{BaseURL: strings.TrimSpace(baseURL)})
	if err != nil {
		return nil, err
	}
	return NewDaemonRemoteProject(client, project)
}

func (r *DaemonRemoteProject) Project() string {
	if r == nil {
		return ""
	}
	return strings.TrimSpace(r.project)
}

func (r *DaemonRemoteProject) Health(ctx context.Context) error {
	if r == nil || r.client == nil {
		return fmt.Errorf("remote project 未初始化")
	}
	return r.client.Health(ctx)
}

func (r *DaemonRemoteProject) SubmitDispatch(ctx context.Context, req DaemonDispatchSubmitRequest) (DaemonDispatchSubmitReceipt, error) {
	if r == nil || r.client == nil {
		return DaemonDispatchSubmitReceipt{}, fmt.Errorf("remote project 未初始化")
	}
	req.Project = r.Project()
	return r.client.SubmitDispatch(ctx, req)
}

func (r *DaemonRemoteProject) SubmitWorkerRun(ctx context.Context, req DaemonWorkerRunSubmitRequest) (DaemonWorkerRunSubmitReceipt, error) {
	if r == nil || r.client == nil {
		return DaemonWorkerRunSubmitReceipt{}, fmt.Errorf("remote project 未初始化")
	}
	req.Project = r.Project()
	return r.client.SubmitWorkerRun(ctx, req)
}

func (r *DaemonRemoteProject) SubmitRun(ctx context.Context, req DaemonRunSubmitRequest) (DaemonRunSubmitReceipt, error) {
	if r == nil || r.client == nil {
		return DaemonRunSubmitReceipt{}, fmt.Errorf("remote project 未初始化")
	}
	req.Project = r.Project()
	return r.client.SubmitRun(ctx, req)
}

func (r *DaemonRemoteProject) SubmitSubagentRun(ctx context.Context, req DaemonSubagentSubmitRequest) (DaemonSubagentSubmitReceipt, error) {
	if r == nil || r.client == nil {
		return DaemonSubagentSubmitReceipt{}, fmt.Errorf("remote project 未初始化")
	}
	req.Project = r.Project()
	return r.client.SubmitSubagentRun(ctx, req)
}

func (r *DaemonRemoteProject) SubmitNote(ctx context.Context, req DaemonNoteSubmitRequest) (DaemonNoteSubmitReceipt, error) {
	if r == nil || r.client == nil {
		return DaemonNoteSubmitReceipt{}, fmt.Errorf("remote project 未初始化")
	}
	req.Project = r.Project()
	return r.client.SubmitNote(ctx, req)
}

func (r *DaemonRemoteProject) SendProjectText(ctx context.Context, req DaemonGatewaySendRequest) (DaemonGatewaySendResponse, error) {
	if r == nil || r.client == nil {
		return DaemonGatewaySendResponse{}, fmt.Errorf("remote project 未初始化")
	}
	req.Project = r.Project()
	return r.client.SendProjectText(ctx, req)
}

func (r *DaemonRemoteProject) CancelRun(ctx context.Context, runID uint) (DaemonRunCancelResult, error) {
	if r == nil || r.client == nil {
		return DaemonRunCancelResult{}, fmt.Errorf("remote project 未初始化")
	}
	return r.client.CancelRun(ctx, runID)
}

func (r *DaemonRemoteProject) GetRun(ctx context.Context, runID uint) (*DaemonRunStatus, error) {
	if r == nil || r.client == nil {
		return nil, fmt.Errorf("remote project 未初始化")
	}
	return r.client.GetRun(ctx, runID)
}

func (r *DaemonRemoteProject) ListRunEvents(ctx context.Context, runID uint, limit int) ([]DaemonRunEvent, error) {
	if r == nil || r.client == nil {
		return nil, fmt.Errorf("remote project 未初始化")
	}
	return r.client.ListRunEvents(ctx, runID, limit)
}

func (r *DaemonRemoteProject) GetRunLogs(ctx context.Context, runID uint, lines int) (DaemonRunLogs, error) {
	if r == nil || r.client == nil {
		return DaemonRunLogs{}, fmt.Errorf("remote project 未初始化")
	}
	return r.client.GetRunLogs(ctx, runID, lines)
}

func (r *DaemonRemoteProject) GetRunArtifacts(ctx context.Context, runID uint) (DaemonRunArtifacts, error) {
	if r == nil || r.client == nil {
		return DaemonRunArtifacts{}, fmt.Errorf("remote project 未初始化")
	}
	return r.client.GetRunArtifacts(ctx, runID)
}
