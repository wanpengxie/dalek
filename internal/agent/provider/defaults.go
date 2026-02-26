package provider

import "strings"

const (
	ProviderCodex  = "codex"
	ProviderClaude = "claude"
)

type Defaults struct {
	Model           string
	ReasoningEffort string
}

var providerDefaults = map[string]Defaults{
	ProviderCodex: {
		Model:           "gpt-5.3-codex",
		ReasoningEffort: "xhigh",
	},
	ProviderClaude: {
		Model: "opus",
	},
}

var providerOrder = []string{
	ProviderCodex,
	ProviderClaude,
}

func NormalizeProvider(raw string) string {
	return strings.TrimSpace(strings.ToLower(raw))
}

func SupportedProviders() []string {
	out := make([]string, len(providerOrder))
	copy(out, providerOrder)
	return out
}

func IsSupportedProvider(raw string) bool {
	_, ok := providerDefaults[NormalizeProvider(raw)]
	return ok
}

func DefaultModel(provider string) string {
	return strings.TrimSpace(providerDefaults[NormalizeProvider(provider)].Model)
}

func DefaultReasoningEffort(provider string) string {
	return strings.TrimSpace(providerDefaults[NormalizeProvider(provider)].ReasoningEffort)
}
