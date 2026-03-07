package sdkrunner

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"log"

	"dalek/internal/agent/auditlog"
	"dalek/internal/agent/eventlog"
	agentprovider "dalek/internal/agent/provider"
	"dalek/internal/repo"

	claude "github.com/wanpengxie/go-claude-agent-sdk"
	codexsdk "github.com/wanpengxie/go-codex-sdk"
	gemini "github.com/wanpengxie/go-gemini-sdk"
)

const (
	OutputModeJSON  = "json"
	OutputModeJSONL = "jsonl"
)

type Request struct {
	AgentConfig agentprovider.AgentConfig
	Prompt      string
	SessionID   string
	WorkDir     string
	Env         map[string]string
}

type Event struct {
	Type      string
	Text      string
	RawJSON   string
	SessionID string
}

type Result struct {
	Provider   string
	OutputMode string
	Text       string
	SessionID  string
	Stdout     string
	Stderr     string
	Events     []Event
}

type EventHandler func(Event)

type TaskRunner interface {
	Run(ctx context.Context, req Request, onEvent EventHandler) (Result, error)
}

type DefaultTaskRunner struct{}

func (DefaultTaskRunner) Run(ctx context.Context, req Request, onEvent EventHandler) (Result, error) {
	return Run(ctx, req, onEvent)
}

const claudeRunnerSettingsJSON = `
{
  "$schema": "https://json.schemastore.org/claude-code-settings.json",
  "sandbox": {
    "enabled": false
  },
  "permissions": {
    "defaultMode": "acceptEdits",
    "deny": [
      "Read(./.env)",
      "Read(./.env.*)",
      "Read(./secrets/**)",
      "Read(~/.ssh/**)",
      "Read(~/.aws/**)",
      "Edit(~/.ssh/**)",
      "Edit(~/.aws/**)",
      "Edit(~/.bashrc)",
      "Edit(~/.zshrc)"
    ],
    "ask": [
      "Bash(git push*)",
      "Bash(docker *)",
      "Bash(rm -rf *)",
      "Bash(curl *)",
      "Bash(wget *)",
      "Bash(kill *)"
    ],
    "allow": [
      "Read(~/.claude/**)",
      "Edit(~/.claude/**)",
      "Read(/private/tmp/**)",
      "Edit(/private/tmp/**)",
      "Write(/private/tmp/**)",
      "Read(/tmp/**)",
      "Edit(/tmp/**)",
      "Write(/tmp/**)",
      "WebFetch(domain:github.com)",
      "WebFetch(domain:code.claude.com)",
      "Bash(dalek)",
      "Bash(dalek *)",
      "Bash(bash develop.sh)",
      "Bash(go build *)",
      "Bash(go test *)",
      "Bash(go run *)",
      "Bash(go mod *)",
      "Bash(go get *)",
      "Bash(go vet *)",
      "Bash(go fmt *)",
      "Bash(go generate *)",
      "Bash(go install *)",
      "Bash(go clean *)",
      "Bash(npm *)",
      "Bash(npx *)",
      "Bash(pnpm *)",
      "Bash(yarn *)",
      "Bash(bun *)",
      "Bash(node *)",
      "Bash(tsc *)",
      "Bash(pip install *)",
      "Bash(pip3 install *)",
      "Bash(pip list*)",
      "Bash(pip3 list*)",
      "Bash(pip show *)",
      "Bash(pip3 show *)",
      "Bash(pip freeze*)",
      "Bash(pip3 freeze*)",
      "Bash(uv *)",
      "Bash(python *)",
      "Bash(python3 *)",
      "Bash(cargo *)",
      "Bash(rustc *)",
      "Bash(make *)",
      "Bash(make)",
      "Bash(cmake *)",
      "Bash(mvn *)",
      "Bash(gradle *)",
      "Bash(composer *)",
      "Bash(gem *)",
      "Bash(bundle *)",
      "Bash(swift *)",
      "Bash(pod *)",
      "Bash(git add *)",
      "Bash(git commit *)",
      "Bash(git status*)",
      "Bash(git diff*)",
      "Bash(git log*)",
      "Bash(git branch*)",
      "Bash(git checkout*)",
      "Bash(git switch*)",
      "Bash(git merge*)",
      "Bash(git rebase*)",
      "Bash(git stash*)",
      "Bash(git fetch*)",
      "Bash(git pull*)",
      "Bash(git tag*)",
      "Bash(git show*)",
      "Bash(git rev-parse*)",
      "Bash(git cherry-pick*)",
      "Bash(echo *)",
      "Bash(echo)",
      "Bash(cp *)",
      "Bash(mv *)",
      "Bash(ls)",
      "Bash(ls *)",
      "Bash(cat *)",
      "Bash(date)",
      "Bash(date *)",
      "Bash(pwd)",
      "Bash(mkdir *)",
      "Bash(touch *)",
      "Bash(which *)",
      "Bash(env)",
      "Bash(env *)",
      "Bash(printenv)",
      "Bash(printenv *)",
      "Bash(head *)",
      "Bash(tail *)",
      "Bash(grep *)",
      "Bash(find *)",
      "Bash(wc *)",
      "Bash(sort *)",
      "Bash(uniq *)",
      "Bash(sed *)",
      "Bash(awk *)",
      "Bash(tr *)",
      "Bash(cut *)",
      "Bash(xargs *)",
      "Bash(test *)",
      "Bash([ *)",
      "Bash(true)",
      "Bash(false)",
      "Bash(sleep *)",
      "Bash(ps)",
      "Bash(ps *)"
    ]
  }
}
`

