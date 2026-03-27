package subagent

import (
	agentprovider "dalek/internal/agent/provider"
	"dalek/internal/repo"
	"fmt"
	"strings"
)

func (s *Service) resolveAgentSettings(providerRaw, modelRaw string) (agentprovider.AgentConfig, error) {
	if s == nil || s.p == nil {
		return agentprovider.AgentConfig{}, fmt.Errorf("project 为空")
	}
	baseRole := s.p.Config.WithDefaults().WorkerAgent
	providers := s.p.Providers
	if len(providers) == 0 {
		providers = repo.DefaultProviders()
	}

	// 确定 provider key：用户指定 > role 配置默认
	providerKey := strings.TrimSpace(providerRaw)
	if providerKey == "" {
		providerKey = strings.TrimSpace(baseRole.Provider)
	}
	if providerKey == "" {
		providerKey = agentprovider.ProviderCodex
	}

	// 解析 provider 配置
	resolved, err := repo.ResolveAgentConfig(providerKey, providers)
	if err != nil {
		return agentprovider.AgentConfig{}, err
	}

	// 用户显式指定 model 时覆盖
	if model := strings.TrimSpace(modelRaw); model != "" {
		resolved.Model = model
	}

	// Subagent 强制 permission=auto：不继承父级的 bypass 权限
	resolved.Permission = agentprovider.PermissionAuto
	resolved.DangerFullAccess = false
	resolved.BypassPermissions = false

	if _, err := agentprovider.NewFromConfig(resolved); err != nil {
		return agentprovider.AgentConfig{}, err
	}
	return resolved, nil
}
