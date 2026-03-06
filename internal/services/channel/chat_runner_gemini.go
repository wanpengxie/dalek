package channel

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"

	"dalek/internal/agent/sdkrunner"
	"dalek/internal/services/channel/agentcli"
	gemini "github.com/wanpengxie/go-gemini-sdk"
)

type geminiClient interface {
	Connect(ctx context.Context) error
	Query(ctx context.Context, prompt string) (geminiTurn, error)
	Interrupt(ctx context.Context) error
	Close() error
	SessionID() string
	Err() error
}

type geminiTurn interface {
	Messages() <-chan gemini.Message
	Errors() <-chan error
}

var createGeminiClient = func(opts ...gemini.Option) geminiClient {
	return geminiClientAdapter{client: gemini.NewClient(opts...)}
}

type geminiClientAdapter struct {
	client *gemini.Client
}

func (a geminiClientAdapter) Connect(ctx context.Context) error {
	return a.client.Connect(ctx)
}

func (a geminiClientAdapter) Query(ctx context.Context, prompt string) (geminiTurn, error) {
	return a.client.Query(ctx, prompt)
}

func (a geminiClientAdapter) Interrupt(ctx context.Context) error {
	return a.client.Interrupt(ctx)
}

func (a geminiClientAdapter) Close() error {
	return a.client.Close()
}

func (a geminiClientAdapter) SessionID() string {
	return a.client.SessionID()
}

func (a geminiClientAdapter) Err() error {
	return a.client.Err()
}

type geminiChatRunner struct {
	runMu sync.Mutex
	mu    sync.Mutex

	opts   []gemini.Option
	client geminiClient

	toolApprovalMu sync.Mutex
	toolApprovalFn func(ctx context.Context, toolName string, input map[string]any) (bool, error)
}

func newGeminiChatRunner(ctx context.Context, req ChatRunRequest) (ChatRunner, error) {
	_ = ctx
	runner := &geminiChatRunner{}
	opts := make([]gemini.Option, 0, 8)
	if req.Model != "" {
		opts = append(opts, gemini.WithModel(req.Model))
	}
	if req.WorkDir != "" {
		opts = append(opts, gemini.WithWorkDir(req.WorkDir))
	}
	if req.Command != "" {
		opts = append(opts, gemini.WithBinaryPath(req.Command))
	}
	if env := geminiChatEnvList(req.Env); len(env) > 0 {
		opts = append(opts, gemini.WithEnv(env...))
	}
	if addDirs := sdkrunner.SDKAdditionalDirectories(req.WorkDir); len(addDirs) > 0 {
		opts = append(opts, gemini.WithAddDirs(addDirs...))
	}
	opts = append(opts, gemini.WithCanUseTool(runner.canUseTool))
	runner.opts = opts
	return runner, nil
}

func (r *geminiChatRunner) setToolApproval(fn func(ctx context.Context, toolName string, input map[string]any) (bool, error)) {
	r.toolApprovalMu.Lock()
	defer r.toolApprovalMu.Unlock()
	r.toolApprovalFn = fn
}

func (r *geminiChatRunner) canUseTool(ctx context.Context, call gemini.ToolCallInfo, options []gemini.PermissionOption) (string, error) {
	r.toolApprovalMu.Lock()
	fn := r.toolApprovalFn
	r.toolApprovalMu.Unlock()

	if fn == nil {
		return pickGeminiChatPermissionOption(true, options), nil
	}

	input := map[string]any{}
	if len(call.Args) > 0 {
		_ = json.Unmarshal(call.Args, &input)
	}
	allow, err := fn(ctx, strings.TrimSpace(call.ToolName), input)
	if err != nil {
		return "", err
	}
	return pickGeminiChatPermissionOption(allow, options), nil
}

