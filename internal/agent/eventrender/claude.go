package eventrender

import (
	"encoding/json"
	"strings"
)

type claudeRenderer struct{}

// claudeAssistantMsg 用于解析 Claude assistant 事件的 rawJSON。
type claudeAssistantMsg struct {
	Content []json.RawMessage `json:"content"`
	Model   string            `json:"model"`
}

// claudeContentBlock 是 content[0] 的通用表示，按 key 判定类型。
type claudeContentBlock struct {
	// ThinkingBlock
	Thinking  string `json:"thinking,omitempty"`
	Signature string `json:"signature,omitempty"`
	// ToolUseBlock
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`
	// TextBlock
	Text string `json:"text,omitempty"`
}

// claudeToolResult 用于解析 user event 的 text（ToolResult JSON 数组）。
type claudeToolResult struct {
	ToolUseID string `json:"tool_use_id"`
	Content   string `json:"content"`
}

func (c claudeRenderer) Render(seq int, eventType string, rawJSON string, text string) []UnifiedStep {
	ts := nowMS()

	switch eventType {
	case "system":
		return []UnifiedStep{{
			Seq: seq, StepType: StepLifecycle, Summary: "[claude] 会话初始化", RawJSON: rawJSON, Ts: ts,
		}}
	case "result":
		return []UnifiedStep{{
			Seq: seq, StepType: StepLifecycle, Summary: "[claude] 会话结束", RawJSON: rawJSON, Ts: ts,
		}}
	case "stream_event", "rate_limit_event":
		return nil
	case "assistant":
		return c.renderAssistant(seq, rawJSON, text, ts)
	case "user":
		return c.renderUser(seq, rawJSON, text, ts)
	default:
		return nil
	}
}

func (c claudeRenderer) renderAssistant(seq int, rawJSON string, text string, ts int64) []UnifiedStep {
	if rawJSON == "" {
		if text == "" {
			return nil
		}
		return []UnifiedStep{{
			Seq: seq, StepType: StepMessage, Summary: Truncate(text, 120), Detail: text, Ts: ts,
		}}
	}

	var msg claudeAssistantMsg
	if err := json.Unmarshal([]byte(rawJSON), &msg); err != nil || len(msg.Content) == 0 {
		if text == "" {
			return nil
		}
		return []UnifiedStep{{
			Seq: seq, StepType: StepMessage, Summary: Truncate(text, 120), Detail: text, RawJSON: rawJSON, Ts: ts,
		}}
	}

	var block claudeContentBlock
	if err := json.Unmarshal(msg.Content[0], &block); err != nil {
		return nil
	}

	// 按 key 判定 block 类型
	switch {
	case block.Thinking != "":
		return []UnifiedStep{{
			Seq: seq, StepType: StepThinking, Summary: Truncate(block.Thinking, 120), Detail: block.Thinking,
			RawJSON: rawJSON, Ts: ts,
		}}
	case block.Name != "":
		summary := c.buildToolCallSummary(block.Name, block.Input)
		detail := c.buildToolCallDetail(block.Name, block.Input)
		return []UnifiedStep{{
			Seq: seq, StepType: StepToolCall, Summary: Truncate(summary, 120), Detail: detail,
			ToolName: block.Name, RawJSON: rawJSON, Ts: ts,
		}}
	case block.Text != "":
		// Claude SDK 的 text-only assistant 事件即最终回复，
		// 由 AppendAssistantText 单独处理；此处不渲染，避免重复。
		return nil
	default:
		return nil
	}
}

func (c claudeRenderer) renderUser(seq int, rawJSON string, text string, ts int64) []UnifiedStep {
	if text == "" {
		return nil
	}
	// 检测是否为 ToolResult（text 是 JSON 数组且含 tool_use_id）
	if !strings.Contains(text, "tool_use_id") {
		return nil
	}
	var results []claudeToolResult
	if err := json.Unmarshal([]byte(text), &results); err != nil || len(results) == 0 {
		return nil
	}
	content := results[0].Content
	return []UnifiedStep{{
		Seq: seq, StepType: StepToolResult, Summary: Truncate(content, 120),
		Detail: content, RawJSON: rawJSON, Ts: ts,
	}}
}

func (c claudeRenderer) buildToolCallDetail(name string, inputRaw json.RawMessage) string {
	if len(inputRaw) == 0 {
		return name
	}
	var input map[string]any
	if err := json.Unmarshal(inputRaw, &input); err != nil {
		return name
	}

	switch name {
	case "Bash":
		if cmd, ok := input["command"].(string); ok {
			return "$ " + cmd
		}
	case "Read":
		if fp, ok := input["file_path"].(string); ok {
			return fp
		}
	case "Write", "Edit":
		b, err := json.MarshalIndent(input, "", "  ")
		if err != nil {
			return name
		}
		return name + ":\n" + string(b)
	}

	b, err := json.MarshalIndent(input, "", "  ")
	if err != nil {
		return name
	}
	return name + ":\n" + string(b)
}

func (c claudeRenderer) buildToolCallSummary(name string, inputRaw json.RawMessage) string {
	if len(inputRaw) == 0 {
		return name
	}
	var input map[string]any
	if err := json.Unmarshal(inputRaw, &input); err != nil {
		return name
	}

	switch name {
	case "Bash":
		if cmd, ok := input["command"].(string); ok {
			return "$ " + cmd
		}
	case "Read":
		if fp, ok := input["file_path"].(string); ok {
			return fp
		}
	case "Grep":
		if p, ok := input["pattern"].(string); ok {
			return "grep: " + p
		}
	case "Glob":
		if p, ok := input["pattern"].(string); ok {
			return "glob: " + p
		}
	case "Write":
		if fp, ok := input["file_path"].(string); ok {
			return "write: " + fp
		}
	case "Edit":
		if fp, ok := input["file_path"].(string); ok {
			return "edit: " + fp
		}
	case "Task":
		if desc, ok := input["description"].(string); ok {
			return desc
		}
	}

	// 其他工具：name + json(input) 截断
	b, err := json.Marshal(input)
	if err != nil {
		return name
	}
	return name + ": " + Truncate(string(b), 200)
}
