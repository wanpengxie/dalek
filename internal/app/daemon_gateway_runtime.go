package app

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"dalek/internal/contracts"
	channelsvc "dalek/internal/services/channel"
)

const defaultResolverCacheTTL = 30 * time.Second

type daemonGatewayResolverCacheEntry struct {
	ctx       *channelsvc.ProjectContext
	expiresAt time.Time
}

type daemonGatewayProjectResolver struct {
	home *Home
	ttl  time.Duration

	mu    sync.Mutex
	cache map[string]*daemonGatewayResolverCacheEntry
}

func newDaemonGatewayProjectResolver(home *Home) *daemonGatewayProjectResolver {
	return &daemonGatewayProjectResolver{
		home:  home,
		ttl:   defaultResolverCacheTTL,
		cache: map[string]*daemonGatewayResolverCacheEntry{},
	}
}

func (r *daemonGatewayProjectResolver) Resolve(name string) (*channelsvc.ProjectContext, error) {
	if r == nil || r.home == nil {
		return nil, fmt.Errorf("daemon gateway project resolver 未初始化")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("project name 不能为空")
	}
	now := time.Now()

	r.mu.Lock()
	if cached, ok := r.cache[name]; ok && cached != nil && cached.ctx != nil && now.Before(cached.expiresAt) {
		r.mu.Unlock()
		return cached.ctx, nil
	}
	r.mu.Unlock()

	p, err := r.home.OpenProjectByName(name)
	if err != nil {
		return nil, err
	}
	ctx := &channelsvc.ProjectContext{
		Name:     strings.TrimSpace(p.Name()),
		RepoRoot: strings.TrimSpace(p.RepoRoot()),
		Runtime:  &daemonGatewayProjectRuntime{project: p},
	}
	ttl := r.ttl
	if ttl <= 0 {
		ttl = defaultResolverCacheTTL
	}

	r.mu.Lock()
	r.cache[name] = &daemonGatewayResolverCacheEntry{
		ctx:       ctx,
		expiresAt: time.Now().Add(ttl),
	}
	r.mu.Unlock()
	return ctx, nil
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
}

func (r *daemonGatewayProjectRuntime) ProcessInbound(ctx context.Context, env contracts.InboundEnvelope) (channelsvc.ProcessResult, error) {
	if r == nil || r.project == nil {
		return channelsvc.ProcessResult{}, fmt.Errorf("project runtime 为空")
	}
	res, err := r.project.ProcessChannelInbound(ctx, env)
	if err != nil {
		return channelsvc.ProcessResult{}, err
	}
	out := channelsvc.ProcessResult{
		BindingID:         res.BindingID,
		ConversationID:    res.ConversationID,
		InboundMessageID:  res.InboundMessageID,
		JobID:             res.JobID,
		RunID:             strings.TrimSpace(res.RunID),
		JobStatus:         res.JobStatus,
		JobError:          strings.TrimSpace(res.JobError),
		JobErrorType:      strings.TrimSpace(res.JobErrorType),
		OutboundMessageID: res.OutboundMessageID,
		OutboxID:          res.OutboxID,
		ReplyText:         strings.TrimSpace(res.ReplyText),
		AgentProvider:     strings.TrimSpace(res.AgentProvider),
		AgentModel:        strings.TrimSpace(res.AgentModel),
		AgentOutputMode:   strings.TrimSpace(res.AgentOutputMode),
		AgentCommand:      strings.TrimSpace(res.AgentCommand),
	}
	if len(res.AgentEvents) > 0 {
		out.AgentEvents = make([]channelsvc.AgentEvent, 0, len(res.AgentEvents))
		for _, ev := range res.AgentEvents {
			out.AgentEvents = append(out.AgentEvents, channelsvc.AgentEvent{
				RunID:  strings.TrimSpace(ev.RunID),
				Seq:    ev.Seq,
				Stream: channelsvc.AgentEventStream(strings.TrimSpace(ev.Stream)),
				Ts:     ev.Ts,
				Data: channelsvc.AgentEventData{
					Phase:     strings.TrimSpace(ev.Data.Phase),
					StartedAt: ev.Data.StartedAt,
					EndedAt:   ev.Data.EndedAt,
					Text:      strings.TrimSpace(ev.Data.Text),
					Error:     strings.TrimSpace(ev.Data.Error),
					ErrorType: strings.TrimSpace(ev.Data.ErrorType),
					ToolName:  strings.TrimSpace(ev.Data.ToolName),
					ToolInput: strings.TrimSpace(ev.Data.ToolInput),
				},
			})
		}
	}
	if len(res.PendingActions) > 0 {
		out.PendingActions = make([]channelsvc.PendingActionView, 0, len(res.PendingActions))
		for _, item := range res.PendingActions {
			mapped := channelsvc.PendingActionView{
				ID:             item.ID,
				ConversationID: item.ConversationID,
				JobID:          item.JobID,
				Action: contracts.TurnAction{
					Name: strings.TrimSpace(item.ActionName),
				},
				Status:       item.Status,
				Decider:      strings.TrimSpace(item.Decider),
				DecisionNote: strings.TrimSpace(item.DecisionNote),
			}
			if len(item.ActionArgs) > 0 {
				mapped.Action.Args = make(map[string]any, len(item.ActionArgs))
				for k, v := range item.ActionArgs {
					mapped.Action.Args[strings.TrimSpace(k)] = v
				}
			} else {
				mapped.Action.Args = map[string]any{}
			}
			if item.DecidedAt != nil {
				t := *item.DecidedAt
				mapped.DecidedAt = &t
			}
			if item.ExecutedAt != nil {
				t := *item.ExecutedAt
				mapped.ExecutedAt = &t
			}
			out.PendingActions = append(out.PendingActions, mapped)
		}
	}
	return out, nil
}

