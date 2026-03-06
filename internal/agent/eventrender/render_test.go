package eventrender

import (
	"testing"
)

// --- Codex Tests ---

func TestCodexReasoning(t *testing.T) {
	r := ForProvider("codex")
	raw := `{"type":"item.completed","item":{"id":"item_0","type":"reasoning","text":"**准备检查代理配置**\n\n用户要求我先检查 AGENTS.md 文件。"}}`
	steps := r.Render(1, "item.completed", raw, "")
	if len(steps) != 1 {
		t.Fatalf("expected 1 step, got %d", len(steps))
	}
	if steps[0].StepType != StepThinking {
		t.Errorf("expected thinking, got %s", steps[0].StepType)
	}
	if steps[0].Seq != 1 {
		t.Errorf("expected seq=1, got %d", steps[0].Seq)
	}
}

func TestCodexAgentMessage(t *testing.T) {
	r := ForProvider("codex")
	raw := `{"type":"item.completed","item":{"id":"item_1","type":"agent_message","text":"我会先按仓库约束加载上下文。"}}`
	steps := r.Render(2, "item.completed", raw, "")
	if len(steps) != 1 {
		t.Fatalf("expected 1 step, got %d", len(steps))
	}
	if steps[0].StepType != StepMessage {
		t.Errorf("expected message, got %s", steps[0].StepType)
	}
}

func TestCodexCommandExecution(t *testing.T) {
	r := ForProvider("codex")
	raw := `{"type":"item.completed","item":{"id":"item_3","type":"command_execution","command":"/bin/zsh -lc 'cat .dalek/config.json'","aggregated_output":"cat: .dalek/config.json: No such file or directory\n","exit_code":1,"status":"failed"}}`
	steps := r.Render(3, "item.completed", raw, "")
	if len(steps) != 2 {
		t.Fatalf("expected 2 steps (tool_call + tool_result), got %d", len(steps))
	}
	if steps[0].StepType != StepToolCall {
		t.Errorf("step[0] expected tool_call, got %s", steps[0].StepType)
	}
	if steps[0].Seq != 3 {
		t.Errorf("step[0] expected seq=3, got %d", steps[0].Seq)
	}
	if steps[1].StepType != StepToolResult {
		t.Errorf("step[1] expected tool_result, got %s", steps[1].StepType)
	}
	if steps[1].Seq != 4 {
		t.Errorf("step[1] expected seq=4 (seq+1), got %d", steps[1].Seq)
	}
	if steps[0].ToolName != "command_execution" {
		t.Errorf("expected tool_name=command_execution, got %s", steps[0].ToolName)
	}
}

func TestCodexItemStartedDiscard(t *testing.T) {
	r := ForProvider("codex")
	steps := r.Render(1, "item.started", `{"type":"item.started","item":{"id":"item_0","type":"reasoning"}}`, "")
	if len(steps) != 0 {
		t.Errorf("expected discard (0 steps), got %d", len(steps))
	}
}

func TestCodexItemUpdatedDiscard(t *testing.T) {
	r := ForProvider("codex")
	steps := r.Render(1, "item.updated", `{"type":"item.updated"}`, "")
	if len(steps) != 0 {
		t.Errorf("expected discard (0 steps), got %d", len(steps))
	}
}

func TestCodexThreadStarted(t *testing.T) {
	r := ForProvider("codex")
	steps := r.Render(1, "thread.started", `{"type":"thread.started","thread_id":"019c80c7-abcd"}`, "")
	if len(steps) != 1 {
		t.Fatalf("expected 1 step, got %d", len(steps))
	}
	if steps[0].StepType != StepLifecycle {
		t.Errorf("expected lifecycle, got %s", steps[0].StepType)
	}
	if steps[0].Summary != "[codex] 会话开始" {
		t.Errorf("unexpected summary: %s", steps[0].Summary)
	}
}

func TestCodexTurnCompleted(t *testing.T) {
	r := ForProvider("codex")
	steps := r.Render(1, "turn.completed", `{"type":"turn.completed"}`, "")
	if len(steps) != 1 {
		t.Fatalf("expected 1 step, got %d", len(steps))
	}
	if steps[0].StepType != StepLifecycle {
		t.Errorf("expected lifecycle, got %s", steps[0].StepType)
	}
	if steps[0].Summary != "[codex] 轮次结束" {
		t.Errorf("unexpected summary: %s", steps[0].Summary)
	}
}

