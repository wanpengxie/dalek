package channel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"dalek/internal/agent/auditlog"
	"dalek/internal/agent/progresstimeout"
	agentprovider "dalek/internal/agent/provider"
	"dalek/internal/agent/sdkrunner"
	"dalek/internal/services/channel/agentcli"
)

type ChatRunRequest struct {
	ConversationID string
	Provider       string
	Model          string
	Reasoning      string
	Command        string
	WorkDir        string
	Prompt         string
	SessionID      string
	Env            map[string]string

	// OnToolApproval is called when the agent requests permission to use a tool
	// (via Claude SDK's CanUseTool mechanism). Returns true to allow, false to deny.
	// Only effective for Claude provider with ask-mode permission rules.
	// If nil, all tool uses matching ask rules are auto-allowed.
	OnToolApproval func(ctx context.Context, toolName string, input map[string]any) (bool, error)
}

type ChatRunResult struct {
	Command    string
	Stdout     string
	Stderr     string
	Text       string
	SessionID  string
	OutputMode agentcli.OutputMode
	Events     []agentcli.Event
}

type ChatEventHandler func(agentcli.Event)

type ChatRunner interface {
	RunTurn(ctx context.Context, req ChatRunRequest, onEvent ChatEventHandler) (ChatRunResult, error)
	Close() error
}

type InterruptibleChatRunner interface {
	Interrupt(ctx context.Context) (bool, error)
}

type ForceCloseChatRunner interface {
	ForceClose() error
}

type ChatRunnerManager interface {
	RunTurn(ctx context.Context, req ChatRunRequest, onEvent ChatEventHandler) (ChatRunResult, error)
	InterruptConversation(ctx context.Context, conversationID string) (bool, error)
	CloseConversation(conversationID string)
	ForceCloseConversation(conversationID string) error
	Close() error
}

var createClaudeChatRunner = newClaudeChatRunner
var createGeminiChatRunner = newGeminiChatRunner

func newDefaultChatRunnerManager(taskRunner sdkrunner.TaskRunner) ChatRunnerManager {
	return &defaultChatRunnerManager{
		taskRunner: taskRunner,
		stateful:   map[string]*managedChatRunner{},
	}
}

type managedChatRunner struct {
	signature string
	runner    ChatRunner
}

type defaultChatRunnerManager struct {
	taskRunner sdkrunner.TaskRunner

	mu       sync.Mutex
	stateful map[string]*managedChatRunner
}

func (m *defaultChatRunnerManager) RunTurn(ctx context.Context, req ChatRunRequest, onEvent ChatEventHandler) (ChatRunResult, error) {
	req = normalizeChatRunRequest(req)
	startedAt := time.Now()
	_ = auditlog.Append(req.WorkDir, map[string]any{
		"layer":            "chat_runner",
		"phase":            "request",
		"provider":         req.Provider,
		"conversation_id":  req.ConversationID,
		"model":            req.Model,
		"reasoning_effort": req.Reasoning,
		"command":          req.Command,
		"work_dir":         req.WorkDir,
		"session_id":       req.SessionID,
		"prompt":           req.Prompt,
		"env_keys":         auditlog.SortedEnvKeys(req.Env),
	})
	if req.Provider == "" {
		err := fmt.Errorf("chat runner provider 为空")
		appendChatRunnerResponseAudit(req, startedAt, ChatRunResult{}, err)
		return ChatRunResult{}, err
	}
	if req.Prompt == "" {
		err := fmt.Errorf("chat runner prompt 为空")
		appendChatRunnerResponseAudit(req, startedAt, ChatRunResult{}, err)
		return ChatRunResult{}, err
	}
	runCtx, watchdog := progresstimeout.New(ctx, 0)
	defer watchdog.Stop()
	progressHandler := func(ev agentcli.Event) {
		watchdog.Touch()
		if onEvent != nil {
			onEvent(ev)
		}
	}

	if !providerUsesStatefulChatRunner(req.Provider) {
		runner := &sdkTaskBackedChatRunner{taskRunner: m.taskRunner}
		out, err := runner.RunTurn(runCtx, req, progressHandler)
		if watchdog.TimedOut() {
			err = watchdog.TimeoutError("agent chat turn")
		}
		appendChatRunnerResponseAudit(req, startedAt, out, err)
		return out, err
	}

	key := buildStatefulRunnerKey(req.Provider, req.ConversationID)
	signature := buildStatefulRunnerSignature(req)

	runner, err := m.getOrCreateStatefulRunner(ctx, key, signature, req)
	if err != nil {
		appendChatRunnerResponseAudit(req, startedAt, ChatRunResult{}, err)
		return ChatRunResult{}, err
	}
	result, runErr := runner.RunTurn(runCtx, req, progressHandler)
	if watchdog.TimedOut() {
		runErr = watchdog.TimeoutError("agent chat turn")
	}
	if runErr != nil && shouldEvictStatefulRunner(runErr) {
		m.evictStatefulRunner(key, runner)
	}
	appendChatRunnerResponseAudit(req, startedAt, result, runErr)
	return result, runErr
}

