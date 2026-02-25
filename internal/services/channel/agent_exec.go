package channel

import (
	"context"
	"strings"

	"dalek/internal/services/channel/agentcli"
)

func (s *Service) runAgentCLI(ctx context.Context, backend agentcli.Backend, req agentcli.RunRequest) (agentcli.Result, error) {
	return agentcli.Run(ctx, backend, req)
}

type runAgentSDKRequest struct {
	ConversationID string
	Provider       string
	Model          string
	Command        string
	WorkDir        string
	Prompt         string
	SessionID      string
	Env            map[string]string
	Reasoning      string
	OnToolApproval func(ctx context.Context, toolName string, input map[string]any) (bool, error)
	OnEvent        func(agentcli.Event)
}

func (s *Service) runAgentSDK(ctx context.Context, req runAgentSDKRequest) (agentcli.Result, error) {
	if s == nil {
		return agentcli.Result{}, context.Canceled
	}
	manager := s.chatRunners
	if manager == nil {
		manager = newDefaultChatRunnerManager(nil)
		s.chatRunners = manager
	}
	r, err := manager.RunTurn(ctx, ChatRunRequest{
		ConversationID: strings.TrimSpace(req.ConversationID),
		Provider:       strings.TrimSpace(strings.ToLower(req.Provider)),
		Model:          strings.TrimSpace(req.Model),
		Reasoning:      strings.TrimSpace(strings.ToLower(req.Reasoning)),
		Command:        strings.TrimSpace(req.Command),
		Prompt:         strings.TrimSpace(req.Prompt),
		SessionID:      strings.TrimSpace(req.SessionID),
		WorkDir:        strings.TrimSpace(req.WorkDir),
		Env:            req.Env,
		OnToolApproval: req.OnToolApproval,
	}, func(ev agentcli.Event) {
		if req.OnEvent != nil {
			req.OnEvent(ev)
		}
	})
	out := agentcli.Result{
		Command:    strings.TrimSpace(r.Command),
		Stdout:     strings.TrimSpace(r.Stdout),
		Stderr:     strings.TrimSpace(r.Stderr),
		Text:       strings.TrimSpace(r.Text),
		SessionID:  strings.TrimSpace(r.SessionID),
		Events:     make([]agentcli.Event, 0, len(r.Events)),
		OutputMode: agentcli.OutputText,
	}
	if out.Command == "" {
		out.Command = strings.TrimSpace(strings.ToLower(req.Provider)) + "(sdk)"
	}
	switch strings.TrimSpace(strings.ToLower(string(r.OutputMode))) {
	case string(agentcli.OutputJSONL):
		out.OutputMode = agentcli.OutputJSONL
	case string(agentcli.OutputJSON):
		out.OutputMode = agentcli.OutputJSON
	default:
		out.OutputMode = agentcli.OutputText
	}
	for _, ev := range r.Events {
		out.Events = append(out.Events, agentcli.Event{
			Type:      strings.TrimSpace(ev.Type),
			Text:      strings.TrimSpace(ev.Text),
			RawJSON:   strings.TrimSpace(ev.RawJSON),
			SessionID: strings.TrimSpace(ev.SessionID),
		})
	}
	return out, err
}
