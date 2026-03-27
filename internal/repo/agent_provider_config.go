package repo

import (
	"dalek/internal/agent/provider"
	"fmt"
	"strings"
)

// ResolveAgentConfig 根据 provider key 和 providers map 解析出 provider.AgentConfig。
// 如果 key 在 providers map 中不存在但是合法的 provider 类型名，回退到该类型的默认配置。
func ResolveAgentConfig(providerKey string, providers map[string]ProviderConfig) (provider.AgentConfig, error) {
	providerKey = strings.TrimSpace(providerKey)
	if providerKey == "" {
		return provider.AgentConfig{}, fmt.Errorf("provider key 不能为空")
	}
	pc, ok := providers[providerKey]
	if !ok {
		// Fallback: 把 key 当作 provider 类型名
		normalized := provider.NormalizeProvider(providerKey)
		if provider.IsSupportedProvider(normalized) {
			return provider.AgentConfig{
				Provider:        normalized,
				Model:           provider.DefaultModel(normalized),
				ReasoningEffort: provider.DefaultReasoningEffort(normalized),
			}.Normalize(), nil
		}
		keys := make([]string, 0, len(providers))
		for k := range providers {
			keys = append(keys, k)
		}
		return provider.AgentConfig{}, fmt.Errorf(
			"provider %q 未在 providers map 中定义（可用: %s）",
			providerKey, strings.Join(keys, ", "),
		)
	}
	providerType := provider.NormalizeProvider(pc.Type)
	if providerType == "" {
		// type 为空时把 key 当类型名
		providerType = provider.NormalizeProvider(providerKey)
	}
	return provider.AgentConfig{
		Provider:        providerType,
		Model:           strings.TrimSpace(pc.Model),
		ReasoningEffort: strings.TrimSpace(pc.ReasoningEffort),
		ExtraFlags:      append([]string(nil), pc.ExtraFlags...),
		// Permission 映射在 Phase 3 实现
	}.Normalize(), nil
}