func (r *daemonGatewayProjectRuntime) GatewayTurnTimeout() time.Duration {
	if r == nil || r.project == nil {
		return 0
	}
	return r.project.GatewayTurnTimeout()
}

func (r *daemonGatewayProjectRuntime) InterruptConversation(ctx context.Context, channelType, adapter, peerConversationID string) (channelsvc.InterruptResult, error) {
	if r == nil || r.project == nil {
		return channelsvc.InterruptResult{}, fmt.Errorf("project runtime 为空")
	}
	return r.project.InterruptChannelConversation(ctx, channelType, adapter, peerConversationID)
}

func (r *daemonGatewayProjectRuntime) ResetConversationSession(ctx context.Context, channelType, adapter, peerConversationID string) (bool, error) {
	if r == nil || r.project == nil {
		return false, fmt.Errorf("project runtime 为空")
	}
	return r.project.ResetChannelConversationSession(ctx, channelType, adapter, peerConversationID)
}

func (r *daemonGatewayProjectRuntime) ListPendingActions(ctx context.Context, jobID uint) ([]channelsvc.PendingActionView, error) {
	if r == nil || r.project == nil {
		return nil, fmt.Errorf("project runtime 为空")
	}
	return r.project.ListChannelPendingActions(ctx, jobID)
}

func (r *daemonGatewayProjectRuntime) ApprovePendingAction(ctx context.Context, actionID uint, decider string) (channelsvc.PendingActionDecisionResult, error) {
	if r == nil || r.project == nil {
		return channelsvc.PendingActionDecisionResult{}, fmt.Errorf("project runtime 为空")
	}
	return r.project.ApproveChannelPendingAction(ctx, actionID, decider)
}

func (r *daemonGatewayProjectRuntime) RejectPendingAction(ctx context.Context, actionID uint, decider, note string) (channelsvc.PendingActionDecisionResult, error) {
	if r == nil || r.project == nil {
		return channelsvc.PendingActionDecisionResult{}, fmt.Errorf("project runtime 为空")
	}
	return r.project.RejectChannelPendingAction(ctx, actionID, decider, note)
}

func (r *daemonGatewayProjectRuntime) DecidePendingAction(ctx context.Context, req channelsvc.PendingActionDecisionRequest) (channelsvc.PendingActionDecisionResult, error) {
	if r == nil || r.project == nil {
		return channelsvc.PendingActionDecisionResult{}, fmt.Errorf("project runtime 为空")
	}
	return r.project.DecideChannelPendingAction(ctx, req)
}