func appendChatRunnerResponseAudit(req ChatRunRequest, startedAt time.Time, out ChatRunResult, runErr error) {
	_ = auditlog.Append(req.WorkDir, map[string]any{
		"layer":            "chat_runner",
		"phase":            "response",
		"provider":         req.Provider,
		"conversation_id":  req.ConversationID,
		"model":            req.Model,
		"reasoning_effort": req.Reasoning,
		"command":          req.Command,
		"work_dir":         req.WorkDir,
		"session_id":       out.SessionID,
		"duration_ms":      time.Since(startedAt).Milliseconds(),
		"output_mode":      string(out.OutputMode),
		"text":             out.Text,
		"stdout":           out.Stdout,
		"stderr":           out.Stderr,
		"events":           out.Events,
		"error":            chatErrString(runErr),
	})
}

func chatErrString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func (m *defaultChatRunnerManager) InterruptConversation(ctx context.Context, conversationID string) (bool, error) {
	conv := conversationID
	if conv == "" {
		return false, nil
	}
	prefix := "|" + conv
	m.mu.Lock()
	runners := make([]ChatRunner, 0, 2)
	for key, item := range m.stateful {
		if !strings.HasSuffix(key, prefix) {
			continue
		}
		if item == nil || item.runner == nil {
			continue
		}
		runners = append(runners, item.runner)
	}
	m.mu.Unlock()
	if len(runners) == 0 {
		return false, nil
	}

	interrupted := false
	errs := make([]string, 0, len(runners))
	for _, runner := range runners {
		interruptible, ok := runner.(InterruptibleChatRunner)
		if !ok {
			continue
		}
		okInterrupt, err := interruptible.Interrupt(ctx)
		if err != nil {
			errs = append(errs, err.Error())
			continue
		}
		if okInterrupt {
			interrupted = true
		}
	}
	if len(errs) > 0 {
		clean := make([]string, 0, len(errs))
		for _, item := range errs {
			if item == "" {
				continue
			}
			clean = append(clean, item)
		}
		if len(clean) > 0 {
			return interrupted, fmt.Errorf("interrupt stateful chat runner failed: %s", strings.Join(clean, "; "))
		}
	}
	return interrupted, nil
}

func (m *defaultChatRunnerManager) CloseConversation(conversationID string) {
	conv := conversationID
	if conv == "" {
		return
	}
	prefix := "|" + conv
	m.mu.Lock()
	removed := make([]ChatRunner, 0, 2)
	for key, item := range m.stateful {
		if !strings.HasSuffix(key, prefix) {
			continue
		}
		delete(m.stateful, key)
		if item != nil && item.runner != nil {
			removed = append(removed, item.runner)
		}
	}
	m.mu.Unlock()
	for _, runner := range removed {
		_ = runner.Close()
	}
}

