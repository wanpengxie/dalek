package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	nodesvc "dalek/internal/services/node"
)

func (p *Project) RegisterNode(ctx context.Context, opt RegisterNodeOptions) (Node, error) {
	if p == nil || p.node == nil {
		return Node{}, fmt.Errorf("project node service 为空")
	}
	opt.Name = strings.TrimSpace(opt.Name)
	return p.node.Register(ctx, opt)
}

func (p *Project) GetNodeByName(ctx context.Context, name string) (*Node, error) {
	if p == nil || p.node == nil {
		return nil, fmt.Errorf("project node service 为空")
	}
	return p.node.GetByName(ctx, strings.TrimSpace(name))
}

func (p *Project) ListNodes(ctx context.Context, opt ListNodesOptions) ([]Node, error) {
	if p == nil || p.node == nil {
		return nil, fmt.Errorf("project node service 为空")
	}
	opt.Status = strings.TrimSpace(opt.Status)
	return p.node.List(ctx, opt)
}

func (p *Project) RemoveNode(ctx context.Context, name string) (bool, error) {
	if p == nil || p.node == nil {
		return false, fmt.Errorf("project node service 为空")
	}
	return p.node.RemoveByName(ctx, strings.TrimSpace(name))
}

func (p *Project) UpdateNodeStatus(ctx context.Context, name, status string, lastSeenAt *time.Time) error {
	if p == nil || p.node == nil {
		return fmt.Errorf("project node service 为空")
	}
	return p.node.UpdateStatus(ctx, strings.TrimSpace(name), strings.TrimSpace(status), lastSeenAt)
}

func (p *Project) HeartbeatNode(ctx context.Context, name string, observedAt *time.Time) error {
	if p == nil || p.nodeSession == nil {
		return fmt.Errorf("project node session service 为空")
	}
	return p.nodeSession.Heartbeat(ctx, strings.TrimSpace(name), observedAt)
}

func (p *Project) BeginNodeSession(ctx context.Context, name string, observedAt *time.Time) (nodesvc.BeginSessionResult, error) {
	if p == nil || p.nodeSession == nil {
		return nodesvc.BeginSessionResult{}, fmt.Errorf("project node session service 为空")
	}
	return p.nodeSession.BeginSession(ctx, strings.TrimSpace(name), observedAt)
}

func (p *Project) HeartbeatNodeWithEpoch(ctx context.Context, name string, sessionEpoch int, observedAt *time.Time) error {
	if p == nil || p.nodeSession == nil {
		return fmt.Errorf("project node session service 为空")
	}
	return p.nodeSession.HeartbeatWithEpoch(ctx, strings.TrimSpace(name), sessionEpoch, observedAt)
}

func (p *Project) RefreshExpiredNodes(ctx context.Context, leaseTTL time.Duration) (nodesvc.RefreshExpiredResult, error) {
	if p == nil || p.nodeSession == nil {
		return nodesvc.RefreshExpiredResult{}, fmt.Errorf("project node session service 为空")
	}
	return p.nodeSession.RefreshExpired(ctx, leaseTTL)
}
