package app

import (
	"testing"

	"dalek/internal/repo"
)

func TestApplyAgentProviderModel_SwitchToClaudeClearsInheritedCodexModel(t *testing.T) {
	cfg := repo.Config{
		WorkerAgent: repo.AgentExecConfig{
			Provider: "codex",
			Model:    "gpt-5.3-codex",
		},
		PMAgent: repo.AgentExecConfig{
			Provider: "codex",
			Model:    "gpt-5.3-codex",
		},
	}.WithDefaults()
	got := applyAgentProviderModel(cfg, "claude", "").WithDefaults()
	if got.WorkerAgent.Provider != "claude" {
		t.Fatalf("unexpected worker_agent.provider: %q", got.WorkerAgent.Provider)
	}
	if got.WorkerAgent.Model != "" {
		t.Fatalf("worker_agent.model should be empty after switch to claude, got=%q", got.WorkerAgent.Model)
	}
	if got.WorkerAgent.ReasoningEffort != "" {
		t.Fatalf("worker_agent.reasoning_effort should be empty for claude, got=%q", got.WorkerAgent.ReasoningEffort)
	}
	if got.PMAgent.Provider != "claude" {
		t.Fatalf("unexpected pm_agent.provider: %q", got.PMAgent.Provider)
	}
	if got.PMAgent.Model != "" {
		t.Fatalf("pm_agent.model should be empty after switch to claude, got=%q", got.PMAgent.Model)
	}
	if got.PMAgent.ReasoningEffort != "" {
		t.Fatalf("pm_agent.reasoning_effort should be empty for claude, got=%q", got.PMAgent.ReasoningEffort)
	}
}

func TestApplyAgentProviderModel_SwitchToCodexRestoresDefaults(t *testing.T) {
	cfg := repo.Config{
		WorkerAgent: repo.AgentExecConfig{
			Provider: "claude",
			Model:    "claude-3-7-sonnet",
		},
		PMAgent: repo.AgentExecConfig{
			Provider: "claude",
			Model:    "claude-3-7-sonnet",
		},
	}.WithDefaults()
	got := applyAgentProviderModel(cfg, "codex", "").WithDefaults()
	if got.WorkerAgent.Provider != "codex" {
		t.Fatalf("unexpected worker_agent.provider: %q", got.WorkerAgent.Provider)
	}
	if got.WorkerAgent.Model != "gpt-5.3-codex" {
		t.Fatalf("unexpected worker_agent.model default: %q", got.WorkerAgent.Model)
	}
	if got.WorkerAgent.ReasoningEffort != "xhigh" {
		t.Fatalf("unexpected worker_agent.reasoning_effort default: %q", got.WorkerAgent.ReasoningEffort)
	}
	if got.PMAgent.Provider != "codex" {
		t.Fatalf("unexpected pm_agent.provider: %q", got.PMAgent.Provider)
	}
	if got.PMAgent.Model != "gpt-5.3-codex" {
		t.Fatalf("unexpected pm_agent.model default: %q", got.PMAgent.Model)
	}
	if got.PMAgent.ReasoningEffort != "xhigh" {
		t.Fatalf("unexpected pm_agent.reasoning_effort default: %q", got.PMAgent.ReasoningEffort)
	}
}

func TestComposeProjectConfigLayers_DefaultHomeRepoPrecedence(t *testing.T) {
	repoCfg := repo.Config{
		WorkerAgent: repo.AgentExecConfig{
			Provider: "codex",
			Model:    "repo-codex-model",
		},
	}
	got := composeProjectConfigLayers(repo.Config{}, "claude", "home-claude-model", repoCfg)
	if got.WorkerAgent.Provider != "codex" {
		t.Fatalf("worker_agent.provider should use repo override, got=%q", got.WorkerAgent.Provider)
	}
	if got.WorkerAgent.Model != "repo-codex-model" {
		t.Fatalf("worker_agent.model should use repo override, got=%q", got.WorkerAgent.Model)
	}
	if got.PMAgent.Provider != "claude" {
		t.Fatalf("pm_agent.provider should use home override, got=%q", got.PMAgent.Provider)
	}
	if got.PMAgent.Model != "home-claude-model" {
		t.Fatalf("pm_agent.model should use home override, got=%q", got.PMAgent.Model)
	}
}