func (m *defaultChatRunnerManager) ForceCloseConversation(conversationID string) error {
	conv := conversationID
	if conv == "" {
		return nil
	}
	prefix := "|" + conv
	m.mu.Lock()
	removed := make([]ChatRunner, 0, 2)
	for key, item := range m.stateful {
		if !strings.HasSuffix(key, prefix) {
			continue
		}
		delete(m.stateful, key)
		if item != nil && item.runner != nil {
			removed = append(removed, item.runner)
		}
	}
	m.mu.Unlock()
	var errs []error
	for _, runner := range removed {
		if forceCloser, ok := runner.(ForceCloseChatRunner); ok && forceCloser != nil {
			if err := forceCloser.ForceClose(); err != nil {
				errs = append(errs, err)
			}
			continue
		}
		if err := runner.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (m *defaultChatRunnerManager) Close() error {
	m.mu.Lock()
	removed := make([]ChatRunner, 0, len(m.stateful))
	for key, item := range m.stateful {
		delete(m.stateful, key)
		if item != nil && item.runner != nil {
			removed = append(removed, item.runner)
		}
	}
	m.mu.Unlock()
	for _, runner := range removed {
		_ = runner.Close()
	}
	return nil
}

func (m *defaultChatRunnerManager) getOrCreateStatefulRunner(ctx context.Context, key, signature string, req ChatRunRequest) (ChatRunner, error) {
	m.mu.Lock()
	existing := m.stateful[key]
	if existing != nil && existing.runner != nil && existing.signature == signature {
		runner := existing.runner
		m.mu.Unlock()
		return runner, nil
	}
	var stale ChatRunner
	if existing != nil && existing.runner != nil {
		stale = existing.runner
		delete(m.stateful, key)
	}
	m.mu.Unlock()
	if stale != nil {
		_ = stale.Close()
	}

	var (
		runner ChatRunner
		err    error
	)
	switch req.Provider {
	case "claude":
		runner, err = createClaudeChatRunner(ctx, req)
	case "gemini":
		runner, err = createGeminiChatRunner(ctx, req)
	default:
		runner = &sdkTaskBackedChatRunner{taskRunner: m.taskRunner}
	}
	if err != nil {
		return nil, err
	}

	m.mu.Lock()
	existing = m.stateful[key]
	if existing != nil && existing.runner != nil && existing.signature == signature {
		m.mu.Unlock()
		_ = runner.Close()
		return existing.runner, nil
	}
	m.stateful[key] = &managedChatRunner{
		signature: signature,
		runner:    runner,
	}
	m.mu.Unlock()
	return runner, nil
}

func (m *defaultChatRunnerManager) evictStatefulRunner(key string, candidate ChatRunner) {
	m.mu.Lock()
	var stale ChatRunner
	if existing := m.stateful[key]; existing != nil && existing.runner == candidate {
		delete(m.stateful, key)
		stale = existing.runner
	}
	m.mu.Unlock()
	if stale != nil {
		_ = stale.Close()
	}
}

func providerUsesStatefulChatRunner(provider string) bool {
	switch strings.ToLower(provider) {
	case "claude", "gemini":
		return true
	default:
		return false
	}
}

func shouldEvictStatefulRunner(err error) bool {
	msg := strings.ToLower(err.Error())
	if msg == "" {
		return false
	}
	switch {
	case strings.Contains(msg, "broken pipe"),
		strings.Contains(msg, "connection reset"),
		strings.Contains(msg, "eof"),
		strings.Contains(msg, "not connected"),
		strings.Contains(msg, "stream disconnected"),
		strings.Contains(msg, "transport is closed"):
		return true
	default:
		return false
	}
}

func buildStatefulRunnerKey(provider, conversationID string) string {
	p := strings.ToLower(provider)
	conv := conversationID
	if conv == "" {
		conv = "conv-default"
	}
	return p + "|" + conv
}

func buildStatefulRunnerSignature(req ChatRunRequest) string {
	parts := []string{
		"provider=" + strings.ToLower(req.Provider),
		"model=" + req.Model,
		"command=" + req.Command,
		"workdir=" + req.WorkDir,
		"reasoning=" + strings.ToLower(req.Reasoning),
		"env=" + encodeChatEnv(req.Env),
	}
	return strings.Join(parts, "|")
}

func encodeChatEnv(env map[string]string) string {
	if len(env) == 0 {
		return ""
	}
	keys := make([]string, 0, len(env))
	for key := range env {
		if key == "" {
			continue
		}
		keys = append(keys, key)
	}
	if len(keys) == 0 {
		return ""
	}
	sort.Strings(keys)
	payload := make(map[string]string, len(keys))
	for _, key := range keys {
		payload[key] = env[key]
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return ""
	}
	return string(b)
}

func normalizeChatRunRequest(req ChatRunRequest) ChatRunRequest {
	req.ConversationID = strings.TrimSpace(req.ConversationID)
	req.Provider = strings.ToLower(strings.TrimSpace(req.Provider))
	req.Model = strings.TrimSpace(req.Model)
	req.Reasoning = strings.ToLower(strings.TrimSpace(req.Reasoning))
	req.Command = strings.TrimSpace(req.Command)
	req.WorkDir = strings.TrimSpace(req.WorkDir)
	req.Prompt = strings.TrimSpace(req.Prompt)
	req.SessionID = strings.TrimSpace(req.SessionID)
	req.Env = cloneChatEnv(req.Env)
	return req
}

func cloneChatEnv(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		key := strings.TrimSpace(k)
		if key == "" {
			continue
		}
		out[key] = v
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

type sdkTaskBackedChatRunner struct {
	taskRunner sdkrunner.TaskRunner
}

func (r *sdkTaskBackedChatRunner) RunTurn(ctx context.Context, req ChatRunRequest, onEvent ChatEventHandler) (ChatRunResult, error) {
	runner := r.taskRunner
	if runner == nil {
		runner = sdkrunner.DefaultTaskRunner{}
	}
	internalEvents := make([]agentcli.Event, 0, 64)
	result, err := runner.Run(ctx, sdkrunner.Request{
		AgentConfig: agentprovider.AgentConfig{
			Provider:        req.Provider,
			Model:           req.Model,
			ReasoningEffort: req.Reasoning,
			Command:         req.Command,
		},
		Prompt:    req.Prompt,
		SessionID: req.SessionID,
		WorkDir:   req.WorkDir,
		Env:       req.Env,
	}, func(ev sdkrunner.Event) {
		item := agentcli.Event{
			Type:      ev.Type,
			Text:      ev.Text,
			RawJSON:   ev.RawJSON,
			SessionID: ev.SessionID,
		}
		internalEvents = append(internalEvents, item)
		if onEvent != nil {
			onEvent(item)
		}
	})

	out := ChatRunResult{
		Command:    req.Command,
		Stdout:     result.Stdout,
		Stderr:     result.Stderr,
		Text:       result.Text,
		SessionID:  result.SessionID,
		Events:     internalEvents,
		OutputMode: agentcli.OutputText,
	}
	if out.Command == "" {
		out.Command = strings.ToLower(req.Provider) + "(sdk)"
	}
	switch strings.ToLower(result.OutputMode) {
	case string(agentcli.OutputJSONL):
		out.OutputMode = agentcli.OutputJSONL
	case string(agentcli.OutputJSON):
		out.OutputMode = agentcli.OutputJSON
	default:
		out.OutputMode = agentcli.OutputText
	}
	return out, err
}

func (r *sdkTaskBackedChatRunner) Close() error { return nil }
