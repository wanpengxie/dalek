package provider

import (
	"strings"
)

// AgentConfig 是 agent provider 的结构化配置。
type AgentConfig struct {
	Provider          string   `json:"provider"`
	Model             string   `json:"model,omitempty"`
	ReasoningEffort   string   `json:"reasoning_effort,omitempty"`
	ExtraFlags        []string `json:"extra_flags,omitempty"`
	DangerFullAccess  bool     `json:"danger_full_access,omitempty"`
	BypassPermissions bool     `json:"bypass_permissions,omitempty"`
	Command           string   `json:"command,omitempty"`
}

func (c AgentConfig) Normalize() AgentConfig {
	out := c
	out.Provider = strings.TrimSpace(strings.ToLower(out.Provider))
	out.Model = strings.TrimSpace(out.Model)
	out.ReasoningEffort = strings.TrimSpace(out.ReasoningEffort)
	out.Command = strings.TrimSpace(out.Command)
	switch out.Provider {
	case ProviderCodex:
		out.BypassPermissions = false
	case ProviderClaude:
		out.ReasoningEffort = ""
		out.DangerFullAccess = false
	case ProviderGemini:
		out.ReasoningEffort = ""
		out.DangerFullAccess = false
		out.BypassPermissions = false
	default:
		out.DangerFullAccess = false
		out.BypassPermissions = false
	}
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
	return n.Provider == "" && n.Model == "" && n.ReasoningEffort == "" && n.Command == "" && len(n.ExtraFlags) == 0 && !n.DangerFullAccess && !n.BypassPermissions
}
