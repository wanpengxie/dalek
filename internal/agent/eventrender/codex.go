package eventrender

import (
	"encoding/json"
	"fmt"
	"strings"
)

type codexRenderer struct{}

// codexItemCompleted 用于解析 Codex item.completed 事件的 rawJSON。
type codexItemCompleted struct {
	Type string    `json:"type"`
	Item codexItem `json:"item"`
}

type codexItem struct {
	ID               string `json:"id"`
	Type             string `json:"type"`
	Text             string `json:"text"`
	Message          string `json:"message"`
	Command          string `json:"command"`
	AggregatedOutput string `json:"aggregated_output"`
	ExitCode         int    `json:"exit_code"`
	Status           string `json:"status"`
}

func (c codexRenderer) Render(seq int, eventType string, rawJSON string, text string) []UnifiedStep {
	ts := nowMS()

	switch eventType {
	case "thread.started":
		return []UnifiedStep{{
			Seq: seq, StepType: StepLifecycle, Summary: "[codex] 会话开始", RawJSON: rawJSON, Ts: ts,
		}}
	case "turn.started":
		return []UnifiedStep{{
			Seq: seq, StepType: StepLifecycle, Summary: "[codex] 轮次开始", RawJSON: rawJSON, Ts: ts,
		}}
	case "turn.completed":
		return []UnifiedStep{{
			Seq: seq, StepType: StepLifecycle, Summary: "[codex] 轮次结束", RawJSON: rawJSON, Ts: ts,
		}}
	case "item.started", "item.updated":
		return nil
	case "item.completed":
		return c.renderItemCompleted(seq, rawJSON, text, ts)
	default:
		return nil
	}
}

func (c codexRenderer) renderItemCompleted(seq int, rawJSON string, text string, ts int64) []UnifiedStep {
	var ev codexItemCompleted
	if rawJSON != "" {
		if err := json.Unmarshal([]byte(rawJSON), &ev); err != nil {
			return c.fallbackFromText(seq, text, rawJSON, ts)
		}
	} else {
		return c.fallbackFromText(seq, text, rawJSON, ts)
	}

	switch ev.Item.Type {
	case "reasoning":
		full := ev.Item.Text
		if full == "" {
			full = text
		}
		return []UnifiedStep{{
			Seq: seq, StepType: StepThinking, Summary: Truncate(full, 120), Detail: full, RawJSON: rawJSON, Ts: ts,
		}}

	case "agent_message":
		full := ev.Item.Text
		if full == "" {
			full = text
		}
		return []UnifiedStep{{
			Seq: seq, StepType: StepMessage, Summary: Truncate(full, 120), Detail: full, RawJSON: rawJSON, Ts: ts,
		}}

	case "command_execution":
		cmd := ev.Item.Command
		output := strings.TrimSpace(ev.Item.AggregatedOutput)
		callSummary := "$ " + Truncate(cmd, 120)
		resultSummary := fmt.Sprintf("exit=%d", ev.Item.ExitCode)
		return []UnifiedStep{
			{
				Seq: seq, StepType: StepToolCall, Summary: callSummary, Detail: "$ " + cmd,
				ToolName: "command_execution", RawJSON: rawJSON, Ts: ts,
			},
			{
				Seq: seq + 1, StepType: StepToolResult, Summary: resultSummary, Detail: output,
				ToolName: "command_execution", RawJSON: rawJSON, Ts: ts,
			},
		}

	case "error":
		full := ev.Item.Message
		if full == "" {
			full = ev.Item.Text
		}
		if full == "" {
			full = text
		}
		return []UnifiedStep{{
			Seq: seq, StepType: StepError, Summary: Truncate(full, 120), Detail: full, RawJSON: rawJSON, Ts: ts,
		}}

	default:
		return nil
	}
}

func (c codexRenderer) fallbackFromText(seq int, text string, rawJSON string, ts int64) []UnifiedStep {
	if text == "" {
		return nil
	}
	return []UnifiedStep{{
		Seq: seq, StepType: StepMessage, Summary: Truncate(text, 120), Detail: text, RawJSON: rawJSON, Ts: ts,
	}}
}