func (r *geminiChatRunner) RunTurn(ctx context.Context, req ChatRunRequest, onEvent ChatEventHandler) (ChatRunResult, error) {
	r.runMu.Lock()
	defer r.runMu.Unlock()

	r.setToolApproval(req.OnToolApproval)
	defer r.setToolApproval(nil)

	client, err := r.ensureClientConnected(ctx)
	if err != nil {
		return ChatRunResult{}, err
	}
	turn, err := client.Query(ctx, req.Prompt)
	if err != nil {
		r.resetClientIfSame(client)
		return ChatRunResult{}, err
	}

	msgsCh, errsCh := turn.Messages(), turn.Errors()
	lines := make([]string, 0, 128)
	events := make([]agentcli.Event, 0, 64)
	var reply strings.Builder
	var resultErr error
	out := ChatRunResult{
		Command:    req.Command,
		OutputMode: agentcli.OutputJSON,
	}
	if out.Command == "" {
		out.Command = "gemini(sdk)"
	}

	finalize := func() ChatRunResult {
		out.Events = events
		out.Stdout = strings.Join(lines, "\n")
		if out.SessionID == "" {
			out.SessionID = strings.TrimSpace(client.SessionID())
		}
		out.Text = strings.TrimSpace(reply.String())
		if out.Text == "" {
			out.Text = lastAgentCLIEventText(events)
		}
		return out
	}

	for msgsCh != nil || errsCh != nil {
		select {
		case <-ctx.Done():
			return finalize(), ctx.Err()
		case err, ok := <-errsCh:
			if !ok {
				errsCh = nil
				continue
			}
			if err != nil {
				r.resetClientIfSame(client)
				return finalize(), err
			}
		case msg, ok := <-msgsCh:
			if !ok {
				msgsCh = nil
				continue
			}
			item, resultMessageErr := convertGeminiMessageToAgentCLIEvent(msg)
			if item.SessionID != "" {
				out.SessionID = item.SessionID
			}
			events = append(events, item)
			if item.RawJSON != "" {
				lines = append(lines, item.RawJSON)
			}
			if text := geminiChatReplyText(msg); text != "" {
				reply.WriteString(text)
			}
			if onEvent != nil {
				onEvent(item)
			}
			if resultMessageErr != nil {
				resultErr = resultMessageErr
			}
		}
	}

	if err := client.Err(); err != nil {
		r.resetClientIfSame(client)
		return finalize(), err
	}
	if resultErr != nil {
		return finalize(), resultErr
	}
	return finalize(), nil
}

