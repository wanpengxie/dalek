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
	Send(ctx context.Context, prompt string) error
	ReceiveMessagesWithErrors() (<-chan gemini.StreamBlock, <-chan error)
	Interrupt(ctx context.Context) error
	Close() error
	SessionID() string
	Err() error
}

var createGeminiClient = func(opts ...gemini.Option) geminiClient {
	return gemini.NewClient(opts...)
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
	if err := client.Send(ctx, req.Prompt); err != nil {
		r.resetClientIfSame(client)
		return ChatRunResult{}, err
	}

	blocksCh, errsCh := client.ReceiveMessagesWithErrors()
	lines := make([]string, 0, 128)
	events := make([]agentcli.Event, 0, 64)
	var reply strings.Builder
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

	for blocksCh != nil || errsCh != nil {
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
		case block, ok := <-blocksCh:
			if !ok {
				blocksCh = nil
				continue
			}
			item := convertGeminiStreamBlockToAgentCLIEvent(block)
			if item.SessionID != "" {
				out.SessionID = item.SessionID
			}
			events = append(events, item)
			if item.RawJSON != "" {
				lines = append(lines, item.RawJSON)
			}
			if isGeminiChatMessageBlock(block) && strings.TrimSpace(block.Text) != "" {
				reply.WriteString(block.Text)
			}
			if onEvent != nil {
				onEvent(item)
			}
			if block.Done || block.Kind == gemini.BlockKindDone {
				if err := client.Err(); err != nil {
					r.resetClientIfSame(client)
					return finalize(), err
				}
				return finalize(), nil
			}
		}
	}

	if err := client.Err(); err != nil {
		r.resetClientIfSame(client)
		return finalize(), err
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

func convertGeminiStreamBlockToAgentCLIEvent(block gemini.StreamBlock) agentcli.Event {
	eventType := strings.TrimSpace(block.RawType)
	if eventType == "" {
		eventType = geminiChatBlockEventType(block)
	}
	return agentcli.Event{
		Type:      eventType,
		Text:      extractGeminiBlockTextForChat(block),
		RawJSON:   mustJSONForChat(block),
		SessionID: strings.TrimSpace(block.SessionID),
	}
}

func extractGeminiBlockTextForChat(block gemini.StreamBlock) string {
	if s := strings.TrimSpace(block.Text); s != "" {
		return s
	}
	if s := strings.TrimSpace(block.Error); s != "" {
		return s
	}
	if len(block.Data) > 0 {
		var raw any
		if err := json.Unmarshal(block.Data, &raw); err == nil {
			if s := strings.TrimSpace(collectAnyTextForChat(raw)); s != "" {
				return s
			}
		}
	}
	if s := strings.TrimSpace(block.ToolName); s != "" {
		return s
	}
	if block.Done || block.Kind == gemini.BlockKindDone {
		return "completed"
	}
	return geminiChatBlockEventType(block)
}

func isGeminiChatMessageBlock(block gemini.StreamBlock) bool {
	switch block.Kind {
	case gemini.BlockKindText:
		return true
	default:
		return false
	}
}

func geminiChatBlockEventType(block gemini.StreamBlock) string {
	switch block.Kind {
	case gemini.BlockKindText:
		return "message"
	case gemini.BlockKindThinking:
		return "thinking"
	case gemini.BlockKindToolCall:
		return "tool_call"
	case gemini.BlockKindToolResult:
		return "tool_call_update"
	case gemini.BlockKindDone:
		return "completed"
	case gemini.BlockKindError:
		return "error"
	default:
		return "unknown"
	}
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
