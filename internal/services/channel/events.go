package channel

import (
	"strings"
	"time"

	"dalek/internal/agent/eventrender"
	"dalek/internal/services/channel/agentcli"
)

type AgentEventStream string

const (
	StreamLifecycle AgentEventStream = "lifecycle"
	StreamAssistant AgentEventStream = "assistant"
	StreamTool      AgentEventStream = "tool"
	StreamError     AgentEventStream = "error"
)

type AgentEvent struct {
	RunID  string           `json:"run_id"`
	Seq    int              `json:"seq"`
	Stream AgentEventStream `json:"stream"`
	Ts     int64            `json:"ts"`
	Data   AgentEventData   `json:"data"`
}

type AgentEventData struct {
	Phase     string `json:"phase,omitempty"`
	StartedAt int64  `json:"started_at,omitempty"`
	EndedAt   int64  `json:"ended_at,omitempty"`

	Text string `json:"text,omitempty"`
	// RawJSON 保留 SDK/CLI 的原始事件 JSON，供可观测与审计。
	RawJSON string `json:"raw_json,omitempty"`

	Error     string `json:"error,omitempty"`
	ErrorType string `json:"error_type,omitempty"`

	ToolName  string `json:"tool_name,omitempty"`
	ToolInput string `json:"tool_input,omitempty"`
	Detail    string `json:"detail,omitempty"` // 事件完整内容（所有类型均填充，不截断）
}

func SynthesizeEventsFromCLIResult(runID string, startedAt time.Time, cliEvents []agentcli.Event, replyText string, runErr error, provider string) []AgentEvent {
	runID = strings.TrimSpace(runID)
	if runID == "" {
		runID = "run-unknown"
	}
	startMS := startedAt.UnixMilli()
	if startMS <= 0 {
		startMS = time.Now().UnixMilli()
	}

	events := make([]AgentEvent, 0, len(cliEvents)+3)
	seq := 0
	nextSeq := func() int {
		seq++
		return seq
	}

	events = append(events, AgentEvent{
		RunID:  runID,
		Seq:    nextSeq(),
		Stream: StreamLifecycle,
		Ts:     startMS,
		Data: AgentEventData{
			Phase:     "start",
			StartedAt: startMS,
		},
	})

	replyText = strings.TrimSpace(replyText)
	for _, ev := range cliEvents {
		items := buildAgentEventFromCLIEvent(runID, 0, ev, provider)
		for _, item := range items {
			item.Seq = nextSeq()
			if replyText != "" && item.Data.Text == replyText {
				continue
			}
			events = append(events, item)
		}
	}

	endMS := time.Now().UnixMilli()
	if replyText != "" {
		events = append(events, AgentEvent{
			RunID:  runID,
			Seq:    nextSeq(),
			Stream: StreamAssistant,
			Ts:     endMS,
			Data: AgentEventData{
				Text: replyText,
			},
		})
	}

	lifecycle := AgentEvent{
		RunID:  runID,
		Seq:    nextSeq(),
		Stream: StreamLifecycle,
		Ts:     endMS,
		Data: AgentEventData{
			Phase:     "end",
			StartedAt: startMS,
			EndedAt:   endMS,
		},
	}
	if runErr != nil {
		msg := runErr.Error()
		lifecycle.Data.Phase = "error"
		lifecycle.Data.Error = msg
		lifecycle.Data.ErrorType = classifyJobErrorType(msg)
	}
	events = append(events, lifecycle)
	return events
}

func buildAgentEventFromCLIEvent(runID string, seq int, ev agentcli.Event, provider string) []AgentEvent {
	renderer := eventrender.ForProvider(provider)
	steps := renderer.Render(seq, ev.Type, ev.RawJSON, ev.Text)
	if len(steps) == 0 {
		return nil
	}
	events := make([]AgentEvent, 0, len(steps))
	for _, step := range steps {
		events = append(events, mapStepToAgentEvent(runID, step))
	}
	return events
}

func mapStepToAgentEvent(runID string, step eventrender.UnifiedStep) AgentEvent {
	stream := StreamAssistant
	phase := ""
	switch step.StepType {
	case eventrender.StepThinking:
		stream = StreamAssistant
	case eventrender.StepMessage:
		stream = StreamAssistant
		phase = "message"
	case eventrender.StepToolCall, eventrender.StepToolResult:
		stream = StreamTool
	case eventrender.StepError:
		stream = StreamError
	case eventrender.StepLifecycle:
		stream = StreamLifecycle
		phase = "update"
	}
	data := AgentEventData{
		Phase:    phase,
		Text:     step.Summary,
		RawJSON:  step.RawJSON,
		ToolName: step.ToolName,
	}
	if step.Detail != "" {
		data.Detail = step.Detail
		// ToolInput 保持向后兼容：tool 类事件同时填充
		if stream == StreamTool {
			data.ToolInput = step.Detail
		}
	}
	if stream == StreamError {
		data.Error = step.Summary
		data.ErrorType = classifyJobErrorType(step.Summary)
	}
	return AgentEvent{
		RunID:  runID,
		Seq:    step.Seq,
		Stream: stream,
		Ts:     step.Ts,
		Data:   data,
	}
}

func AppendLifecycleErrorEvent(runID string, startedAt time.Time, in []AgentEvent, runErr error) []AgentEvent {
	if runErr == nil {
		return copyAgentEvents(in)
	}
	out := copyAgentEvents(in)
	runID = strings.TrimSpace(runID)
	if runID == "" {
		runID = "run-unknown"
	}
	startMS := startedAt.UnixMilli()
	if startMS <= 0 {
		startMS = time.Now().UnixMilli()
	}
	maxSeq := 0
	for _, ev := range out {
		if ev.Seq > maxSeq {
			maxSeq = ev.Seq
		}
	}
	msg := runErr.Error()
	out = append(out, AgentEvent{
		RunID:  runID,
		Seq:    maxSeq + 1,
		Stream: StreamLifecycle,
		Ts:     time.Now().UnixMilli(),
		Data: AgentEventData{
			Phase:     "error",
			StartedAt: startMS,
			EndedAt:   time.Now().UnixMilli(),
			Error:     msg,
			ErrorType: classifyJobErrorType(msg),
		},
	})
	return out
}

func copyAgentEvents(in []AgentEvent) []AgentEvent {
	if len(in) == 0 {
		return nil
	}
	out := make([]AgentEvent, 0, len(in))
	for _, ev := range in {
		trimmed := AgentEvent{
			RunID:  ev.RunID,
			Seq:    ev.Seq,
			Stream: AgentEventStream(string(ev.Stream)),
			Ts:     ev.Ts,
			Data: AgentEventData{
				Phase:     ev.Data.Phase,
				StartedAt: ev.Data.StartedAt,
				EndedAt:   ev.Data.EndedAt,
				Text:      ev.Data.Text,
				RawJSON:   ev.Data.RawJSON,
				Error:     ev.Data.Error,
				ErrorType: ev.Data.ErrorType,
				ToolName:  ev.Data.ToolName,
				ToolInput: ev.Data.ToolInput,
			},
		}
		if trimmed.RunID == "" {
			continue
		}
		if trimmed.Stream == "" {
			continue
		}
		out = append(out, trimmed)
	}
	return out
}
