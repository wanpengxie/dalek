package channel

import (
	"context"
	"fmt"
	"strings"

	"dalek/internal/agent/provider"
	"dalek/internal/agent/sdkrunner"
	"dalek/internal/contracts"
	"dalek/internal/services/agentexec"
	"dalek/internal/services/channel/agentcli"
	"dalek/internal/services/core"
)

func (s *Service) runAgentCLI(ctx context.Context, conversationID string, backend agentcli.Backend, req agentcli.RunRequest) (agentcli.Result, error) {
	prepared, err := agentcli.PrepareCommand(backend, req)
	if err != nil {
		return agentcli.Result{}, err
	}

	executor := agentexec.NewProcessExecutor(agentexec.ProcessConfig{
		Provider: channelBackendProvider{
			command:    prepared.Command,
			args:       prepared.Args,
			outputMode: prepared.OutputMode,
			backend:    backend,
		},
		BaseConfig: agentexec.BaseConfig{
			Runtime:     s.channelTaskRuntime(),
			OwnerType:   contracts.TaskOwnerChannel,
			TaskType:    contracts.TaskTypeChannelTurn,
			ProjectKey:  s.channelTaskProjectKey(),
			SubjectType: "channel_conversation",
			SubjectID:   strings.TrimSpace(conversationID),
			WorkDir:     strings.TrimSpace(req.WorkDir),
		},
		Stdin: prepared.Stdin,
	})
	handle, err := executor.Execute(ctx, strings.TrimSpace(req.Prompt))
	if err != nil {
		return agentcli.Result{}, err
	}
	runRes, waitErr := handle.Wait(ctx)
	out := agentcli.Result{
		Command:    strings.TrimSpace(prepared.Command),
		Stdout:     strings.TrimSpace(runRes.Stdout),
		Stderr:     strings.TrimSpace(runRes.Stderr),
		OutputMode: prepared.OutputMode,
	}
	if waitErr != nil {
		return out, waitErr
	}
	text, sessionID, events := agentcli.ParseOutput(out.Stdout, prepared.OutputMode, backend)
	out.Text = strings.TrimSpace(text)
	out.SessionID = strings.TrimSpace(sessionID)
	out.Events = events
	return out, nil
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
	if ctx == nil {
		return agentcli.Result{}, fmt.Errorf("context 不能为空")
	}
	manager := s.chatRunners
	if manager == nil {
		manager = newDefaultChatRunnerManager(nil)
		s.chatRunners = manager
	}
	providerName := strings.TrimSpace(strings.ToLower(req.Provider))
	executor := agentexec.NewSDKExecutor(agentexec.SDKConfig{
		Provider:        providerName,
		Model:           strings.TrimSpace(req.Model),
		ReasoningEffort: strings.TrimSpace(strings.ToLower(req.Reasoning)),
		Command:         strings.TrimSpace(req.Command),
		Runner: channelSDKTaskRunner{
			manager: manager,
			req:     req,
		},
		BaseConfig: agentexec.BaseConfig{
			Runtime:     s.channelTaskRuntime(),
			OwnerType:   contracts.TaskOwnerChannel,
			TaskType:    contracts.TaskTypeChannelTurn,
			ProjectKey:  s.channelTaskProjectKey(),
			SubjectType: "channel_conversation",
			SubjectID:   strings.TrimSpace(req.ConversationID),
			WorkDir:     strings.TrimSpace(req.WorkDir),
			Env:         req.Env,
		},
		SessionID: strings.TrimSpace(req.SessionID),
	})

	handle, err := executor.Execute(ctx, strings.TrimSpace(req.Prompt))
	if err != nil {
		return agentcli.Result{}, err
	}
	runRes, waitErr := handle.Wait(ctx)

	events, sessionID := parseSDKParsedEvents(runRes.Parsed.Events)
	out := agentcli.Result{
		Command:    strings.TrimSpace(req.Command),
		Stdout:     strings.TrimSpace(runRes.Stdout),
		Stderr:     strings.TrimSpace(runRes.Stderr),
		Text:       strings.TrimSpace(runRes.Parsed.Text),
		SessionID:  strings.TrimSpace(req.SessionID),
		Events:     events,
		OutputMode: sdkOutputMode(providerName),
	}
	if out.Command == "" {
		out.Command = providerName + "(sdk)"
	}
	if strings.TrimSpace(sessionID) != "" {
		out.SessionID = strings.TrimSpace(sessionID)
	}
	if out.Text == "" {
		out.Text = lastAgentCLIEventText(events)
	}
	if waitErr != nil {
		return out, waitErr
	}
	return out, nil
}

func (s *Service) channelTaskRuntime() core.TaskRuntime {
	if s == nil || s.p == nil || s.p.TaskRuntime == nil || s.p.DB == nil {
		return nil
	}
	return s.p.TaskRuntime.ForDB(s.p.DB)
}

func (s *Service) channelTaskProjectKey() string {
	if s == nil || s.p == nil {
		return ""
	}
	if v := strings.TrimSpace(s.p.Key); v != "" {
		return v
	}
	return strings.TrimSpace(s.p.Name)
}

type channelBackendProvider struct {
	command    string
	args       []string
	outputMode agentcli.OutputMode
	backend    agentcli.Backend
}

