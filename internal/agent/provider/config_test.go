package provider

import "testing"

func TestNewFromConfig_CodexWithCommandOverride(t *testing.T) {
	p, err := NewFromConfig(AgentConfig{
		Provider:        "codex",
		Command:         "/tmp/fake-codex",
		Model:           "gpt-5.3-codex",
		ReasoningEffort: "xhigh",
	})
	if err != nil {
		t.Fatalf("NewFromConfig(codex) failed: %v", err)
	}
	bin, _ := p.BuildCommand("hello")
	if bin != "/tmp/fake-codex" {
		t.Fatalf("unexpected codex bin: %q", bin)
	}
}

func TestNewFromConfig_ClaudeWithCommandOverride(t *testing.T) {
	p, err := NewFromConfig(AgentConfig{
		Provider: "claude",
		Command:  "/tmp/fake-claude",
		Model:    "claude-3-7-sonnet",
	})
	if err != nil {
		t.Fatalf("NewFromConfig(claude) failed: %v", err)
	}
	bin, _ := p.BuildCommand("hello")
	if bin != "/tmp/fake-claude" {
		t.Fatalf("unexpected claude bin: %q", bin)
	}
}

func TestNewFromConfig_RejectsShellProvider(t *testing.T) {
	if _, err := NewFromConfig(AgentConfig{
		Provider: "shell",
		Command:  "bash ./old.sh",
	}); err == nil {
		t.Fatalf("expected shell provider rejected")
	}
}

func TestProviderDefaults(t *testing.T) {
	if got := DefaultModel(ProviderCodex); got != "gpt-5.3-codex" {
		t.Fatalf("unexpected codex default model: %q", got)
	}
	if got := DefaultReasoningEffort(ProviderCodex); got != "xhigh" {
		t.Fatalf("unexpected codex default reasoning_effort: %q", got)
	}
	if got := DefaultModel(ProviderClaude); got != "opus" {
		t.Fatalf("unexpected claude default model: %q", got)
	}
	if got := DefaultReasoningEffort(ProviderClaude); got != "" {
		t.Fatalf("unexpected claude default reasoning_effort: %q", got)
	}
}

func TestSupportedProviders(t *testing.T) {
	got := SupportedProviders()
	if len(got) != 2 || got[0] != ProviderCodex || got[1] != ProviderClaude {
		t.Fatalf("unexpected supported providers: %#v", got)
	}
	if !IsSupportedProvider(" codex ") || !IsSupportedProvider("CLAUDE") {
		t.Fatalf("expected codex/claude supported")
	}
	if IsSupportedProvider("shell") {
		t.Fatalf("shell should not be supported")
	}
}
