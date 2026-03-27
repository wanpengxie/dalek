package repo

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	agentprovider "dalek/internal/agent/provider"
)

func TestConfigWithDefaults_SetsAgentAndTimeoutDefaults(t *testing.T) {
	cfg := (Config{}).WithDefaults()
	if strings.TrimSpace(cfg.PMAgent.Provider) == "" {
		t.Fatalf("expected non-empty pm_agent.provider default")
	}
	if strings.TrimSpace(cfg.WorkerAgent.Provider) == "" {
		t.Fatalf("expected non-empty worker_agent.provider default")
	}
	if cfg.PMAgent.Provider != "claude" {
		t.Fatalf("expected pm_agent.provider=claude by default, got=%q", cfg.PMAgent.Provider)
	}
	if cfg.WorkerAgent.Provider != "codex" {
		t.Fatalf("expected worker_agent.provider=codex by default, got=%q", cfg.WorkerAgent.Provider)
	}
	if cfg.GatewayAgent.Provider != "claude" {
		t.Fatalf("expected gateway_agent.provider=claude by default, got=%q", cfg.GatewayAgent.Provider)
	}
	if !cfg.Notebook.Enabled {
		t.Fatalf("expected notebook.enabled=true by default")
	}
	if !cfg.Notebook.AutoShape {
		t.Fatalf("expected notebook.auto_shape=true by default")
	}
	if cfg.Notebook.ShapeIntervalSec != 60 {
		t.Fatalf("expected notebook.shape_interval_sec=60 by default, got=%d", cfg.Notebook.ShapeIntervalSec)
	}
}

func TestMergeConfig_OverridesPMAgent(t *testing.T) {
	base := Config{}.WithDefaults()
	got := MergeConfig(base, Config{
		PMAgent: RoleConfig{
			Provider: "codex",
		},
	})
	if strings.TrimSpace(got.PMAgent.Provider) != "codex" {
		t.Fatalf("unexpected pm_agent.provider: %q", got.PMAgent.Provider)
	}
}

func TestMergeConfig_OverridesWorkerAgent(t *testing.T) {
	base := Config{}.WithDefaults()
	got := MergeConfig(base, Config{
		WorkerAgent: RoleConfig{
			Provider: "claude",
		},
	})
	if strings.TrimSpace(got.WorkerAgent.Provider) != "claude" {
		t.Fatalf("unexpected worker_agent.provider: %q", got.WorkerAgent.Provider)
	}
}

func TestConfigWithDefaults_GatewayAgentTurnTimeout(t *testing.T) {
	cfg := (Config{}).WithDefaults()
	if cfg.GatewayAgent.TurnTimeoutMS != 0 {
		t.Fatalf("gateway agent turn timeout default mismatch: got=%d", cfg.GatewayAgent.TurnTimeoutMS)
	}
}

func TestMergeConfig_OverridesGatewayAgentFields(t *testing.T) {
	base := Config{}.WithDefaults()
	got := MergeConfig(base, Config{
		GatewayAgent: GatewayRoleConfig{
			Provider:      "codex",
			Output:        "jsonl",
			ResumeOutput:  "json",
			TurnTimeoutMS: 30000,
		},
	})

	if got.GatewayAgent.Provider != "codex" {
		t.Fatalf("unexpected gateway_agent.provider: %q", got.GatewayAgent.Provider)
	}
	if got.GatewayAgent.Output != "jsonl" {
		t.Fatalf("unexpected gateway_agent.output: %q", got.GatewayAgent.Output)
	}
	if got.GatewayAgent.ResumeOutput != "json" {
		t.Fatalf("unexpected gateway_agent.resume_output: %q", got.GatewayAgent.ResumeOutput)
	}
	if got.GatewayAgent.TurnTimeoutMS != 30000 {
		t.Fatalf("unexpected gateway_agent.turn_timeout_ms: %d", got.GatewayAgent.TurnTimeoutMS)
	}
}

func TestLoadConfig_DoesNotRewriteWhenSchemaAndAgentsComplete(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "config.json")
	raw, err := json.MarshalIndent(Config{
		SchemaVersion: CurrentProjectSchemaVersion,
		WorkerAgent: RoleConfig{
			Provider: "codex",
		},
		PMAgent: RoleConfig{
			Provider: "codex",
		},
		Notebook: NotebookConfig{
			Enabled:          true,
			AutoShape:        true,
			ShapeIntervalSec: 60,
		},
	}, "", "  ")
	if err != nil {
		t.Fatalf("marshal config failed: %v", err)
	}
	if err := os.WriteFile(path, append(raw, '\n'), 0o644); err != nil {
		t.Fatalf("write config failed: %v", err)
	}

	_, needsRewrite, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}
	if needsRewrite {
		t.Fatalf("expected needsRewrite=false when schema/agents are complete")
	}
}

func TestMergeConfig_OverridesNotebookFields(t *testing.T) {
	base := Config{}.WithDefaults()

	var override Config
	if err := json.Unmarshal([]byte(`{
		"schema_version": 3,
		"notebook": {
			"enabled": false,
			"auto_shape": false,
			"shape_interval_sec": 15
		}
	}`), &override); err != nil {
		t.Fatalf("unmarshal override failed: %v", err)
	}

	got := MergeConfig(base, override)
	if got.Notebook.Enabled {
		t.Fatalf("notebook.enabled should be overridden to false")
	}
	if got.Notebook.AutoShape {
		t.Fatalf("notebook.auto_shape should be overridden to false")
	}
	if got.Notebook.ShapeIntervalSec != 15 {
		t.Fatalf("notebook.shape_interval_sec should be overridden: got=%d", got.Notebook.ShapeIntervalSec)
	}
}

func TestLoadConfig_OldSchemaVersionReturnsError(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "config.json")
	raw := `{
  "schema_version": 1,
  "worker_agent": { "provider": "codex" },
  "pm_agent": { "provider": "claude" }
}`
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		t.Fatalf("write config failed: %v", err)
	}

	_, _, err := LoadConfig(path)
	if err == nil {
		t.Fatalf("expected error for schema_version=1, got nil")
	}
	if !strings.Contains(err.Error(), "已不再支持") {
		t.Fatalf("expected deprecation error, got=%q", err.Error())
	}
}

func TestResolveAgentConfig_AppliesNormalizationAndDefaults(t *testing.T) {
	providers := DefaultProviders()
	got, err := ResolveAgentConfig("codex", providers)
	if err != nil {
		t.Fatalf("ResolveAgentConfig failed: %v", err)
	}
	if got.Provider != agentprovider.ProviderCodex {
		t.Fatalf("unexpected provider: %q", got.Provider)
	}
	if got.Model != agentprovider.DefaultModel(agentprovider.ProviderCodex) {
		t.Fatalf("unexpected model default: %q", got.Model)
	}
	if got.ReasoningEffort != agentprovider.DefaultReasoningEffort(agentprovider.ProviderCodex) {
		t.Fatalf("unexpected reasoning_effort default: %q", got.ReasoningEffort)
	}
}

func TestResolveAgentConfig_ClaudeClearsReasoningEffort(t *testing.T) {
	providers := DefaultProviders()
	got, err := ResolveAgentConfig("claude", providers)
	if err != nil {
		t.Fatalf("ResolveAgentConfig failed: %v", err)
	}
	if got.Provider != agentprovider.ProviderClaude {
		t.Fatalf("unexpected provider: %q", got.Provider)
	}
	if got.ReasoningEffort != "" {
		t.Fatalf("claude reasoning_effort should be empty, got=%q", got.ReasoningEffort)
	}
}
