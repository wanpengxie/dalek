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
	manager := s.ensureChatRunnerManager()
	r, err := manager.RunTurn(ctx, ChatRunRequest{
		ConversationID: req.ConversationID,
		Provider:       strings.ToLower(req.Provider),
		Model:          req.Model,
		Reasoning:      strings.ToLower(req.Reasoning),
		Command:        req.Command,
		Prompt:         req.Prompt,
		SessionID:      req.SessionID,
		WorkDir:        req.WorkDir,
		Env:            req.Env,
		OnToolApproval: req.OnToolApproval,
	}, func(ev agentcli.Event) {
		if req.OnEvent != nil {
			req.OnEvent(ev)
		}
	})
	out := agentcli.Result{
		Command:    r.Command,
		Stdout:     r.Stdout,
		Stderr:     r.Stderr,
		Text:       r.Text,
		SessionID:  r.SessionID,
		Events:     make([]agentcli.Event, 0, len(r.Events)),
		OutputMode: agentcli.OutputText,
	}
	if out.Command == "" {
		out.Command = strings.ToLower(req.Provider) + "(sdk)"
	}
	switch strings.ToLower(string(r.OutputMode)) {
	case string(agentcli.OutputJSONL):
		out.OutputMode = agentcli.OutputJSONL
	case string(agentcli.OutputJSON):
		out.OutputMode = agentcli.OutputJSON
	default:
		out.OutputMode = agentcli.OutputText
	}
	for _, ev := range r.Events {
		out.Events = append(out.Events, agentcli.Event{
			Type:      ev.Type,
			Text:      ev.Text,
			RawJSON:   ev.RawJSON,
			SessionID: ev.SessionID,
		})
	}
	return out, err
}
