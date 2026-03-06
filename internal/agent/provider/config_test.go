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

func TestNewFromConfig_GeminiWithCommandOverride(t *testing.T) {
	p, err := NewFromConfig(AgentConfig{
		Provider: "gemini",
		Command:  "/tmp/fake-gemini",
		Model:    "gemini-2.5-pro",
	})
	if err != nil {
		t.Fatalf("NewFromConfig(gemini) failed: %v", err)
	}
	bin, _ := p.BuildCommand("hello")
	if bin != "/tmp/fake-gemini" {
		t.Fatalf("unexpected gemini bin: %q", bin)
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
	if got := DefaultModel(ProviderGemini); got != "gemini-2.5-pro" {
		t.Fatalf("unexpected gemini default model: %q", got)
	}
	if got := DefaultReasoningEffort(ProviderGemini); got != "" {
		t.Fatalf("unexpected gemini default reasoning_effort: %q", got)
	}
}

func TestSupportedProviders(t *testing.T) {
	got := SupportedProviders()
	if len(got) != 3 || got[0] != ProviderCodex || got[1] != ProviderClaude || got[2] != ProviderGemini {
		t.Fatalf("unexpected supported providers: %#v", got)
	}
	if !IsSupportedProvider(" codex ") || !IsSupportedProvider("CLAUDE") || !IsSupportedProvider("Gemini") {
		t.Fatalf("expected codex/claude/gemini supported")
	}
	if IsSupportedProvider("shell") {
		t.Fatalf("shell should not be supported")
	}
}
