package provider

import (
	"fmt"
	"strings"
)

// AgentConfig 是 agent provider 的结构化配置。
type AgentConfig struct {
	Provider        string   `json:"provider"`
	Model           string   `json:"model,omitempty"`
	ReasoningEffort string   `json:"reasoning_effort,omitempty"`
	ExtraFlags      []string `json:"extra_flags,omitempty"`
	Command         string   `json:"command,omitempty"`
}

func (c AgentConfig) Normalize() AgentConfig {
	out := c
	out.Provider = strings.TrimSpace(strings.ToLower(out.Provider))
	out.Model = strings.TrimSpace(out.Model)
	out.ReasoningEffort = strings.TrimSpace(out.ReasoningEffort)
	out.Command = strings.TrimSpace(out.Command)
	if len(out.ExtraFlags) > 0 {
		flags := make([]string, 0, len(out.ExtraFlags))
		for _, f := range out.ExtraFlags {
			f = strings.TrimSpace(f)
			if f == "" {
				continue
			}
			flags = append(flags, f)
		}
		out.ExtraFlags = flags
	}
	return out
}

func (c AgentConfig) IsZero() bool {
	n := c.Normalize()
	return n.Provider == "" && n.Model == "" && n.ReasoningEffort == "" && n.Command == "" && len(n.ExtraFlags) == 0
}

// NewFromConfig 根据结构化配置构造 provider。
func NewFromConfig(cfg AgentConfig) (Provider, error) {
	cfg = cfg.Normalize()
	if cfg.Provider == "" {
		return nil, fmt.Errorf("agent provider 为空，仅支持 codex/claude")
	}
	switch cfg.Provider {
	case "codex":
		return CodexProvider{
			Command:         cfg.Command,
			Model:           cfg.Model,
			ReasoningEffort: cfg.ReasoningEffort,
			ExtraFlags:      append([]string(nil), cfg.ExtraFlags...),
		}, nil
	case "claude":
		return ClaudeProvider{
			Command:    cfg.Command,
			Model:      cfg.Model,
			ExtraFlags: append([]string(nil), cfg.ExtraFlags...),
		}, nil
	default:
		return nil, fmt.Errorf("unknown agent provider: %s（仅支持 codex/claude）", cfg.Provider)
	}
}