func TestCodexError(t *testing.T) {
	r := ForProvider("codex")
	raw := `{"type":"item.completed","item":{"id":"item_5","type":"error","message":"rate limit exceeded"}}`
	steps := r.Render(1, "item.completed", raw, "")
	if len(steps) != 1 {
		t.Fatalf("expected 1 step, got %d", len(steps))
	}
	if steps[0].StepType != StepError {
		t.Errorf("expected error, got %s", steps[0].StepType)
	}
}

// --- Claude Tests ---

func TestClaudeThinkingBlock(t *testing.T) {
	r := ForProvider("claude")
	raw := `{"content":[{"thinking":"The user wants to verify the git status and branch setup.","signature":"EtQCabcdef"}],"model":"claude-opus-4-6"}`
	steps := r.Render(1, "assistant", raw, "")
	if len(steps) != 1 {
		t.Fatalf("expected 1 step, got %d", len(steps))
	}
	if steps[0].StepType != StepThinking {
		t.Errorf("expected thinking, got %s", steps[0].StepType)
	}
	if steps[0].Seq != 1 {
		t.Errorf("expected seq=1, got %d", steps[0].Seq)
	}
}

func TestClaudeToolUseBlockBash(t *testing.T) {
	r := ForProvider("claude")
	raw := `{"content":[{"id":"toolu_01DZb1abc","name":"Bash","input":{"command":"git log --all --oneline -20","description":"View recent git commit history"}}],"model":"claude-opus-4-6"}`
	steps := r.Render(2, "assistant", raw, "")
	if len(steps) != 1 {
		t.Fatalf("expected 1 step, got %d", len(steps))
	}
	if steps[0].StepType != StepToolCall {
		t.Errorf("expected tool_call, got %s", steps[0].StepType)
	}
	if steps[0].ToolName != "Bash" {
		t.Errorf("expected tool_name=Bash, got %s", steps[0].ToolName)
	}
	expected := "$ git log --all --oneline -20"
	if steps[0].Summary != expected {
		t.Errorf("expected summary=%q, got %q", expected, steps[0].Summary)
	}
}

func TestClaudeToolUseBlockRead(t *testing.T) {
	r := ForProvider("claude")
	raw := `{"content":[{"id":"toolu_01abc","name":"Read","input":{"file_path":"/Users/test/main.go"}}],"model":"claude-opus-4-6"}`
	steps := r.Render(3, "assistant", raw, "")
	if len(steps) != 1 {
		t.Fatalf("expected 1 step, got %d", len(steps))
	}
	if steps[0].StepType != StepToolCall {
		t.Errorf("expected tool_call, got %s", steps[0].StepType)
	}
	if steps[0].Summary != "/Users/test/main.go" {
		t.Errorf("unexpected summary: %s", steps[0].Summary)
	}
}

func TestClaudeTextBlock(t *testing.T) {
	r := ForProvider("claude")
	raw := `{"content":[{"text":"你好！今天有什么可以帮你的吗？"}],"model":"claude-opus-4-6"}`
	steps := r.Render(4, "assistant", raw, "")
	// text-only assistant 事件是最终回复，由 AppendAssistantText 处理，renderer 应返回 nil。
	if len(steps) != 0 {
		t.Fatalf("expected 0 steps for text-only assistant, got %d", len(steps))
	}
}

func TestClaudeUserToolResult(t *testing.T) {
	r := ForProvider("claude")
	text := `[{"tool_use_id":"toolu_01Rjc6abc","content":"cca5818 feat(channel): 接入EventBus流转"}]`
	steps := r.Render(5, "user", "", text)
	if len(steps) != 1 {
		t.Fatalf("expected 1 step, got %d", len(steps))
	}
	if steps[0].StepType != StepToolResult {
		t.Errorf("expected tool_result, got %s", steps[0].StepType)
	}
}

func TestClaudeStreamEventDiscard(t *testing.T) {
	r := ForProvider("claude")
	steps := r.Render(1, "stream_event", `{"delta":"some partial"}`, "")
	if len(steps) != 0 {
		t.Errorf("expected discard (0 steps), got %d", len(steps))
	}
}

func TestClaudeRateLimitEventDiscard(t *testing.T) {
	r := ForProvider("claude")
	steps := r.Render(1, "rate_limit_event", `{"requests_remaining":95}`, "")
	if len(steps) != 0 {
		t.Errorf("expected discard (0 steps), got %d", len(steps))
	}
}