func ClaudeRunnerSettingsJSON() string {
	return strings.TrimSpace(claudeRunnerSettingsJSON)
}

func Run(ctx context.Context, req Request, onEvent EventHandler) (out Result, runErr error) {
	if ctx == nil {
		ctx = context.Background()
	}
	req.AgentConfig = req.AgentConfig.Normalize()
	req.Prompt = strings.TrimSpace(req.Prompt)
	req.SessionID = strings.TrimSpace(req.SessionID)
	req.WorkDir = strings.TrimSpace(req.WorkDir)
	startedAt := time.Now()
	_ = auditlog.Append(req.WorkDir, map[string]any{
		"layer":            "task_runner",
		"phase":            "request",
		"provider":         req.AgentConfig.Provider,
		"model":            req.AgentConfig.Model,
		"reasoning_effort": req.AgentConfig.ReasoningEffort,
		"command":          req.AgentConfig.Command,
		"work_dir":         req.WorkDir,
		"session_id":       req.SessionID,
		"prompt":           req.Prompt,
		"env_keys":         auditlog.SortedEnvKeys(req.Env),
	})
	defer func() {
		_ = auditlog.Append(req.WorkDir, map[string]any{
			"layer":            "task_runner",
			"phase":            "response",
			"provider":         req.AgentConfig.Provider,
			"model":            req.AgentConfig.Model,
			"reasoning_effort": req.AgentConfig.ReasoningEffort,
			"command":          req.AgentConfig.Command,
			"work_dir":         req.WorkDir,
			"session_id":       strings.TrimSpace(out.SessionID),
			"duration_ms":      time.Since(startedAt).Milliseconds(),
			"output_mode":      strings.TrimSpace(out.OutputMode),
			"text":             strings.TrimSpace(out.Text),
			"stdout":           strings.TrimSpace(out.Stdout),
			"stderr":           strings.TrimSpace(out.Stderr),
			"events":           out.Events,
			"error":            errString(runErr),
		})
	}()
	if req.AgentConfig.Provider == "" {
		runErr = fmt.Errorf("sdk provider 为空")
		return Result{}, runErr
	}
	if req.Prompt == "" {
		runErr = fmt.Errorf("sdk prompt 为空")
		return Result{}, runErr
	}

	// eventlog: 初始化 run 日志
	evLogProject := eventlog.ResolveProjectName(req.WorkDir)
	evLogRunID := fmt.Sprintf("task-%d", startedAt.UnixMilli())
	evLogger, evLogErr := eventlog.Open(evLogProject, evLogRunID)
	if evLogErr != nil {
		log.Printf("eventlog: open failed: %v", evLogErr)
	}
	if evLogger != nil {
		_ = evLogger.WriteHeader(eventlog.RunMeta{
			RunID:           evLogRunID,
			Project:         evLogProject,
			Provider:        req.AgentConfig.Provider,
			Model:           req.AgentConfig.Model,
			SessionID:       req.SessionID,
			WorkDir:         req.WorkDir,
			Layer:           "task_runner",
			ReasoningEffort: req.AgentConfig.ReasoningEffort,
		})
		defer func() {
			_ = evLogger.WriteFooter(eventlog.RunFooter{
				RunID:      evLogRunID,
				DurationMS: time.Since(startedAt).Milliseconds(),
				ReplyText:  strings.TrimSpace(out.Text),
				Error:      errString(runErr),
				SessionID:  strings.TrimSpace(out.SessionID),
			})
			_ = evLogger.Close()
		}()
	}
	var evLogSeq int
	originalOnEvent := onEvent
	onEvent = func(ev Event) {
		evLogSeq++
		if evLogger != nil {
			_ = evLogger.WriteEvent(evLogSeq, ev.Type, ev.RawJSON)
		}
		if originalOnEvent != nil {
			originalOnEvent(ev)
		}
	}

	switch req.AgentConfig.Provider {
	case "codex":
		out, runErr = runCodex(ctx, req, onEvent)
		return out, runErr
	case "claude":
		out, runErr = runClaude(ctx, req, onEvent)
		return out, runErr
	case "gemini":
		out, runErr = runGemini(ctx, req, onEvent)
		return out, runErr
	default:
		runErr = fmt.Errorf("unknown sdk provider: %s", req.AgentConfig.Provider)
		return Result{}, runErr
	}
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return strings.TrimSpace(err.Error())
}

