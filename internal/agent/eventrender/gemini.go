package eventrender

import (
	"encoding/json"
	"fmt"
	"strings"
)

type geminiRenderer struct{}

type geminiSessionEvent struct {
	Type       string          `json:"type"`
	RawType    string          `json:"raw_type,omitempty"`
	Text       string          `json:"text,omitempty"`
	ToolName   string          `json:"tool_name,omitempty"`
	ToolCallID string          `json:"tool_call_id,omitempty"`
	Done       bool            `json:"done,omitempty"`
	Error      string          `json:"error,omitempty"`
	Data       json.RawMessage `json:"data,omitempty"`
}

func (g geminiRenderer) Render(seq int, eventType string, rawJSON string, text string) []UnifiedStep {
	ts := nowMS()
	switch eventType {
	case "thinking", "agent_thought_chunk", "thought", "thought_chunk":
		full := strings.TrimSpace(text)
		if full == "" {
			full = "thinking"
		}
		return []UnifiedStep{{
			Seq: seq, StepType: StepThinking, Summary: Truncate(full, 120), Detail: full, RawJSON: rawJSON, Ts: ts,
		}}
	case "message", "message_chunk", "agent_message_chunk":
		full := strings.TrimSpace(text)
		if full == "" {
			return nil
		}
		return []UnifiedStep{{
			Seq: seq, StepType: StepMessage, Summary: Truncate(full, 120), Detail: full, RawJSON: rawJSON, Ts: ts,
		}}
	case "tool_call":
		return g.renderToolCall(seq, rawJSON, text, ts)
	case "tool_call_update":
		return g.renderToolResult(seq, rawJSON, text, ts)
	case "completed":
		return []UnifiedStep{{
			Seq: seq, StepType: StepLifecycle, Summary: "[gemini] 轮次结束", RawJSON: rawJSON, Ts: ts,
		}}
	case "error":
		full := strings.TrimSpace(text)
		if full == "" {
			full = "gemini error"
		}
		return []UnifiedStep{{
			Seq: seq, StepType: StepError, Summary: Truncate(full, 120), Detail: full, RawJSON: rawJSON, Ts: ts,
		}}
	default:
		return nil
	}
}

func (g geminiRenderer) renderToolCall(seq int, rawJSON string, text string, ts int64) []UnifiedStep {
	ev := parseGeminiSessionEvent(rawJSON)
	toolName := strings.TrimSpace(ev.ToolName)
	if toolName == "" {
		toolName = "tool_call"
	}
	summary, detail := g.summarizeGeminiTool(toolName, ev.Data, text)
	return []UnifiedStep{{
		Seq: seq, StepType: StepToolCall, Summary: Truncate(summary, 120), Detail: detail, ToolName: toolName, RawJSON: rawJSON, Ts: ts,
	}}
}

func (g geminiRenderer) renderToolResult(seq int, rawJSON string, text string, ts int64) []UnifiedStep {
	ev := parseGeminiSessionEvent(rawJSON)
	toolName := strings.TrimSpace(ev.ToolName)
	if toolName == "" {
		toolName = "tool_call"
	}
	summary, detail := g.summarizeGeminiToolResult(ev.Data, text)
	return []UnifiedStep{{
		Seq: seq, StepType: StepToolResult, Summary: Truncate(summary, 120), Detail: detail, ToolName: toolName, RawJSON: rawJSON, Ts: ts,
	}}
}

func (g geminiRenderer) summarizeGeminiTool(toolName string, data json.RawMessage, fallback string) (string, string) {
	payload := parseGeminiDataObject(data)
	switch strings.ToLower(toolName) {
	case "bash", "shell":
		if cmd := strings.TrimSpace(readGeminiDataString(payload, "command", "cmd")); cmd != "" {
			return "$ " + cmd, "$ " + cmd
		}
	case "read":
		if path := strings.TrimSpace(readGeminiDataString(payload, "file_path", "path")); path != "" {
			return path, path
		}
	}
	if len(payload) > 0 {
		if b, err := json.MarshalIndent(payload, "", "  "); err == nil {
			return toolName + ": " + Truncate(string(b), 120), toolName + ":\n" + string(b)
		}
	}
	full := strings.TrimSpace(fallback)
	if full == "" {
		full = toolName
	}
	return full, full
}

func (g geminiRenderer) summarizeGeminiToolResult(data json.RawMessage, fallback string) (string, string) {
	payload := parseGeminiDataObject(data)
	parts := make([]string, 0, 2)
	if status := strings.TrimSpace(readGeminiDataString(payload, "status")); status != "" {
		parts = append(parts, "status="+status)
	}
	if exitCode, ok := payload["exitCode"]; ok {
		parts = append(parts, fmt.Sprintf("exit=%v", exitCode))
	}
	summary := strings.Join(parts, " ")
	if summary == "" {
		summary = strings.TrimSpace(fallback)
	}
	if summary == "" {
		summary = "tool result"
	}

	detail := strings.TrimSpace(fallback)
	if detail == "" && len(payload) > 0 {
		if output := strings.TrimSpace(readGeminiDataString(payload, "output", "stdout", "stderr")); output != "" {
			detail = output
		} else if b, err := json.MarshalIndent(payload, "", "  "); err == nil {
			detail = string(b)
		}
	}
	return summary, detail
}

func parseGeminiSessionEvent(rawJSON string) geminiSessionEvent {
	if rawJSON == "" {
		return geminiSessionEvent{}
	}
	var ev geminiSessionEvent
	if err := json.Unmarshal([]byte(rawJSON), &ev); err != nil {
		return geminiSessionEvent{}
	}
	return ev
}

func parseGeminiDataObject(raw json.RawMessage) map[string]any {
	if len(raw) == 0 {
		return nil
	}
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil
	}
	return obj
}

func readGeminiDataString(obj map[string]any, keys ...string) string {
	for _, key := range keys {
		if obj == nil {
			return ""
		}
		if s, ok := obj[key].(string); ok {
			return s
		}
	}
	return ""
}
