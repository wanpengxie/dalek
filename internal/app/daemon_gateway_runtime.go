package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"dalek/internal/contracts"
	channelsvc "dalek/internal/services/channel"
)

type daemonGatewayProjectResolver struct {
	home     *Home
	registry *ProjectRegistry
}

func newDaemonGatewayProjectResolver(home *Home, registries ...*ProjectRegistry) *daemonGatewayProjectResolver {
	var registry *ProjectRegistry
	if len(registries) > 0 {
		registry = registries[0]
	}
	if registry == nil && home != nil {
		registry = NewProjectRegistry(home)
	}
	return &daemonGatewayProjectResolver{
		home:     home,
		registry: registry,
	}
}

func (r *daemonGatewayProjectResolver) Resolve(name string) (*channelsvc.ProjectContext, error) {
	if r == nil || r.registry == nil {
		return nil, fmt.Errorf("daemon gateway project resolver 未初始化")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("project name 不能为空")
	}
	p, err := r.registry.Open(name)
	if err != nil {
		return nil, err
	}
	return &channelsvc.ProjectContext{
		Name:     strings.TrimSpace(p.Name()),
		RepoRoot: strings.TrimSpace(p.RepoRoot()),
		Runtime: &daemonGatewayProjectRuntime{
			project: p,
			channel: p.channel,
		},
	}, nil
}

func (r *daemonGatewayProjectResolver) ListProjects() ([]string, error) {
	if r == nil || r.home == nil {
		return nil, fmt.Errorf("daemon gateway project resolver 未初始化")
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

func (r *daemonGatewayProjectResolver) ResolveProjectMeta(name string) (*contracts.ProjectMeta, error) {
	project, err := r.Resolve(name)
	if err != nil {
		return nil, err
	}
	if project == nil {
		return nil, nil
	}
	return &contracts.ProjectMeta{
		Name:     strings.TrimSpace(project.Name),
		RepoRoot: strings.TrimSpace(project.RepoRoot),
	}, nil
}

type daemonGatewayProjectRuntime struct {
	project *Project
	channel *channelsvc.Service
}

func (r *daemonGatewayProjectRuntime) ProcessInbound(ctx context.Context, env contracts.InboundEnvelope) (channelsvc.ProcessResult, error) {
	if r == nil || r.channel == nil {
		return channelsvc.ProcessResult{}, fmt.Errorf("project runtime 为空")
	}
	return r.channel.ProcessInbound(ctx, env)
}

func (r *daemonGatewayProjectRuntime) GatewayTurnTimeout() time.Duration {
	if r == nil || r.project == nil {
		return 0
	}
	return r.project.GatewayTurnTimeout()
}

func (r *daemonGatewayProjectRuntime) InterruptConversation(ctx context.Context, channelType contracts.ChannelType, adapter, peerConversationID string) (channelsvc.InterruptResult, error) {
	if r == nil || r.channel == nil {
		return channelsvc.InterruptResult{}, fmt.Errorf("project runtime 为空")
	}
	return r.channel.InterruptPeerConversation(ctx, channelType, adapter, peerConversationID)
}

func (r *daemonGatewayProjectRuntime) ResetConversationSession(ctx context.Context, channelType contracts.ChannelType, adapter, peerConversationID string) (bool, error) {
	if r == nil || r.channel == nil {
		return false, fmt.Errorf("project runtime 为空")
	}
	return r.channel.ResetPeerConversationSession(ctx, channelType, adapter, peerConversationID)
}

func (r *daemonGatewayProjectRuntime) HardResetConversation(ctx context.Context, channelType contracts.ChannelType, adapter, peerConversationID string) (bool, error) {
	if r == nil || r.channel == nil {
		return false, fmt.Errorf("project runtime 为空")
	}
	return r.channel.HardResetPeerConversation(ctx, channelType, adapter, peerConversationID)
}

func (r *daemonGatewayProjectRuntime) ListPendingActions(ctx context.Context, jobID uint) ([]channelsvc.PendingActionView, error) {
	if r == nil || r.channel == nil {
		return nil, fmt.Errorf("project runtime 为空")
	}
	return r.channel.ListPendingActions(ctx, jobID)
}

func (r *daemonGatewayProjectRuntime) ApprovePendingAction(ctx context.Context, actionID uint, decider string) (channelsvc.PendingActionDecisionResult, error) {
	if r == nil || r.channel == nil {
		return channelsvc.PendingActionDecisionResult{}, fmt.Errorf("project runtime 为空")
	}
	return r.channel.ApprovePendingAction(ctx, actionID, decider)
}

func (r *daemonGatewayProjectRuntime) RejectPendingAction(ctx context.Context, actionID uint, decider, note string) (channelsvc.PendingActionDecisionResult, error) {
	if r == nil || r.channel == nil {
		return channelsvc.PendingActionDecisionResult{}, fmt.Errorf("project runtime 为空")
	}
	return r.channel.RejectPendingAction(ctx, actionID, decider, note)
}

func (r *daemonGatewayProjectRuntime) DecidePendingAction(ctx context.Context, req channelsvc.PendingActionDecisionRequest) (channelsvc.PendingActionDecisionResult, error) {
	if r == nil || r.channel == nil {
		return channelsvc.PendingActionDecisionResult{}, fmt.Errorf("project runtime 为空")
	}
	return r.channel.DecidePendingAction(ctx, req)
}