func runCodex(ctx context.Context, req Request, onEvent EventHandler) (Result, error) {
	out := Result{
		Provider:   "codex",
		OutputMode: OutputModeJSONL,
	}
	env := mergeEnvMap(req.Env)
	opts := codexsdk.CodexOptions{
		CodexPathOverride: strings.TrimSpace(req.AgentConfig.Command),
	}
	if len(env) > 0 {
		opts.Env = env
	}
	client, err := codexsdk.New(opts)
	if err != nil {
		return out, err
	}
	threadOpts := codexsdk.ThreadOptions{
		Model:            strings.TrimSpace(req.AgentConfig.Model),
		SandboxMode:      codexSandboxMode(req),
		WorkingDirectory: strings.TrimSpace(req.WorkDir),
		SkipGitRepoCheck: true,
		ApprovalPolicy:   codexsdk.ApprovalNever,
	}
	networkEnabled := true
	threadOpts.NetworkAccessEnabled = &networkEnabled
	if addDirs := SDKAdditionalDirectories(req.WorkDir); len(addDirs) > 0 {
		threadOpts.AdditionalDirectories = append(threadOpts.AdditionalDirectories, addDirs...)
	}
	if effort := parseCodexReasoningEffort(req.AgentConfig.ReasoningEffort); effort != "" {
		threadOpts.ModelReasoningEffort = effort
	}

	var thread *codexsdk.Thread
	if strings.TrimSpace(req.SessionID) != "" {
		thread = client.ResumeThread(strings.TrimSpace(req.SessionID), threadOpts)
	} else {
		thread = client.StartThread(threadOpts)
	}

	streamed := thread.RunStreamed(ctx, codexsdk.StringInput(req.Prompt))
	lines := make([]string, 0, 128)
	finalTexts := make([]string, 0, 16)
	for ev := range streamed.Events {
		raw := mustJSON(ev)
		txt := extractCodexEventText(ev)
		sessionID := strings.TrimSpace(thread.ID())
		item := Event{
			Type:      strings.TrimSpace(string(ev.Type)),
			Text:      txt,
			RawJSON:   raw,
			SessionID: sessionID,
		}
		out.Events = append(out.Events, item)
		if raw != "" {
			lines = append(lines, raw)
		}
		if onEvent != nil {
			onEvent(item)
		}
		if ev.Type == codexsdk.EventItemCompleted && ev.Item != nil && ev.Item.Type == codexsdk.ItemAgentMessage {
			if s := strings.TrimSpace(ev.Item.Text); s != "" {
				finalTexts = append(finalTexts, s)
			}
		}
	}
	out.Stdout = strings.Join(lines, "\n")
	out.SessionID = strings.TrimSpace(thread.ID())
	out.Text = lastNonEmpty(finalTexts)
	if out.Text == "" {
		out.Text = lastEventText(out.Events)
	}
	if err := streamed.Err(); err != nil {
		return out, err
	}
	return out, nil
}

