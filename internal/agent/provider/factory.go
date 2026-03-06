package provider

import (
	"fmt"
	"strings"
)

type constructor func(cfg AgentConfig) Provider

var providerConstructors = map[string]constructor{
	ProviderCodex: func(cfg AgentConfig) Provider {
		return CodexProvider{
			Command:         cfg.Command,
			Model:           cfg.Model,
			ReasoningEffort: cfg.ReasoningEffort,
			ExtraFlags:      append([]string(nil), cfg.ExtraFlags...),
		}
	},
	ProviderClaude: func(cfg AgentConfig) Provider {
		return ClaudeProvider{
			Command:    cfg.Command,
			Model:      cfg.Model,
			ExtraFlags: append([]string(nil), cfg.ExtraFlags...),
		}
	},
	ProviderGemini: func(cfg AgentConfig) Provider {
		return GeminiProvider{
			Command:    cfg.Command,
			Model:      cfg.Model,
			ExtraFlags: append([]string(nil), cfg.ExtraFlags...),
		}
	},
}

func NewFromConfig(cfg AgentConfig) (Provider, error) {
	cfg = cfg.Normalize()
	supported := strings.Join(SupportedProviders(), "/")
	if cfg.Provider == "" {
		return nil, fmt.Errorf("agent provider 为空，仅支持 %s", supported)
	}
	build, ok := providerConstructors[cfg.Provider]
	if !ok {
		return nil, fmt.Errorf("unknown agent provider: %s（仅支持 %s）", cfg.Provider, supported)
	}
	return build(cfg), nil
}
