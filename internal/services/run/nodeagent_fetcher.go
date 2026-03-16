package run

import (
	"context"
	"fmt"
	"strings"

	nodeagentsvc "dalek/internal/services/nodeagent"
)

type NodeAgentRunFetcher struct {
	client          *nodeagentsvc.Client
	projectKey      string
	protocolVersion string
}

func NewNodeAgentRunFetcher(client *nodeagentsvc.Client, projectKey, protocolVersion string) *NodeAgentRunFetcher {
	return &NodeAgentRunFetcher{
		client:          client,
		projectKey:      strings.TrimSpace(projectKey),
		protocolVersion: strings.TrimSpace(protocolVersion),
	}
}

func (f *NodeAgentRunFetcher) FetchRun(ctx context.Context, runID uint) (RemoteRunStatus, error) {
	if f == nil || f.client == nil {
		return RemoteRunStatus{}, fmt.Errorf("node agent run fetcher 未初始化")
	}
	if runID == 0 {
		return RemoteRunStatus{}, fmt.Errorf("run_id 不能为空")
	}
	resp, err := f.client.QueryRun(ctx, nodeagentsvc.RunQueryRequest{
		Meta: nodeagentsvc.RequestMeta{
			ProjectKey:      strings.TrimSpace(f.projectKey),
			RunID:           runID,
			ProtocolVersion: strings.TrimSpace(f.protocolVersion),
		},
	})
	if err != nil {
		return RemoteRunStatus{}, err
	}
	return RemoteRunStatus{
		Found:         resp.Found,
		RunID:         resp.RunID,
		RequestID:     "",
		Status:        strings.TrimSpace(resp.Status),
		Summary:       strings.TrimSpace(resp.Summary),
		SnapshotID:    strings.TrimSpace(resp.SnapshotID),
		BaseCommit:    "",
		VerifyTarget:  strings.TrimSpace(resp.VerifyTarget),
		LastEventType: strings.TrimSpace(resp.LastEventType),
		LastEventNote: strings.TrimSpace(resp.LastEventNote),
		UpdatedAt:     resp.UpdatedAt,
	}, nil
}

func (f *NodeAgentRunFetcher) FetchRunByRequestID(ctx context.Context, requestID string) (RemoteRunStatus, error) {
	requestID = strings.TrimSpace(requestID)
	if requestID == "" {
		return RemoteRunStatus{}, fmt.Errorf("request_id 不能为空")
	}
	if f == nil || f.client == nil {
		return RemoteRunStatus{}, fmt.Errorf("node agent run fetcher 未初始化")
	}
	resp, err := f.client.QueryRun(ctx, nodeagentsvc.RunQueryRequest{
		Meta: nodeagentsvc.RequestMeta{
			RequestID:       requestID,
			ProjectKey:      strings.TrimSpace(f.projectKey),
			ProtocolVersion: strings.TrimSpace(f.protocolVersion),
		},
	})
	if err != nil {
		return RemoteRunStatus{}, err
	}
	return RemoteRunStatus{
		Found:         resp.Found,
		RunID:         resp.RunID,
		RequestID:     requestID,
		Status:        strings.TrimSpace(resp.Status),
		Summary:       strings.TrimSpace(resp.Summary),
		SnapshotID:    strings.TrimSpace(resp.SnapshotID),
		BaseCommit:    "",
		VerifyTarget:  strings.TrimSpace(resp.VerifyTarget),
		LastEventType: strings.TrimSpace(resp.LastEventType),
		LastEventNote: strings.TrimSpace(resp.LastEventNote),
		UpdatedAt:     resp.UpdatedAt,
	}, nil
}