func runClaude(ctx context.Context, req Request, onEvent EventHandler) (Result, error) {
	out := Result{
		Provider:   "claude",
		OutputMode: OutputModeJSON,
	}
	opts := make([]claude.Option, 0, 8)
	if strings.TrimSpace(req.AgentConfig.Model) != "" {
		opts = append(opts, claude.WithModel(strings.TrimSpace(req.AgentConfig.Model)))
	}
	if strings.TrimSpace(req.WorkDir) != "" {
		opts = append(opts, claude.WithCwd(strings.TrimSpace(req.WorkDir)))
	}
	if strings.TrimSpace(req.AgentConfig.Command) != "" {
		opts = append(opts, claude.WithCLIPath(strings.TrimSpace(req.AgentConfig.Command)))
	}
	if strings.TrimSpace(req.SessionID) != "" {
		opts = append(opts, claude.WithResume(strings.TrimSpace(req.SessionID)))
	}
	env := normalizeEnvMap(req.Env)
	if env == nil {
		env = map[string]string{}
	}
	// 清除嵌套会话检测，避免从父 Claude Code 进程继承 CLAUDECODE=1 导致子进程拒绝启动
	if _, ok := env["CLAUDECODE"]; !ok {
		env["CLAUDECODE"] = ""
	}
	opts = append(opts, claude.WithEnv(env))
	if addDirs := SDKAdditionalDirectories(req.WorkDir); len(addDirs) > 0 {
		opts = append(opts, claude.WithAddDirs(addDirs...))
	}
	if settings := ClaudeRunnerSettingsJSON(); settings != "" {
		opts = append(opts, claude.WithSettings(settings))
	}
	if mode := claudePermissionMode(req); mode != "" {
		opts = append(opts, claude.WithPermissionMode(mode))
	}
	opts = append(opts, claude.WithIncludePartialMessages())
	opts = append(opts, claude.WithSettingSources(claude.SettingSourceProject))
	opts = append(opts, claude.WithCanUseTool(autoApproveClaudeTool))

	msgs, errs := claude.Query(ctx, req.Prompt, opts...)
	lines := make([]string, 0, 128)
	texts := make([]string, 0, 16)
	for msg := range msgs {
		ev, sid, text := convertClaudeMessage(msg)
		if sid != "" {
			out.SessionID = sid
		}
		if text != "" {
			texts = append(texts, text)
		}
		out.Events = append(out.Events, ev)
		if ev.RawJSON != "" {
			lines = append(lines, ev.RawJSON)
		}
		if onEvent != nil {
			onEvent(ev)
		}
	}
	out.Stdout = strings.Join(lines, "\n")
	out.Text = lastNonEmpty(texts)
	if out.Text == "" {
		out.Text = lastEventText(out.Events)
	}
	if err, ok := <-errs; ok && err != nil {
		return out, err
	}
	return out, nil
}

func autoApproveClaudeTool(ctx context.Context, toolName string, input map[string]any, permCtx claude.ToolPermissionContext) (claude.PermissionResult, error) {
	_ = ctx
	_ = toolName
	_ = input
	_ = permCtx
	return &claude.PermissionResultAllow{}, nil
}

func codexSandboxMode(req Request) codexsdk.SandboxMode {
	if req.AgentConfig.DangerFullAccess {
		return codexsdk.SandboxDangerFullAccess
	}
	return codexsdk.SandboxWorkspaceWrite
}

func claudePermissionMode(req Request) claude.PermissionMode {
	if req.AgentConfig.BypassPermissions {
		return claude.PermissionBypassPermissions
	}
	return ""
}

