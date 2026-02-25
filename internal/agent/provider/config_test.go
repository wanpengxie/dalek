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
		Command:  "bash ./legacy.sh",
	}); err == nil {
		t.Fatalf("expected shell provider rejected")
	}
}
