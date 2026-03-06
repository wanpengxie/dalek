package eventrender

import (
	"time"

	"dalek/internal/agent/provider"
)

// StepType 表示统一步骤的类型。
type StepType string

const (
	StepThinking   StepType = "thinking"
	StepToolCall   StepType = "tool_call"
	StepToolResult StepType = "tool_result"
	StepMessage    StepType = "message"
	StepError      StepType = "error"
	StepLifecycle  StepType = "lifecycle"
)

// UnifiedStep 是 SDK 原始事件渲染后的统一步骤。
type UnifiedStep struct {
	Seq      int      `json:"seq"`
	StepType StepType `json:"step_type"`
	Summary  string   `json:"summary"`
	Detail   string   `json:"detail,omitempty"`
	ToolName string   `json:"tool_name,omitempty"`
	RawJSON  string   `json:"raw_json,omitempty"`
	Ts       int64    `json:"ts"`
}

// Renderer 将一条 SDK 原始事件转换为 0~N 个 UnifiedStep。
// 返回 nil/空 slice 表示该事件应丢弃。
type Renderer interface {
	Render(seq int, eventType string, rawJSON string, text string) []UnifiedStep
}

// ForProvider 返回对应 provider 的 Renderer。
func ForProvider(providerName string) Renderer {
	switch provider.NormalizeProvider(providerName) {
	case provider.ProviderClaude:
		return claudeRenderer{}
	case provider.ProviderCodex:
		return codexRenderer{}
	case provider.ProviderGemini:
		return geminiRenderer{}
	default:
		return fallbackRenderer{}
	}
}

// Truncate 截断字符串到 max 字符，超出加 "..."。
func Truncate(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "..."
}

func nowMS() int64 {
	return time.Now().UnixMilli()
}

// fallbackRenderer 对未知 provider 直接返回 text 作为 message。
type fallbackRenderer struct{}

func (f fallbackRenderer) Render(seq int, eventType string, rawJSON string, text string) []UnifiedStep {
	if text == "" && rawJSON == "" {
		return nil
	}
	summary := text
	if summary == "" {
		summary = eventType
	}
	return []UnifiedStep{{
		Seq:      seq,
		StepType: StepMessage,
		Summary:  Truncate(summary, 120),
		Detail:   summary,
		RawJSON:  rawJSON,
		Ts:       nowMS(),
	}}
}