func runGemini(ctx context.Context, req Request, onEvent EventHandler) (Result, error) {
	out := Result{
		Provider:   "gemini",
		OutputMode: OutputModeJSON,
	}
	opts := make([]gemini.Option, 0, 8)
	if strings.TrimSpace(req.AgentConfig.Model) != "" {
		opts = append(opts, gemini.WithModel(strings.TrimSpace(req.AgentConfig.Model)))
	}
	if strings.TrimSpace(req.WorkDir) != "" {
		opts = append(opts, gemini.WithWorkDir(strings.TrimSpace(req.WorkDir)))
	}
	if strings.TrimSpace(req.AgentConfig.Command) != "" {
		opts = append(opts, gemini.WithBinaryPath(strings.TrimSpace(req.AgentConfig.Command)))
	}
	if env := geminiEnvList(req.Env); len(env) > 0 {
		opts = append(opts, gemini.WithEnv(env...))
	}
	if addDirs := SDKAdditionalDirectories(req.WorkDir); len(addDirs) > 0 {
		opts = append(opts, gemini.WithAddDirs(addDirs...))
	}
	opts = append(opts, gemini.WithCanUseTool(autoApproveGeminiTool))

	lines := make([]string, 0, 128)
	texts := make([]string, 0, 16)
	msgs, errs := gemini.Query(ctx, req.Prompt, opts...)
	for msg := range msgs {
		ev, sid, text := convertGeminiMessage(msg)
		if sid != "" {
			out.SessionID = sid
		}
		if text != "" {
			texts = append(texts, text)
		}
		out.Events = append(out.Events, ev)
		if ev.RawJSON != "" {
			lines = append(lines, ev.RawJSON)
		}
		if onEvent != nil {
			onEvent(ev)
		}
	}
	out.Stdout = strings.Join(lines, "\n")
	out.Text = lastNonEmpty(texts)
	if out.Text == "" {
		out.Text = lastEventText(out.Events)
	}
	if err, ok := <-errs; ok && err != nil {
		return out, err
	}
	return out, nil
}

func convertClaudeMessage(msg claude.Message) (Event, string, string) {
	switch m := msg.(type) {
	case *claude.AssistantMessage:
		text := extractClaudeAssistantText(m)
		return Event{
			Type:    "assistant",
			Text:    text,
			RawJSON: mustJSON(m),
		}, "", text
	case *claude.ResultMessage:
		text := strings.TrimSpace(m.Result)
		if text == "" {
			text = strings.TrimSpace(collectAnyText(m.StructuredOutput))
		}
		sid := strings.TrimSpace(m.SessionID)
		return Event{
			Type:      "result",
			Text:      text,
			RawJSON:   mustJSON(m),
			SessionID: sid,
		}, sid, text
	case *claude.StreamEvent:
		text := strings.TrimSpace(collectAnyText(m.Event))
		sid := strings.TrimSpace(m.SessionID)
		return Event{
			Type:      "stream_event",
			Text:      text,
			RawJSON:   mustJSON(m),
			SessionID: sid,
		}, sid, text
	case *claude.SystemMessage:
		text := strings.TrimSpace(collectAnyText(m.Data))
		if text == "" {
			text = strings.TrimSpace(m.Subtype)
		}
		return Event{
			Type:    "system",
			Text:    text,
			RawJSON: mustJSON(m),
		}, "", text
	case *claude.UserMessage:
		text := strings.TrimSpace(collectAnyText(m.Content))
		return Event{
			Type:    "user",
			Text:    text,
			RawJSON: mustJSON(m),
		}, "", text
	case *claude.RateLimitEvent:
		text := strings.TrimSpace(collectAnyText(m.Data))
		return Event{
			Type:    "rate_limit_event",
			Text:    text,
			RawJSON: mustJSON(m),
		}, "", text
	default:
		text := strings.TrimSpace(collectAnyText(msg))
		return Event{
			Type:    "message",
			Text:    text,
			RawJSON: mustJSON(msg),
		}, "", text
	}
}

