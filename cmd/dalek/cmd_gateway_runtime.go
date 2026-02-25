package main

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"dalek/internal/app"
	"dalek/internal/contracts"
	channelsvc "dalek/internal/services/channel"
)

type homeProjectResolver struct {
	home *app.Home

	mu    sync.Mutex
	cache map[string]*channelsvc.ProjectContext
}

func newHomeProjectResolver(home *app.Home) *homeProjectResolver {
	return &homeProjectResolver{
		home:  home,
		cache: map[string]*channelsvc.ProjectContext{},
	}
}

func (r *homeProjectResolver) Resolve(name string) (*channelsvc.ProjectContext, error) {
	if r == nil || r.home == nil {
		return nil, fmt.Errorf("project resolver 未初始化")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("project name 不能为空")
	}

	r.mu.Lock()
	if cached, ok := r.cache[name]; ok && cached != nil {
		r.mu.Unlock()
		return cached, nil
	}
	r.mu.Unlock()

	p, err := r.home.OpenProjectByName(name)
	if err != nil {
		return nil, err
	}
	ctx := &channelsvc.ProjectContext{
		Name:     strings.TrimSpace(p.Name()),
		RepoRoot: strings.TrimSpace(p.RepoRoot()),
		Runtime:  &appProjectRuntime{project: p},
	}

	r.mu.Lock()
	r.cache[name] = ctx
	r.mu.Unlock()
	return ctx, nil
}

func (r *homeProjectResolver) ListProjects() ([]string, error) {
	if r == nil || r.home == nil {
		return nil, fmt.Errorf("project resolver 未初始化")
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

type appProjectRuntime struct {
	project *app.Project
}

func (r *appProjectRuntime) ProcessInbound(ctx context.Context, env contracts.InboundEnvelope) (channelsvc.ProcessResult, error) {
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
	return out, nil
}

func (r *appProjectRuntime) GatewayTurnTimeout() time.Duration {
	if r == nil || r.project == nil {
		return 0
	}
	return r.project.GatewayTurnTimeout()
}

func (r *appProjectRuntime) InterruptConversation(ctx context.Context, channelType, adapter, peerConversationID string) (channelsvc.InterruptResult, error) {
	if r == nil || r.project == nil {
		return channelsvc.InterruptResult{}, fmt.Errorf("project runtime 为空")
	}
	return r.project.InterruptChannelConversation(ctx, channelType, adapter, peerConversationID)
}

func (r *appProjectRuntime) ResetConversationSession(ctx context.Context, channelType, adapter, peerConversationID string) (bool, error) {
	if r == nil || r.project == nil {
		return false, fmt.Errorf("project runtime 为空")
	}
	return r.project.ResetChannelConversationSession(ctx, channelType, adapter, peerConversationID)
}