func (p channelBackendProvider) Name() string {
	name := strings.TrimSpace(p.command)
	if name == "" {
		return "channel_backend"
	}
	return name
}

func (p channelBackendProvider) BuildCommand(prompt string) (string, []string) {
	_ = prompt
	return strings.TrimSpace(p.command), append([]string(nil), p.args...)
}

func (p channelBackendProvider) ParseOutput(stdout string) provider.ParsedOutput {
	text, _, events := agentcli.ParseOutput(stdout, p.outputMode, p.backend)
	outEvents := make([]any, 0, len(events))
	for _, ev := range events {
		outEvents = append(outEvents, map[string]any{
			"type":       strings.TrimSpace(ev.Type),
			"text":       strings.TrimSpace(ev.Text),
			"raw_json":   strings.TrimSpace(ev.RawJSON),
			"session_id": strings.TrimSpace(ev.SessionID),
		})
	}
	return provider.ParsedOutput{
		Text:   strings.TrimSpace(text),
		Events: outEvents,
	}
}

type channelSDKTaskRunner struct {
	manager ChatRunnerManager
	req     runAgentSDKRequest
}

func (r channelSDKTaskRunner) Run(ctx context.Context, req sdkrunner.Request, onEvent sdkrunner.EventHandler) (sdkrunner.Result, error) {
	if r.manager == nil {
		return sdkrunner.Result{}, fmt.Errorf("chat runner manager 为空")
	}
	out, err := r.manager.RunTurn(ctx, ChatRunRequest{
		ConversationID: strings.TrimSpace(r.req.ConversationID),
		Provider:       strings.TrimSpace(strings.ToLower(req.Provider)),
		Model:          strings.TrimSpace(req.Model),
		Reasoning:      strings.TrimSpace(strings.ToLower(req.ReasoningEffort)),
		Command:        strings.TrimSpace(req.Command),
		WorkDir:        strings.TrimSpace(req.WorkDir),
		Prompt:         strings.TrimSpace(req.Prompt),
		SessionID:      strings.TrimSpace(req.SessionID),
		Env:            req.Env,
		OnToolApproval: r.req.OnToolApproval,
	}, func(ev agentcli.Event) {
		clean := cleanCLIEvent(ev)
		if onEvent != nil {
			onEvent(sdkrunner.Event{
				Type:      clean.Type,
				Text:      clean.Text,
				RawJSON:   clean.RawJSON,
				SessionID: clean.SessionID,
			})
		}
		if r.req.OnEvent != nil {
			r.req.OnEvent(clean)
		}
	})
	events := make([]sdkrunner.Event, 0, len(out.Events))
	for _, ev := range out.Events {
		clean := cleanCLIEvent(ev)
		events = append(events, sdkrunner.Event{
			Type:      clean.Type,
			Text:      clean.Text,
			RawJSON:   clean.RawJSON,
			SessionID: clean.SessionID,
		})
	}
	mode := strings.TrimSpace(strings.ToLower(string(out.OutputMode)))
	if mode == "" {
		mode = string(sdkOutputMode(strings.TrimSpace(strings.ToLower(req.Provider))))
	}
	return sdkrunner.Result{
		Provider:   strings.TrimSpace(strings.ToLower(req.Provider)),
		OutputMode: mode,
		Text:       strings.TrimSpace(out.Text),
		SessionID:  strings.TrimSpace(out.SessionID),
		Stdout:     strings.TrimSpace(out.Stdout),
		Stderr:     strings.TrimSpace(out.Stderr),
		Events:     events,
	}, err
}

func parseSDKParsedEvents(raw []any) ([]agentcli.Event, string) {
	if len(raw) == 0 {
		return nil, ""
	}
	events := make([]agentcli.Event, 0, len(raw))
	lastSessionID := ""
	for _, item := range raw {
		obj, ok := item.(map[string]any)
		if !ok {
			continue
		}
		ev := cleanCLIEvent(agentcli.Event{
			Type:      asTrimmedString(obj["type"]),
			Text:      asTrimmedString(obj["text"]),
			RawJSON:   asTrimmedString(obj["raw_json"]),
			SessionID: asTrimmedString(obj["session_id"]),
		})
		if ev.SessionID != "" {
			lastSessionID = ev.SessionID
		}
		events = append(events, ev)
	}
	if len(events) == 0 {
		return nil, strings.TrimSpace(lastSessionID)
	}
	return events, strings.TrimSpace(lastSessionID)
}

func sdkOutputMode(providerName string) agentcli.OutputMode {
	switch strings.TrimSpace(strings.ToLower(providerName)) {
	case agentcli.ProviderCodex:
		return agentcli.OutputJSONL
	case agentcli.ProviderClaude:
		return agentcli.OutputJSON
	default:
		return agentcli.OutputText
	}
}

func cleanCLIEvent(ev agentcli.Event) agentcli.Event {
	return agentcli.Event{
		Type:      strings.TrimSpace(ev.Type),
		Text:      strings.TrimSpace(ev.Text),
		RawJSON:   strings.TrimSpace(ev.RawJSON),
		SessionID: strings.TrimSpace(ev.SessionID),
	}
}

func asTrimmedString(v any) string {
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(s)
}
