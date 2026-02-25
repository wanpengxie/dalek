package repo

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	if cfg.WorkerAgent.Model != "gpt-5.3-codex" {
		t.Fatalf("unexpected worker_agent.model default: %q", cfg.WorkerAgent.Model)
	}
	if cfg.WorkerAgent.ReasoningEffort != "xhigh" {
		t.Fatalf("unexpected worker_agent.reasoning_effort default: %q", cfg.WorkerAgent.ReasoningEffort)
	}
	if cfg.PMDispatchTimeoutMS != 0 {
		t.Fatalf("expected pm_dispatch_timeout_ms default=0, got=%d", cfg.PMDispatchTimeoutMS)
	}
	if cfg.WorkerAgent.Mode != "sdk" {
		t.Fatalf("unexpected worker_agent.mode default: %q", cfg.WorkerAgent.Mode)
	}
	if cfg.PMAgent.Mode != "sdk" {
		t.Fatalf("unexpected pm_agent.mode default: %q", cfg.PMAgent.Mode)
	}
	if cfg.GatewayAgent.Mode != "sdk" {
		t.Fatalf("unexpected gateway_agent.mode default: %q", cfg.GatewayAgent.Mode)
	}
	if cfg.GatewayAgent.Provider != "claude" {
		t.Fatalf("expected gateway_agent.provider=claude by default, got=%q", cfg.GatewayAgent.Provider)
	}
	if cfg.GatewayAgent.Model != "opus" {
		t.Fatalf("expected gateway_agent.model=opus by default, got=%q", cfg.GatewayAgent.Model)
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

func TestMergeConfig_OverridesPMDispatchTimeoutAndPMAgent(t *testing.T) {
	base := Config{}.WithDefaults()
	got := MergeConfig(base, Config{
		PMAgent: AgentExecConfig{
			Provider: "codex",
			Model:    "gpt-5-codex",
		},
		PMDispatchTimeoutMS: 12345,
	})
	if strings.TrimSpace(got.PMAgent.Provider) != "codex" {
		t.Fatalf("unexpected pm_agent.provider: %q", got.PMAgent.Provider)
	}
	if strings.TrimSpace(got.PMAgent.Model) != "gpt-5-codex" {
		t.Fatalf("unexpected pm_agent.model: %q", got.PMAgent.Model)
	}
	if got.PMDispatchTimeoutMS != 12345 {
		t.Fatalf("unexpected pm_dispatch_timeout_ms: got=%d", got.PMDispatchTimeoutMS)
	}
}

func TestMergeConfig_OverridesAgentCommand(t *testing.T) {
	base := Config{}.WithDefaults()
	got := MergeConfig(base, Config{
		WorkerAgent: AgentExecConfig{
			Provider: "codex",
			Mode:     "sdk",
			Command:  "/tmp/fake-codex",
		},
	})
	if strings.TrimSpace(got.WorkerAgent.Provider) != "codex" {
		t.Fatalf("unexpected worker_agent.provider: %q", got.WorkerAgent.Provider)
	}
	if strings.TrimSpace(got.WorkerAgent.Command) != "/tmp/fake-codex" {
		t.Fatalf("unexpected worker_agent.command: %q", got.WorkerAgent.Command)
	}
	if strings.TrimSpace(got.WorkerAgent.Mode) != "sdk" {
		t.Fatalf("unexpected worker_agent.mode: %q", got.WorkerAgent.Mode)
	}
}

func TestMergeConfig_SwitchProviderToClaudeClearsInheritedCodexModel(t *testing.T) {
	base := Config{
		WorkerAgent: AgentExecConfig{
			Provider: "codex",
			Model:    "gpt-5.3-codex",
		},
		PMAgent: AgentExecConfig{
			Provider: "codex",
			Model:    "gpt-5.3-codex",
		},
	}.WithDefaults()
	got := MergeConfig(base, Config{
		WorkerAgent: AgentExecConfig{Provider: "claude"},
		PMAgent:     AgentExecConfig{Provider: "claude"},
	})
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

func TestMergeConfig_SwitchProviderToCodexRestoresDefaults(t *testing.T) {
	base := Config{
		WorkerAgent: AgentExecConfig{
			Provider: "claude",
			Model:    "claude-3-7-sonnet",
		},
		PMAgent: AgentExecConfig{
			Provider: "claude",
			Model:    "claude-3-7-sonnet",
		},
	}.WithDefaults()
	got := MergeConfig(base, Config{
		WorkerAgent: AgentExecConfig{Provider: "codex"},
		PMAgent:     AgentExecConfig{Provider: "codex"},
	})
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

func TestConfigWithDefaults_GatewayAgentTurnTimeout(t *testing.T) {
	cfg := (Config{}).WithDefaults()
	if cfg.GatewayAgent.TurnTimeoutMS != 0 {
		t.Fatalf("gateway agent turn timeout default mismatch: got=%d", cfg.GatewayAgent.TurnTimeoutMS)
	}
}

func TestMergeConfig_OverridesGatewayAgentFields(t *testing.T) {
	base := Config{}.WithDefaults()
	got := MergeConfig(base, Config{
		GatewayAgent: GatewayAgentConfig{
			Provider:      "codex",
			Mode:          "sdk",
			Model:         "gpt-5-codex",
			Command:       "/tmp/custom-agent",
			Output:        "jsonl",
			ResumeOutput:  "json",
			TurnTimeoutMS: 30000,
		},
	})

	if got.GatewayAgent.Provider != "codex" {
		t.Fatalf("unexpected gateway_agent.provider: %q", got.GatewayAgent.Provider)
	}
	if got.GatewayAgent.Model != "gpt-5-codex" {
		t.Fatalf("unexpected gateway_agent.model: %q", got.GatewayAgent.Model)
	}
	if got.GatewayAgent.Mode != "sdk" {
		t.Fatalf("unexpected gateway_agent.mode: %q", got.GatewayAgent.Mode)
	}
	if got.GatewayAgent.Command != "/tmp/custom-agent" {
		t.Fatalf("unexpected gateway_agent.command: %q", got.GatewayAgent.Command)
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
		WorkerAgent: AgentExecConfig{
			Provider: "codex",
			Model:    "gpt-5.3-codex",
		},
		PMAgent: AgentExecConfig{
			Provider: "codex",
			Model:    "gpt-5.3-codex",
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

	cfg, needsRewrite, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}
	if needsRewrite {
		t.Fatalf("expected needsRewrite=false when schema/agents are complete")
	}
	if cfg.PMDispatchTimeoutMS != 0 {
		t.Fatalf("expected pm_dispatch_timeout_ms default=0 after defaults, got=%d", cfg.PMDispatchTimeoutMS)
	}
}

func TestMergeConfig_OverridesNotebookFields(t *testing.T) {
	base := Config{}.WithDefaults()

	var override Config
	if err := json.Unmarshal([]byte(`{
		"schema_version": 2,
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

func TestLoadConfig_V1SchemaMigratesNotebookAndLogsDeprecation(t *testing.T) {
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

	var deprecationMsg string
	origWarnf := projectConfigDeprecationWarnf
	projectConfigDeprecationWarnf = func(format string, args ...any) {
		deprecationMsg = fmt.Sprintf(format, args...)
	}
	defer func() { projectConfigDeprecationWarnf = origWarnf }()

	cfg, needsRewrite, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}
	if !needsRewrite {
		t.Fatalf("schema_version=1 should need rewrite")
	}
	if cfg.SchemaVersion != CurrentProjectSchemaVersion {
		t.Fatalf("unexpected schema version: got=%d want=%d", cfg.SchemaVersion, CurrentProjectSchemaVersion)
	}
	if !cfg.Notebook.Enabled || !cfg.Notebook.AutoShape || cfg.Notebook.ShapeIntervalSec != 60 {
		t.Fatalf("notebook defaults should be applied after migration: %+v", cfg.Notebook)
	}
	if !strings.Contains(deprecationMsg, "已废弃") {
		t.Fatalf("expected deprecation warning, got=%q", deprecationMsg)
	}
}
