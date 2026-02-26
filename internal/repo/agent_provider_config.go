package repo

import "dalek/internal/agent/provider"

// AgentConfigFromExecConfig 将 repo 层执行配置统一转换为 provider.AgentConfig。
func AgentConfigFromExecConfig(exec AgentExecConfig) provider.AgentConfig {
	normalized := normalizeAgentExecConfig(exec)
	return provider.AgentConfig{
		Provider:        normalized.Provider,
		Model:           normalized.Model,
		ReasoningEffort: normalized.ReasoningEffort,
		ExtraFlags:      append([]string(nil), normalized.ExtraFlags...),
		Command:         normalized.Command,
	}.Normalize()
}
