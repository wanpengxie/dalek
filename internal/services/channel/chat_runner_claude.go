package channel

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"dalek/internal/agent/sdkrunner"
	"dalek/internal/services/channel/agentcli"
	claude "github.com/wanpengxie/go-claude-agent-sdk"
)

type claudeClient interface {
	Connect(ctx context.Context) error
	QueryWithSession(ctx context.Context, prompt string, sessionID string) error
	ReceiveResponseWithErrors(ctx context.Context) (<-chan claude.Message, <-chan error)
	Interrupt(ctx context.Context) error
	Close() error
}

var createClaudeClient = func(opts ...claude.Option) claudeClient {
	return claude.NewClient(opts...)
}

type claudeChatRunner struct {
	runMu sync.Mutex
	mu    sync.Mutex

	opts   []claude.Option
	client claudeClient

	// Per-turn tool approval callback, set by RunTurn, read by canUseTool.
	toolApprovalMu sync.Mutex
	toolApprovalFn func(ctx context.Context, toolName string, input map[string]any) (bool, error)
}

var highRiskBashCommandPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(^|[\s;&|()])git\s+push(\s|$)`),
	regexp.MustCompile(`(^|[\s;&|()])docker(\s|$)`),
	regexp.MustCompile(`(^|[\s;&|()])rm\s+-rf(\s|$)`),
	regexp.MustCompile(`(^|[\s;&|()])curl(\s|$)`),
	regexp.MustCompile(`(^|[\s;&|()])wget(\s|$)`),
	regexp.MustCompile(`(^|[\s;&|()])kill(\s|$)`),
	regexp.MustCompile(`(^|[\s;&|()])sudo(\s|$)`),
	regexp.MustCompile(`(^|[\s;&|()])ssh(\s|$)`),
	regexp.MustCompile(`(^|[\s;&|()])scp(\s|$)`),
	regexp.MustCompile(`(^|[\s;&|()])git\s+reset\s+--hard(\s|$)`),
	regexp.MustCompile(`(^|[\s;&|()])git\s+clean\s+-f[\w-]*(\s|$)`),
}

func newClaudeChatRunner(ctx context.Context, req ChatRunRequest) (ChatRunner, error) {
	_ = ctx
	runner := &claudeChatRunner{}

	opts := make([]claude.Option, 0, 10)
	if req.Model != "" {
		opts = append(opts, claude.WithModel(req.Model))
	}
	if req.WorkDir != "" {
		opts = append(opts, claude.WithCwd(req.WorkDir))
	}
	if req.Command != "" {
		opts = append(opts, claude.WithCLIPath(req.Command))
	}
	if req.SessionID != "" {
		opts = append(opts, claude.WithResume(req.SessionID))
	}
	env := cloneChatEnv(req.Env)
	if env == nil {
		env = map[string]string{}
	}
	if _, ok := env["CLAUDECODE"]; !ok {
		env["CLAUDECODE"] = ""
	}
	opts = append(opts, claude.WithEnv(env))
	if addDirs := sdkrunner.SDKAdditionalDirectories(req.WorkDir); len(addDirs) > 0 {
		opts = append(opts, claude.WithAddDirs(addDirs...))
	}
	if settings := sdkrunner.ClaudeRunnerSettingsJSON(); settings != "" {
		opts = append(opts, claude.WithSettings(settings))
	}
	opts = append(opts, claude.WithIncludePartialMessages())
	opts = append(opts, claude.WithSettingSources(claude.SettingSourceProject))

	// Register CanUseTool callback that delegates to per-turn handler.
	// This ensures Claude CLI's ask-mode permission requests are handled
	// rather than hanging indefinitely.
	opts = append(opts, claude.WithCanUseTool(runner.canUseTool))

	runner.opts = opts
	return runner, nil
}

// canUseTool is the CanUseToolFunc registered with the Claude SDK.
// It delegates to the per-turn toolApprovalFn if set.
// When no per-turn callback is set, it auto-allows all tool uses.
func (r *claudeChatRunner) canUseTool(ctx context.Context, toolName string, input map[string]any, permCtx claude.ToolPermissionContext) (claude.PermissionResult, error) {
	r.toolApprovalMu.Lock()
	fn := r.toolApprovalFn
	r.toolApprovalMu.Unlock()

	if !shouldEscalateToManualToolApproval(toolName, input, permCtx) {
		return &claude.PermissionResultAllow{}, nil
	}

	if fn == nil {
		return &claude.PermissionResultAllow{}, nil
	}
	allow, err := fn(ctx, toolName, input)
	if err != nil {
		return &claude.PermissionResultDeny{Message: err.Error()}, nil
	}
	if allow {
		return &claude.PermissionResultAllow{}, nil
	}
	return &claude.PermissionResultDeny{Message: "用户拒绝了该操作"}, nil
}

func shouldEscalateToManualToolApproval(toolName string, input map[string]any, permCtx claude.ToolPermissionContext) bool {
	if hasAskOrDenyPermissionSuggestion(permCtx.Suggestions) {
		return true
	}

	if !strings.EqualFold(strings.TrimSpace(toolName), "bash") {
		return false
	}

	cmd := strings.TrimSpace(readToolApprovalCommand(input))
	if cmd == "" {
		return true
	}

	normalized := strings.ToLower(cmd)
	for _, pattern := range highRiskBashCommandPatterns {
		if pattern.MatchString(normalized) {
			return true
		}
	}
	return false
}

func hasAskOrDenyPermissionSuggestion(suggestions []claude.PermissionUpdate) bool {
	for _, s := range suggestions {
		if s.Behavior == claude.PermissionBehaviorAsk || s.Behavior == claude.PermissionBehaviorDeny {
			return true
		}
	}
	return false
}

func (r *claudeChatRunner) setToolApproval(fn func(ctx context.Context, toolName string, input map[string]any) (bool, error)) {
	r.toolApprovalMu.Lock()
	defer r.toolApprovalMu.Unlock()
	r.toolApprovalFn = fn
}

func (r *claudeChatRunner) RunTurn(ctx context.Context, req ChatRunRequest, onEvent ChatEventHandler) (ChatRunResult, error) {
	r.runMu.Lock()
	defer r.runMu.Unlock()

	// Set per-turn tool approval callback; clear when turn ends.
	r.setToolApproval(req.OnToolApproval)
	defer r.setToolApproval(nil)

	client, err := r.ensureClientConnected(ctx)
	if err != nil {
		return ChatRunResult{}, err
	}

	sessionID := req.SessionID
	if sessionID == "" {
		sessionID = "chat-" + randomSessionToken(8)
	}
	if err := client.QueryWithSession(ctx, req.Prompt, sessionID); err != nil {
		r.resetClientIfSame(client)
		return ChatRunResult{}, err
	}

	msgs, errs := client.ReceiveResponseWithErrors(ctx)
	lines := make([]string, 0, 128)
	texts := make([]string, 0, 16)
	events := make([]agentcli.Event, 0, 64)
	out := ChatRunResult{
		Command:    req.Command,
		OutputMode: agentcli.OutputJSON,
	}
	if out.Command == "" {
		out.Command = "claude(sdk)"
	}

	for msg := range msgs {
		ev, sid, text := convertClaudeMessageToAgentCLIEvent(msg)
		if sid != "" {
			out.SessionID = sid
		}
		if text != "" {
			texts = append(texts, text)
		}
		events = append(events, ev)
		if ev.RawJSON != "" {
			lines = append(lines, ev.RawJSON)
		}
		if onEvent != nil {
			onEvent(ev)
		}
	}
	out.Events = events
	out.Stdout = strings.Join(lines, "\n")
	if out.SessionID == "" {
		out.SessionID = sessionID
	}
	out.Text = lastNonEmptyText(texts)
	if out.Text == "" {
		out.Text = lastAgentCLIEventText(events)
	}
	if err, ok := <-errs; ok && err != nil {
		r.resetClientIfSame(client)
		return out, err
	}
	return out, nil
}

func (r *claudeChatRunner) Interrupt(ctx context.Context) (bool, error) {
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
		if strings.Contains(msg, "not connected") {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (r *claudeChatRunner) Close() error {
	r.runMu.Lock()
	defer r.runMu.Unlock()
	return r.resetClient()
}

func (r *claudeChatRunner) ensureClientConnected(ctx context.Context) (claudeClient, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.client != nil {
		return r.client, nil
	}
	if ctx == nil {
		return nil, fmt.Errorf("context 不能为空")
	}
	client := createClaudeClient(r.opts...)
	if err := client.Connect(ctx); err != nil {
		return nil, err
	}
	r.client = client
	return r.client, nil
}

func (r *claudeChatRunner) resetClient() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.resetClientLocked()
}

func (r *claudeChatRunner) resetClientIfSame(candidate claudeClient) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.client == nil || r.client != candidate {
		return
	}
	_ = r.resetClientLocked()
}

func (r *claudeChatRunner) resetClientLocked() error {
	if r.client == nil {
		return nil
	}
	err := r.client.Close()
	r.client = nil
	return err
}

func convertClaudeMessageToAgentCLIEvent(msg claude.Message) (agentcli.Event, string, string) {
	switch m := msg.(type) {
	case *claude.AssistantMessage:
		text := extractClaudeAssistantTextForChat(m)
		return agentcli.Event{
			Type:    "assistant",
			Text:    text,
			RawJSON: mustJSONForChat(m),
		}, "", text
	case *claude.ResultMessage:
		text := m.Result
		if text == "" {
			text = collectAnyTextForChat(m.StructuredOutput)
		}
		sid := m.SessionID
		return agentcli.Event{
			Type:      "result",
			Text:      text,
			RawJSON:   mustJSONForChat(m),
			SessionID: sid,
		}, sid, text
	case *claude.StreamEvent:
		text := collectAnyTextForChat(m.Event)
		sid := m.SessionID
		return agentcli.Event{
			Type:      "stream_event",
			Text:      text,
			RawJSON:   mustJSONForChat(m),
			SessionID: sid,
		}, sid, text
	case *claude.SystemMessage:
		text := collectAnyTextForChat(m.Data)
		if text == "" {
			text = m.Subtype
		}
		return agentcli.Event{
			Type:    "system",
			Text:    text,
			RawJSON: mustJSONForChat(m),
		}, "", text
	case *claude.UserMessage:
		text := collectAnyTextForChat(m.Content)
		return agentcli.Event{
			Type:    "user",
			Text:    text,
			RawJSON: mustJSONForChat(m),
		}, "", text
	case *claude.RateLimitEvent:
		text := collectAnyTextForChat(m.Data)
		return agentcli.Event{
			Type:    "rate_limit_event",
			Text:    text,
			RawJSON: mustJSONForChat(m),
		}, "", text
	default:
		text := collectAnyTextForChat(msg)
		return agentcli.Event{
			Type:    "message",
			Text:    text,
			RawJSON: mustJSONForChat(msg),
		}, "", text
	}
}

func extractClaudeAssistantTextForChat(m *claude.AssistantMessage) string {
	if m == nil || len(m.Content) == 0 {
		return ""
	}
	parts := make([]string, 0, len(m.Content))
	for _, block := range m.Content {
		switch b := block.(type) {
		case *claude.TextBlock:
			if s := b.Text; s != "" {
				parts = append(parts, s)
			}
		case *claude.ThinkingBlock:
			if s := b.Thinking; s != "" {
				parts = append(parts, s)
			}
		case *claude.ToolUseBlock:
			if s := b.Name; s != "" {
				parts = append(parts, "tool_use:"+s)
			}
		case *claude.ToolResultBlock:
			if s := collectAnyTextForChat(b.Content); s != "" {
				parts = append(parts, s)
			}
		default:
			if s := collectAnyTextForChat(block); s != "" {
				parts = append(parts, s)
			}
		}
	}
	return strings.Join(parts, "\n")
}

func mustJSONForChat(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(b)
}

func collectAnyTextForChat(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case []string:
		parts := make([]string, 0, len(x))
		for _, item := range x {
			if s := item; s != "" {
				parts = append(parts, s)
			}
		}
		return strings.Join(parts, "\n")
	case []any:
		parts := make([]string, 0, len(x))
		for _, item := range x {
			if s := collectAnyTextForChat(item); s != "" {
				parts = append(parts, s)
			}
		}
		return strings.Join(parts, "\n")
	case map[string]any:
		for _, key := range []string{"text", "content", "result", "message", "thinking", "delta", "summary"} {
			if s := collectAnyTextForChat(x[key]); s != "" {
				return s
			}
		}
		return ""
	default:
		if b, err := json.Marshal(x); err == nil {
			return string(b)
		}
		return ""
	}
}

func lastNonEmptyText(items []string) string {
	for i := len(items) - 1; i >= 0; i-- {
		if s := items[i]; s != "" {
			return s
		}
	}
	return ""
}

func lastAgentCLIEventText(events []agentcli.Event) string {
	for i := len(events) - 1; i >= 0; i-- {
		if s := events[i].Text; s != "" {
			return s
		}
	}
	return ""
}

func randomSessionToken(nbytes int) string {
	if nbytes <= 0 {
		nbytes = 8
	}
	buf := make([]byte, nbytes)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buf)
}