func extractClaudeAssistantText(m *claude.AssistantMessage) string {
	if m == nil || len(m.Content) == 0 {
		return ""
	}
	parts := make([]string, 0, len(m.Content))
	for _, block := range m.Content {
		switch b := block.(type) {
		case *claude.TextBlock:
			if s := strings.TrimSpace(b.Text); s != "" {
				parts = append(parts, s)
			}
		case *claude.ThinkingBlock:
			if s := strings.TrimSpace(b.Thinking); s != "" {
				parts = append(parts, s)
			}
		case *claude.ToolUseBlock:
			if s := strings.TrimSpace(b.Name); s != "" {
				parts = append(parts, "tool_use:"+s)
			}
		case *claude.ToolResultBlock:
			if s := strings.TrimSpace(collectAnyText(b.Content)); s != "" {
				parts = append(parts, s)
			}
		default:
			if s := strings.TrimSpace(collectAnyText(block)); s != "" {
				parts = append(parts, s)
			}
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

func extractCodexEventText(ev codexsdk.ThreadEvent) string {
	if ev.Item != nil {
		item := ev.Item
		switch item.Type {
		case codexsdk.ItemAgentMessage, codexsdk.ItemReasoning:
			if s := strings.TrimSpace(item.Text); s != "" {
				return s
			}
		case codexsdk.ItemCommandExecution:
			if s := strings.TrimSpace(item.AggregatedOutput); s != "" {
				return s
			}
			if s := strings.TrimSpace(item.Command); s != "" {
				return s
			}
		case codexsdk.ItemError:
			if s := strings.TrimSpace(item.ItemMessage); s != "" {
				return s
			}
		default:
			if s := strings.TrimSpace(item.Text); s != "" {
				return s
			}
		}
	}
	if ev.Error != nil {
		if s := strings.TrimSpace(ev.Error.Message); s != "" {
			return s
		}
	}
	return strings.TrimSpace(ev.Message)
}

func convertGeminiMessage(msg gemini.Message) (Event, string, string) {
	switch m := msg.(type) {
	case *gemini.AssistantMessage:
		eventType := geminiAssistantEventType(m)
		eventText := extractGeminiAssistantEventText(m)
		replyText := extractGeminiAssistantReplyText(m)
		sid := strings.TrimSpace(m.SessionID)
		return Event{
			Type:      eventType,
			Text:      eventText,
			RawJSON:   mustJSON(m),
			SessionID: sid,
		}, sid, replyText
	case *gemini.ResultMessage:
		sid := strings.TrimSpace(m.SessionID)
		eventType := "completed"
		text := strings.TrimSpace(m.StopReason)
		if strings.TrimSpace(m.Error) != "" {
			text = strings.TrimSpace(m.Error)
		}
		if m.IsError {
			eventType = "error"
			if text == "" {
				text = "gemini error"
			}
		} else if text == "" {
			text = "completed"
		}
		return Event{
			Type:      eventType,
			Text:      text,
			RawJSON:   mustJSON(m),
			SessionID: sid,
		}, sid, ""
	default:
		text := strings.TrimSpace(collectAnyText(msg))
		return Event{
			Type:    "message",
			Text:    text,
			RawJSON: mustJSON(msg),
		}, "", text
	}
}

func geminiAssistantEventType(msg *gemini.AssistantMessage) string {
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

func extractGeminiAssistantEventText(msg *gemini.AssistantMessage) string {
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
			if s := strings.TrimSpace(collectAnyText(b.Content)); s != "" {
				parts = append(parts, s)
			} else if s := strings.TrimSpace(b.Name); s != "" {
				parts = append(parts, s)
			}
		default:
			if s := strings.TrimSpace(collectAnyText(block)); s != "" {
				parts = append(parts, s)
			}
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

func extractGeminiAssistantReplyText(msg *gemini.AssistantMessage) string {
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
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

func parseCodexReasoningEffort(raw string) codexsdk.ModelReasoningEffort {
	switch strings.TrimSpace(strings.ToLower(raw)) {
	case string(codexsdk.ReasoningMinimal):
		return codexsdk.ReasoningMinimal
	case string(codexsdk.ReasoningLow):
		return codexsdk.ReasoningLow
	case string(codexsdk.ReasoningMedium):
		return codexsdk.ReasoningMedium
	case string(codexsdk.ReasoningHigh):
		return codexsdk.ReasoningHigh
	case string(codexsdk.ReasoningXHigh):
		return codexsdk.ReasoningXHigh
	default:
		return ""
	}
}

func geminiEnvList(extra map[string]string) []string {
	env := normalizeEnvMap(extra)
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

func autoApproveGeminiTool(ctx context.Context, call gemini.ToolCallInfo, options []gemini.PermissionOption) (string, error) {
	_ = ctx
	_ = call
	return pickGeminiPermissionOption(true, options), nil
}

func pickGeminiPermissionOption(allow bool, options []gemini.PermissionOption) string {
	if allow {
		if id := findGeminiPermissionOptionByPrefix(options, "allow_"); id != "" {
			return id
		}
		if id := findGeminiPermissionOptionByPrefix(options, "ask_"); id != "" {
			return id
		}
	} else {
		if id := findGeminiPermissionOptionByPrefix(options, "reject_"); id != "" {
			return id
		}
		if id := findGeminiPermissionOptionByPrefix(options, "deny_"); id != "" {
			return id
		}
		if id := findGeminiPermissionOptionByPrefix(options, "ask_"); id != "" {
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

func findGeminiPermissionOptionByPrefix(options []gemini.PermissionOption, prefix string) string {
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

func mergeEnvMap(extra map[string]string) map[string]string {
	env := map[string]string{}
	for _, item := range os.Environ() {
		k, v, ok := strings.Cut(item, "=")
		if !ok {
			continue
		}
		key := strings.TrimSpace(k)
		if key == "" {
			continue
		}
		env[key] = v
	}
	for k, v := range extra {
		key := strings.TrimSpace(k)
		if key == "" {
			continue
		}
		env[key] = v
	}
	return normalizeEnvMap(env)
}

func GlobalDalekDir() string {
	home := strings.TrimSpace(os.Getenv("DALEK_HOME"))
	if home == "" {
		userHome, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		home = filepath.Join(userHome, ".dalek")
	}
	if strings.HasPrefix(home, "~"+string(os.PathSeparator)) || home == "~" {
		userHome, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		if home == "~" {
			home = userHome
		} else {
			home = filepath.Join(userHome, strings.TrimPrefix(home, "~"+string(os.PathSeparator)))
		}
	}
	abs, err := filepath.Abs(home)
	if err == nil {
		home = abs
	}
	info, err := os.Stat(home)
	if err != nil || !info.IsDir() {
		return ""
	}
	return strings.TrimSpace(home)
}

func RepoRootFromWorkDir(workDir string) string {
	wd := strings.TrimSpace(workDir)
	if wd == "" {
		return ""
	}
	root, err := repo.FindRepoRoot(wd)
	if err != nil {
		return ""
	}
	root = strings.TrimSpace(root)
	if root == "" {
		return ""
	}
	if abs, err := filepath.Abs(root); err == nil {
		root = abs
	}
	return strings.TrimSpace(root)
}

func tmpDir() string {
	dir := "/private/tmp"
	if info, err := os.Stat(dir); err == nil && info.IsDir() {
		return dir
	}
	return "/tmp"
}

func SDKAdditionalDirectories(workDir string) []string {
	out := make([]string, 0, 2)
	seen := map[string]struct{}{}
	add := func(dir string) {
		d := strings.TrimSpace(dir)
		if d == "" {
			return
		}
		if abs, err := filepath.Abs(d); err == nil {
			d = abs
		}
		if _, ok := seen[d]; ok {
			return
		}
		seen[d] = struct{}{}
		out = append(out, d)
	}
	add(GlobalDalekDir())
	add(RepoRootFromWorkDir(workDir))
	add(tmpDir())
	if len(out) == 0 {
		return nil
	}
	return out
}

func normalizeEnvMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := map[string]string{}
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

func lastNonEmpty(items []string) string {
	for i := len(items) - 1; i >= 0; i-- {
		if s := strings.TrimSpace(items[i]); s != "" {
			return s
		}
	}
	return ""
}

func lastEventText(events []Event) string {
	for i := len(events) - 1; i >= 0; i-- {
		if s := strings.TrimSpace(events[i].Text); s != "" {
			return s
		}
	}
	return ""
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func collectAnyText(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case []string:
		parts := make([]string, 0, len(x))
		for _, it := range x {
			if s := strings.TrimSpace(it); s != "" {
				parts = append(parts, s)
			}
		}
		return strings.Join(parts, "\n")
	case []any:
		parts := make([]string, 0, len(x))
		for _, it := range x {
			if s := strings.TrimSpace(collectAnyText(it)); s != "" {
				parts = append(parts, s)
			}
		}
		return strings.Join(parts, "\n")
	case map[string]any:
		for _, key := range []string{"text", "content", "result", "message", "thinking", "delta", "summary"} {
			if s := strings.TrimSpace(collectAnyText(x[key])); s != "" {
				return s
			}
		}
		return ""
	default:
		if b, err := json.Marshal(x); err == nil {
			return strings.TrimSpace(string(b))
		}
		return ""
	}
}
