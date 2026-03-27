package app

import (
	"testing"

	"dalek/internal/repo"
)

func TestApplyAgentProviderModel_SwitchToClaudeClearsProvider(t *testing.T) {
	cfg := repo.Config{
		WorkerAgent: repo.RoleConfig{
			Provider: "codex",
		},
		PMAgent: repo.RoleConfig{
			Provider: "codex",
		},
	}.WithDefaults()
	got := applyAgentProviderModel(cfg, "claude", "").WithDefaults()
	if got.WorkerAgent.Provider != "claude" {
		t.Fatalf("unexpected worker_agent.provider: %q", got.WorkerAgent.Provider)
	}
	if got.PMAgent.Provider != "claude" {
		t.Fatalf("unexpected pm_agent.provider: %q", got.PMAgent.Provider)
	}
}

func TestApplyAgentProviderModel_SwitchToCodexRestoresProvider(t *testing.T) {
	cfg := repo.Config{
		WorkerAgent: repo.RoleConfig{
			Provider: "claude",
		},
		PMAgent: repo.RoleConfig{
			Provider: "claude",
		},
	}.WithDefaults()
	got := applyAgentProviderModel(cfg, "codex", "").WithDefaults()
	if got.WorkerAgent.Provider != "codex" {
		t.Fatalf("unexpected worker_agent.provider: %q", got.WorkerAgent.Provider)
	}
	if got.PMAgent.Provider != "codex" {
		t.Fatalf("unexpected pm_agent.provider: %q", got.PMAgent.Provider)
	}
}

func TestComposeProjectConfigLayers_DefaultHomeRepoPrecedence(t *testing.T) {
	repoCfg := repo.Config{
		WorkerAgent: repo.RoleConfig{
			Provider: "codex",
		},
	}
	got := composeProjectConfigLayers(repo.Config{}, "claude", "home-claude-model", repoCfg)
	if got.WorkerAgent.Provider != "codex" {
		t.Fatalf("worker_agent.provider should use repo override, got=%q", got.WorkerAgent.Provider)
	}
	if got.PMAgent.Provider != "claude" {
		t.Fatalf("pm_agent.provider should use home override, got=%q", got.PMAgent.Provider)
	}
}
