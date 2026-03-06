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
	baseExecCfg := s.p.Config.WithDefaults().WorkerAgent
	providerName := agentprovider.NormalizeProvider(providerRaw)
	baseProvider := agentprovider.NormalizeProvider(baseExecCfg.Provider)
	if providerName == "" {
		providerName = baseProvider
	}
	if providerName == "" {
		providerName = agentprovider.ProviderCodex
	}
	model := strings.TrimSpace(modelRaw)
	if model == "" {
		if strings.TrimSpace(providerRaw) == "" || providerName == baseProvider {
			model = strings.TrimSpace(baseExecCfg.Model)
		}
	}
	if providerName == agentprovider.ProviderCodex && model == "" {
		model = agentprovider.DefaultModel(agentprovider.ProviderCodex)
	}
	reasoning := strings.TrimSpace(strings.ToLower(baseExecCfg.ReasoningEffort))
	if providerName == agentprovider.ProviderClaude || providerName == agentprovider.ProviderGemini {
		reasoning = ""
	}
	resolvedExecCfg := baseExecCfg
	resolvedExecCfg.Provider = providerName
	resolvedExecCfg.Model = model
	resolvedExecCfg.ReasoningEffort = reasoning
	resolved := repo.AgentConfigFromExecConfig(resolvedExecCfg)
	if _, err := agentprovider.NewFromConfig(resolved); err != nil {
		return agentprovider.AgentConfig{}, err
	}
	return resolved, nil
}