func (r *geminiChatRunner) Interrupt(ctx context.Context) (bool, error) {
	if ctx == nil {
		return false, fmt.Errorf("context 不能为空")
	}
	r.mu.Lock()
	client := r.client
	r.mu.Unlock()
	if client == nil {
		return false, nil
	}
	if err := client.Interrupt(ctx); err != nil {
		msg := strings.ToLower(err.Error())
		if strings.Contains(msg, "closed pipe") || strings.Contains(msg, "not connected") {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (r *geminiChatRunner) Close() error {
	r.runMu.Lock()
	defer r.runMu.Unlock()
	return r.resetClient()
}

func (r *geminiChatRunner) ForceClose() error {
	r.mu.Lock()
	client := r.client
	r.client = nil
	r.mu.Unlock()
	if client == nil {
		return nil
	}
	return client.Close()
}

func (r *geminiChatRunner) ensureClientConnected(ctx context.Context) (geminiClient, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.client != nil {
		return r.client, nil
	}
	if ctx == nil {
		return nil, fmt.Errorf("context 不能为空")
	}
	client := createGeminiClient(r.opts...)
	if err := client.Connect(ctx); err != nil {
		return nil, err
	}
	r.client = client
	return r.client, nil
}

func (r *geminiChatRunner) resetClient() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.resetClientLocked()
}

func (r *geminiChatRunner) resetClientIfSame(candidate geminiClient) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.client == nil || r.client != candidate {
		return
	}
	_ = r.resetClientLocked()
}

func (r *geminiChatRunner) resetClientLocked() error {
	if r.client == nil {
		return nil
	}
	err := r.client.Close()
	r.client = nil
	return err
}

func convertGeminiMessageToAgentCLIEvent(msg gemini.Message) (agentcli.Event, error) {
	switch m := msg.(type) {
	case *gemini.AssistantMessage:
		return agentcli.Event{
			Type:      geminiChatAssistantEventType(m),
			Text:      extractGeminiAssistantTextForChat(m),
			RawJSON:   mustJSONForChat(m),
			SessionID: strings.TrimSpace(m.SessionID),
		}, nil
	case *gemini.ResultMessage:
		text := extractGeminiResultTextForChat(m)
		eventType := "completed"
		if m.IsError {
			eventType = "error"
		}
		var err error
		if m.IsError {
			err = fmt.Errorf("%s", text)
		}
		return agentcli.Event{
			Type:      eventType,
			Text:      text,
			RawJSON:   mustJSONForChat(m),
			SessionID: strings.TrimSpace(m.SessionID),
		}, err
	default:
		text := strings.TrimSpace(collectAnyTextForChat(msg))
		return agentcli.Event{
			Type:    "message",
			Text:    text,
			RawJSON: mustJSONForChat(msg),
		}, nil
	}
}

func extractGeminiAssistantTextForChat(msg *gemini.AssistantMessage) string {
	if msg == nil {
		return ""
	}
	parts := make([]string, 0, len(msg.Content))
	for _, block := range msg.Content {
		switch b := block.(type) {
		case *gemini.TextBlock:
			if s := strings.TrimSpace(b.Text); s != "" {
				parts = append(parts, s)
			}
		case *gemini.ThinkingBlock:
			if s := strings.TrimSpace(b.Thinking); s != "" {
				parts = append(parts, s)
			}
		case *gemini.ToolUseBlock:
			if s := strings.TrimSpace(b.Name); s != "" {
				parts = append(parts, s)
			}
		case *gemini.ToolResultBlock:
			if s := strings.TrimSpace(collectAnyTextForChat(b.Content)); s != "" {
				parts = append(parts, s)
			} else if s := strings.TrimSpace(b.Name); s != "" {
				parts = append(parts, s)
			}
		default:
			if s := strings.TrimSpace(collectAnyTextForChat(block)); s != "" {
				parts = append(parts, s)
			}
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

func geminiChatReplyText(msg gemini.Message) string {
	assistant, ok := msg.(*gemini.AssistantMessage)
	if !ok || assistant == nil {
		return ""
	}
	parts := make([]string, 0, len(assistant.Content))
	for _, block := range assistant.Content {
		textBlock, ok := block.(*gemini.TextBlock)
		if !ok {
			continue
		}
		if s := strings.TrimSpace(textBlock.Text); s != "" {
			parts = append(parts, s)
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

func geminiChatAssistantEventType(msg *gemini.AssistantMessage) string {
	if msg == nil {
		return "message"
	}
	for _, block := range msg.Content {
		switch block.(type) {
		case *gemini.TextBlock:
			return "message"
		case *gemini.ThinkingBlock:
			return "thinking"
		case *gemini.ToolUseBlock:
			return "tool_call"
		case *gemini.ToolResultBlock:
			return "tool_call_update"
		}
	}
	return "message"
}

func extractGeminiResultTextForChat(msg *gemini.ResultMessage) string {
	if msg == nil {
		return "completed"
	}
	if s := strings.TrimSpace(msg.Error); s != "" {
		return s
	}
	if s := strings.TrimSpace(msg.StopReason); s != "" {
		return s
	}
	if msg.IsError {
		return "gemini turn failed"
	}
	return "completed"
}

func geminiChatEnvList(extra map[string]string) []string {
	env := cloneChatEnv(extra)
	if len(env) == 0 {
		return nil
	}
	keys := make([]string, 0, len(env))
	for key := range env {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, key := range keys {
		out = append(out, key+"="+env[key])
	}
	return out
}

func pickGeminiChatPermissionOption(allow bool, options []gemini.PermissionOption) string {
	if allow {
		if id := findGeminiChatPermissionOptionByPrefix(options, "allow_"); id != "" {
			return id
		}
		if id := findGeminiChatPermissionOptionByPrefix(options, "ask_"); id != "" {
			return id
		}
	} else {
		if id := findGeminiChatPermissionOptionByPrefix(options, "reject_"); id != "" {
			return id
		}
		if id := findGeminiChatPermissionOptionByPrefix(options, "deny_"); id != "" {
			return id
		}
		if id := findGeminiChatPermissionOptionByPrefix(options, "ask_"); id != "" {
			return id
		}
	}
	for _, option := range options {
		if id := strings.TrimSpace(option.OptionIDValue()); id != "" {
			return id
		}
	}
	return ""
}

func findGeminiChatPermissionOptionByPrefix(options []gemini.PermissionOption, prefix string) string {
	prefix = strings.ToLower(strings.TrimSpace(prefix))
	if prefix == "" {
		return ""
	}
	for _, option := range options {
		id := strings.TrimSpace(option.OptionIDValue())
		if id == "" {
			continue
		}
		if strings.HasPrefix(strings.ToLower(id), prefix) {
			return id
		}
	}
	return ""
}