func TestClaudeSystem(t *testing.T) {
	r := ForProvider("claude")
	steps := r.Render(1, "system", `{"system":"init"}`, "")
	if len(steps) != 1 {
		t.Fatalf("expected 1 step, got %d", len(steps))
	}
	if steps[0].StepType != StepLifecycle {
		t.Errorf("expected lifecycle, got %s", steps[0].StepType)
	}
	if steps[0].Summary != "[claude] 会话初始化" {
		t.Errorf("unexpected summary: %s", steps[0].Summary)
	}
}

func TestClaudeResult(t *testing.T) {
	r := ForProvider("claude")
	steps := r.Render(1, "result", `{"result":"done","session_id":"sess-123"}`, "")
	if len(steps) != 1 {
		t.Fatalf("expected 1 step, got %d", len(steps))
	}
	if steps[0].StepType != StepLifecycle {
		t.Errorf("expected lifecycle, got %s", steps[0].StepType)
	}
	if steps[0].Summary != "[claude] 会话结束" {
		t.Errorf("unexpected summary: %s", steps[0].Summary)
	}
}

func TestClaudeUserNoToolResultDiscard(t *testing.T) {
	r := ForProvider("claude")
	steps := r.Render(1, "user", "", "some user message without tool_use_id")
	if len(steps) != 0 {
		t.Errorf("expected discard (0 steps), got %d", len(steps))
	}
}

// --- General Tests ---

func TestForProviderClaude(t *testing.T) {
	r := ForProvider("claude")
	if _, ok := r.(claudeRenderer); !ok {
		t.Errorf("expected claudeRenderer, got %T", r)
	}
}

func TestForProviderCodex(t *testing.T) {
	r := ForProvider("codex")
	if _, ok := r.(codexRenderer); !ok {
		t.Errorf("expected codexRenderer, got %T", r)
	}
}

func TestForProviderGemini(t *testing.T) {
	r := ForProvider("gemini")
	if _, ok := r.(geminiRenderer); !ok {
		t.Errorf("expected geminiRenderer, got %T", r)
	}
}

func TestForProviderUnknown(t *testing.T) {
	r := ForProvider("unknown")
	if _, ok := r.(fallbackRenderer); !ok {
		t.Errorf("expected fallbackRenderer, got %T", r)
	}
	steps := r.Render(1, "anything", "", "hello world")
	if len(steps) != 1 {
		t.Fatalf("expected 1 step from fallback, got %d", len(steps))
	}
	if steps[0].StepType != StepMessage {
		t.Errorf("expected message from fallback, got %s", steps[0].StepType)
	}
}

func TestTruncate(t *testing.T) {
	tests := []struct {
		input    string
		max      int
		expected string
	}{
		{"hello", 10, "hello"},
		{"hello", 5, "hello"},
		{"hello world", 5, "hello..."},
		{"", 10, ""},
		{"abc", 0, "..."},
		{"你好世界", 2, "你好..."},
	}
	for _, tt := range tests {
		got := Truncate(tt.input, tt.max)
		if got != tt.expected {
			t.Errorf("Truncate(%q, %d) = %q, want %q", tt.input, tt.max, got, tt.expected)
		}
	}
}

func TestEmptyRawJSONNoPanic(t *testing.T) {
	for _, provider := range []string{"claude", "codex", "gemini", "unknown"} {
		r := ForProvider(provider)
		// Should not panic
		_ = r.Render(1, "item.completed", "", "")
		_ = r.Render(1, "assistant", "", "")
		_ = r.Render(1, "user", "", "")
	}
}

func TestInvalidJSONNoPanic(t *testing.T) {
	for _, provider := range []string{"claude", "codex", "gemini", "unknown"} {
		r := ForProvider(provider)
		// Should not panic
		_ = r.Render(1, "item.completed", "{invalid json!!!", "fallback text")
		_ = r.Render(1, "assistant", "not json at all", "fallback text")
		_ = r.Render(1, "user", "broken", "fallback text")
	}
}

// --- Additional edge case tests ---

func TestClaudeToolUseBlockGrep(t *testing.T) {
	r := ForProvider("claude")
	raw := `{"content":[{"id":"toolu_01x","name":"Grep","input":{"pattern":"func main","path":"/src"}}],"model":"claude-opus-4-6"}`
	steps := r.Render(1, "assistant", raw, "")
	if len(steps) != 1 {
		t.Fatalf("expected 1 step, got %d", len(steps))
	}
	if steps[0].Summary != "grep: func main" {
		t.Errorf("unexpected summary: %s", steps[0].Summary)
	}
}

