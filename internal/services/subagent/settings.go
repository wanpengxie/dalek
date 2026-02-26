package subagent

import (
	agentprovider "dalek/internal/agent/provider"
	"fmt"
	"strings"
)

func (s *Service) resolveAgentSettings(providerRaw, modelRaw string) (agentprovider.AgentConfig, error) {
	if s == nil || s.p == nil {
		return agentprovider.AgentConfig{}, fmt.Errorf("project 为空")
	}
	cfg := s.p.Config.WithDefaults().WorkerAgent
	providerName := strings.TrimSpace(strings.ToLower(providerRaw))
	baseProvider := strings.TrimSpace(strings.ToLower(cfg.Provider))
	if providerName == "" {
		providerName = baseProvider
	}
	if providerName == "" {
		providerName = "codex"
	}
	model := strings.TrimSpace(modelRaw)
	if model == "" {
		if strings.TrimSpace(providerRaw) == "" || providerName == baseProvider {
			model = strings.TrimSpace(cfg.Model)
		}
	}
	if providerName == "codex" && model == "" {
		model = "gpt-5.3-codex"
	}
	reasoning := strings.TrimSpace(strings.ToLower(cfg.ReasoningEffort))
	if providerName == "claude" {
		reasoning = ""
	}
	resolved := agentprovider.AgentConfig{
		Provider:        providerName,
		Model:           model,
		ReasoningEffort: reasoning,
		ExtraFlags:      append([]string(nil), cfg.ExtraFlags...),
		Command:         strings.TrimSpace(cfg.Command),
	}
	if _, err := agentprovider.NewFromConfig(resolved); err != nil {
		return agentprovider.AgentConfig{}, err
	}
	return resolved.Normalize(), nil
}
