package provider

import (
	"strings"
)

// Permission 常量
const (
	PermissionAuto   = "auto"   // 受限权限，需要审批
	PermissionBypass = "bypass" // 最大权限，跳过审批和沙箱
)

// AgentConfig 是 agent provider 的结构化配置。
type AgentConfig struct {
	Provider          string   `json:"provider"`
	Model             string   `json:"model,omitempty"`
	ReasoningEffort   string   `json:"reasoning_effort,omitempty"`
	ExtraFlags        []string `json:"extra_flags,omitempty"`
	Permission        string   `json:"permission,omitempty"` // "auto" | "bypass", 默认 "auto"
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
	out.Permission = normalizePermission(out.Permission)

	// 根据 Permission 字段设置 provider-specific 权限标记
	resolvePermissionFlags(&out)

	switch out.Provider {
	case ProviderCodex:
		// codex 不使用 BypassPermissions
		out.BypassPermissions = false
	case ProviderClaude:
		out.ReasoningEffort = ""
		// claude 不使用 DangerFullAccess
		out.DangerFullAccess = false
	case ProviderGemini:
		out.ReasoningEffort = ""
		// gemini 不使用 DangerFullAccess（BypassPermissions 控制 --approval-mode yolo）
		out.DangerFullAccess = false
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
	return n.Provider == "" && n.Model == "" && n.ReasoningEffort == "" && n.Command == "" &&
		len(n.ExtraFlags) == 0 && !n.DangerFullAccess && !n.BypassPermissions &&
		n.Permission == PermissionAuto
}

// normalizePermission 规范化 permission 字段。
func normalizePermission(raw string) string {
	switch strings.TrimSpace(strings.ToLower(raw)) {
	case PermissionBypass:
		return PermissionBypass
	default:
		return PermissionAuto
	}
}

// resolvePermissionFlags 根据 Permission 字段设置 DangerFullAccess/BypassPermissions。
// 这是 permission → provider-specific flags 的统一映射。
func resolvePermissionFlags(cfg *AgentConfig) {
	if cfg == nil {
		return
	}
	if cfg.Permission != PermissionBypass {
		cfg.DangerFullAccess = false
		cfg.BypassPermissions = false
		return
	}
	switch cfg.Provider {
	case ProviderCodex:
		cfg.DangerFullAccess = true
		cfg.BypassPermissions = false
	case ProviderClaude:
		cfg.DangerFullAccess = false
		cfg.BypassPermissions = true
	case ProviderGemini:
		// Gemini 通过 BypassPermissions 控制 --approval-mode yolo
		cfg.DangerFullAccess = false
		cfg.BypassPermissions = true
	default:
		cfg.DangerFullAccess = false
		cfg.BypassPermissions = false
	}
}