func TestClaudeToolUseBlockGlob(t *testing.T) {
	r := ForProvider("claude")
	raw := `{"content":[{"id":"toolu_01x","name":"Glob","input":{"pattern":"**/*.go"}}],"model":"claude-opus-4-6"}`
	steps := r.Render(1, "assistant", raw, "")
	if len(steps) != 1 {
		t.Fatalf("expected 1 step, got %d", len(steps))
	}
	if steps[0].Summary != "glob: **/*.go" {
		t.Errorf("unexpected summary: %s", steps[0].Summary)
	}
}

func TestClaudeToolUseBlockWrite(t *testing.T) {
	r := ForProvider("claude")
	raw := `{"content":[{"id":"toolu_01x","name":"Write","input":{"file_path":"/tmp/test.go","content":"package main"}}],"model":"claude-opus-4-6"}`
	steps := r.Render(1, "assistant", raw, "")
	if len(steps) != 1 {
		t.Fatalf("expected 1 step, got %d", len(steps))
	}
	if steps[0].Summary != "write: /tmp/test.go" {
		t.Errorf("unexpected summary: %s", steps[0].Summary)
	}
}

func TestGeminiMessageChunkRender(t *testing.T) {
	r := ForProvider("gemini")
	raw := `{"type":"message_chunk","raw_type":"agent_message_chunk","text":"hello"}`
	steps := r.Render(1, "agent_message_chunk", raw, "hello")
	if len(steps) != 1 {
		t.Fatalf("expected 1 step, got %d", len(steps))
	}
	if steps[0].StepType != StepMessage {
		t.Fatalf("unexpected step type: %s", steps[0].StepType)
	}
	if steps[0].Detail != "hello" {
		t.Fatalf("unexpected detail: %q", steps[0].Detail)
	}
}

func TestGeminiToolCallRender(t *testing.T) {
	r := ForProvider("gemini")
	raw := `{"type":"tool_call","tool_name":"bash","data":{"command":"go test ./..."}}`
	steps := r.Render(1, "tool_call", raw, "")
	if len(steps) != 1 {
		t.Fatalf("expected 1 step, got %d", len(steps))
	}
	if steps[0].StepType != StepToolCall {
		t.Fatalf("unexpected step type: %s", steps[0].StepType)
	}
	if steps[0].Summary != "$ go test ./..." {
		t.Fatalf("unexpected summary: %q", steps[0].Summary)
	}
}

func TestClaudeToolUseBlockEdit(t *testing.T) {
	r := ForProvider("claude")
	raw := `{"content":[{"id":"toolu_01x","name":"Edit","input":{"file_path":"/tmp/test.go","old_string":"a","new_string":"b"}}],"model":"claude-opus-4-6"}`
	steps := r.Render(1, "assistant", raw, "")
	if len(steps) != 1 {
		t.Fatalf("expected 1 step, got %d", len(steps))
	}
	if steps[0].Summary != "edit: /tmp/test.go" {
		t.Errorf("unexpected summary: %s", steps[0].Summary)
	}
}

func TestClaudeToolUseBlockTask(t *testing.T) {
	r := ForProvider("claude")
	raw := `{"content":[{"id":"toolu_01x","name":"Task","input":{"description":"Search for auth code"}}],"model":"claude-opus-4-6"}`
	steps := r.Render(1, "assistant", raw, "")
	if len(steps) != 1 {
		t.Fatalf("expected 1 step, got %d", len(steps))
	}
	if steps[0].Summary != "Search for auth code" {
		t.Errorf("unexpected summary: %s", steps[0].Summary)
	}
}

func TestClaudeToolUseBlockOther(t *testing.T) {
	r := ForProvider("claude")
	raw := `{"content":[{"id":"toolu_01x","name":"WebSearch","input":{"query":"golang generics"}}],"model":"claude-opus-4-6"}`
	steps := r.Render(1, "assistant", raw, "")
	if len(steps) != 1 {
		t.Fatalf("expected 1 step, got %d", len(steps))
	}
	if steps[0].ToolName != "WebSearch" {
		t.Errorf("expected tool_name=WebSearch, got %s", steps[0].ToolName)
	}
}

func TestCodexUnknownEventTypeDiscard(t *testing.T) {
	r := ForProvider("codex")
	steps := r.Render(1, "some_future_event", `{"type":"some_future_event"}`, "")
	if len(steps) != 0 {
		t.Errorf("expected discard (0 steps), got %d", len(steps))
	}
}

func TestClaudeUnknownEventTypeDiscard(t *testing.T) {
	r := ForProvider("claude")
	steps := r.Render(1, "some_future_event", `{"type":"some_future_event"}`, "")
	if len(steps) != 0 {
		t.Errorf("expected discard (0 steps), got %d", len(steps))
	}
}
