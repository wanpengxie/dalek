package repo

import (
	"dalek/internal/agent/provider"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
)

type Config struct {
	SchemaVersion       int                `json:"schema_version"`
	BranchPrefix        string             `json:"branch_prefix"`
	RefreshIntervalMS   int                `json:"refresh_interval_ms"`
	WorkerAgent         AgentExecConfig    `json:"worker_agent"`
	PMAgent             AgentExecConfig    `json:"pm_agent"`
	ManagerCommand      string             `json:"manager_command"`
	PMDispatchTimeoutMS int                `json:"pm_dispatch_timeout_ms"`
	PMPlannerTimeoutMS  int                `json:"pm_planner_timeout_ms"`
	GatewayAgent        GatewayAgentConfig `json:"gateway_agent"`
	Notebook            NotebookConfig     `json:"notebook"`

	notebookSet              bool
	notebookEnabledSet       bool
	notebookAutoShapeSet     bool
	notebookShapeIntervalSet bool
}

type AgentExecConfig struct {
	Provider          string   `json:"provider"`
	Mode              string   `json:"mode,omitempty"`
	Model             string   `json:"model,omitempty"`
	ReasoningEffort   string   `json:"reasoning_effort,omitempty"`
	ExtraFlags        []string `json:"extra_flags,omitempty"`
	DangerFullAccess  bool     `json:"danger_full_access,omitempty"`
	BypassPermissions bool     `json:"bypass_permissions,omitempty"`
	// Command 可选，用于覆盖 provider 可执行文件路径（例如自定义 codex/claude 二进制）。
	Command string `json:"command,omitempty"`

	dangerFullAccessSet  bool
	bypassPermissionsSet bool
}

type GatewayAgentConfig struct {
	Provider      string `json:"provider"`
	Mode          string `json:"mode,omitempty"`
	Model         string `json:"model"`
	Command       string `json:"command"`
	Output        string `json:"output"`
	ResumeOutput  string `json:"resume_output"`
	TurnTimeoutMS int    `json:"turn_timeout_ms"`
}

type NotebookConfig struct {
	Enabled          bool `json:"enabled"`
	AutoShape        bool `json:"auto_shape"`
	ShapeIntervalSec int  `json:"shape_interval_sec"`
}

const (
	defaultWorkerProvider      = provider.ProviderCodex
	defaultPMProvider          = provider.ProviderClaude
	execModeCLI                = "cli"
	execModeSDK                = "sdk"
	defaultNotebookEnabled     = true
	defaultNotebookAutoShape   = true
	defaultNotebookIntervalSec = 60
)

// CurrentProjectSchemaVersion 是项目结构迁移版本号（非 binary semver）。
// 发生 breaking 的项目结构变更时手动递增。
const CurrentProjectSchemaVersion = 2

var projectConfigDeprecationWarnf = func(format string, args ...any) {
	log.Printf(format, args...)
}

func (c *AgentExecConfig) UnmarshalJSON(data []byte) error {
	type alias AgentExecConfig
	var decoded alias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*c = AgentExecConfig(decoded)

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil
	}
	if _, exists := raw["danger_full_access"]; exists {
		c.dangerFullAccessSet = true
	}
	if _, exists := raw["bypass_permissions"]; exists {
		c.bypassPermissionsSet = true
	}
	return nil
}

func (c *Config) UnmarshalJSON(data []byte) error {
	type alias Config
	var decoded alias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*c = Config(decoded)

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil
	}
	nbRaw, ok := raw["notebook"]
	if !ok {
		return nil
	}
	c.notebookSet = true
	var nb map[string]json.RawMessage
	if err := json.Unmarshal(nbRaw, &nb); err != nil {
		return nil
	}
	if _, exists := nb["enabled"]; exists {
		c.notebookEnabledSet = true
	}
	if _, exists := nb["auto_shape"]; exists {
		c.notebookAutoShapeSet = true
	}
	if _, exists := nb["shape_interval_sec"]; exists {
		c.notebookShapeIntervalSet = true
	}
	return nil
}

func (c Config) WithDefaults() Config {
	out := c
	if out.SchemaVersion <= 0 || out.SchemaVersion < CurrentProjectSchemaVersion {
		out.SchemaVersion = CurrentProjectSchemaVersion
	}
	if out.RefreshIntervalMS <= 0 {
		out.RefreshIntervalMS = 1000
	}
	out.WorkerAgent = normalizeAgentExecConfig(out.WorkerAgent)
	out.PMAgent = normalizeAgentExecConfig(out.PMAgent)
	out.GatewayAgent = normalizeGatewayAgentConfig(out.GatewayAgent)
	if out.WorkerAgent.Provider == "" {
		out.WorkerAgent = AgentExecConfig{
			Provider:        defaultWorkerProvider,
			Mode:            execModeSDK,
			Model:           provider.DefaultModel(defaultWorkerProvider),
			ReasoningEffort: provider.DefaultReasoningEffort(defaultWorkerProvider),
		}
	}
	if out.PMAgent.Provider == "" {
		out.PMAgent = AgentExecConfig{
			Provider: defaultPMProvider,
			Mode:     execModeSDK,
			Model:    provider.DefaultModel(defaultPMProvider),
		}
	}
	if out.GatewayAgent.Provider == "" {
		out.GatewayAgent.Provider = defaultPMProvider
	}
	if out.GatewayAgent.Model == "" {
		out.GatewayAgent.Model = provider.DefaultModel(defaultPMProvider)
	}
	if out.ManagerCommand == "" {
		out.ManagerCommand = ""
	}
	if out.PMDispatchTimeoutMS < 0 {
		out.PMDispatchTimeoutMS = 0
	}
	if out.PMPlannerTimeoutMS < 0 {
		out.PMPlannerTimeoutMS = 0
	}
	if out.GatewayAgent.TurnTimeoutMS < 0 {
		out.GatewayAgent.TurnTimeoutMS = 0
	}
	if !out.notebookEnabledSet {
		out.Notebook.Enabled = defaultNotebookEnabled
	}
	if !out.notebookAutoShapeSet {
		out.Notebook.AutoShape = defaultNotebookAutoShape
	}
	if !out.notebookShapeIntervalSet || out.Notebook.ShapeIntervalSec <= 0 {
		out.Notebook.ShapeIntervalSec = defaultNotebookIntervalSec
	}
	return out
}

func normalizeAgentExecConfig(in AgentExecConfig) AgentExecConfig {
	out := in
	out.Provider = provider.NormalizeProvider(out.Provider)
	out.Mode = normalizeExecMode(out.Mode)
	out.Model = strings.TrimSpace(out.Model)
	out.ReasoningEffort = strings.TrimSpace(out.ReasoningEffort)
	out.Command = strings.TrimSpace(out.Command)
	switch out.Provider {
	case provider.ProviderCodex:
		if out.Model == "" {
			out.Model = provider.DefaultModel(provider.ProviderCodex)
		}
		if out.ReasoningEffort == "" {
			out.ReasoningEffort = provider.DefaultReasoningEffort(provider.ProviderCodex)
		}
		out.BypassPermissions = false
	case provider.ProviderClaude, provider.ProviderGemini:
		// claude/gemini 不使用 reasoning_effort，避免跨 provider 残留。
		out.ReasoningEffort = ""
		out.DangerFullAccess = false
		if out.Provider != provider.ProviderClaude {
			out.BypassPermissions = false
		}
	default:
		out.DangerFullAccess = false
		out.BypassPermissions = false
	}
	if len(out.ExtraFlags) > 0 {
		flags := make([]string, 0, len(out.ExtraFlags))
		for _, flag := range out.ExtraFlags {
			flag = strings.TrimSpace(flag)
			if flag == "" {
				continue
			}
			flags = append(flags, flag)
		}
		out.ExtraFlags = flags
	}
	return out
}

func normalizeGatewayAgentConfig(in GatewayAgentConfig) GatewayAgentConfig {
	out := in
	out.Provider = provider.NormalizeProvider(out.Provider)
	out.Mode = normalizeExecMode(out.Mode)
	out.Model = strings.TrimSpace(out.Model)
	out.Command = strings.TrimSpace(out.Command)
	out.Output = strings.TrimSpace(out.Output)
	out.ResumeOutput = strings.TrimSpace(out.ResumeOutput)
	return out
}

func normalizeExecMode(raw string) string {
	switch strings.TrimSpace(strings.ToLower(raw)) {
	case "", execModeSDK:
		return execModeSDK
	case execModeCLI:
		return execModeCLI
	default:
		return execModeSDK
	}
}

func mergeAgentExecConfig(base AgentExecConfig, override AgentExecConfig) AgentExecConfig {
	out := normalizeAgentExecConfig(base)
	rawOverrideMode := strings.TrimSpace(override.Mode)
	override = normalizeAgentExecConfig(override)
	providerChanged := false
	if override.Provider != "" {
		providerChanged = out.Provider != "" && out.Provider != override.Provider
		out.Provider = override.Provider
	}
	if override.Model != "" {
		out.Model = override.Model
	} else if providerChanged {
		// provider 变化但上层未显式给 model 时，清空下层 model，避免跨 provider 污染。
		out.Model = ""
	}
	if override.ReasoningEffort != "" {
		out.ReasoningEffort = override.ReasoningEffort
	}
	if override.Command != "" {
		out.Command = override.Command
	}
	if override.dangerFullAccessSet {
		out.DangerFullAccess = override.DangerFullAccess
	}
	if override.bypassPermissionsSet {
		out.BypassPermissions = override.BypassPermissions
	}
	if rawOverrideMode != "" {
		out.Mode = normalizeExecMode(rawOverrideMode)
	}
	if len(override.ExtraFlags) > 0 {
		out.ExtraFlags = append([]string(nil), override.ExtraFlags...)
	}
	return normalizeAgentExecConfig(out)
}

func LoadConfig(path string) (Config, bool, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Config{}, false, err
	}
	var cfg Config
	if err := json.Unmarshal(b, &cfg); err != nil {
		return Config{}, false, err
	}
	needsRewrite := cfg.SchemaVersion <= 0 ||
		cfg.SchemaVersion < CurrentProjectSchemaVersion ||
		strings.TrimSpace(cfg.WorkerAgent.Provider) == "" ||
		strings.TrimSpace(cfg.PMAgent.Provider) == "" ||
		!cfg.notebookSet ||
		!cfg.notebookEnabledSet ||
		!cfg.notebookAutoShapeSet ||
		!cfg.notebookShapeIntervalSet
	if cfg.SchemaVersion > 0 && cfg.SchemaVersion < CurrentProjectSchemaVersion {
		projectConfigDeprecationWarnf(
			"[deprecation] project config schema v%d 已废弃，已自动迁移到 v%d；请补充 notebook.enabled/auto_shape/shape_interval_sec",
			cfg.SchemaVersion,
			CurrentProjectSchemaVersion,
		)
	}
	return cfg.WithDefaults(), needsRewrite, nil
}

func WriteConfigAtomic(path string, cfg Config) error {
	cfg = cfg.WithDefaults()
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')

	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".config.json.*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()

	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("写入配置失败: %w", err)
	}
	return nil
}

func MergeConfig(oldCfg, override Config) Config {
	out := oldCfg
	if strings.TrimSpace(override.BranchPrefix) != "" {
		out.BranchPrefix = strings.TrimSpace(override.BranchPrefix)
	}
	if override.RefreshIntervalMS > 0 {
		out.RefreshIntervalMS = override.RefreshIntervalMS
	}
	out.WorkerAgent = mergeAgentExecConfig(out.WorkerAgent, override.WorkerAgent)
	out.PMAgent = mergeAgentExecConfig(out.PMAgent, override.PMAgent)
	if strings.TrimSpace(override.ManagerCommand) != "" {
		out.ManagerCommand = strings.TrimSpace(override.ManagerCommand)
	}
	if override.PMDispatchTimeoutMS > 0 {
		out.PMDispatchTimeoutMS = override.PMDispatchTimeoutMS
	}
	if override.PMPlannerTimeoutMS > 0 {
		out.PMPlannerTimeoutMS = override.PMPlannerTimeoutMS
	}
	if strings.TrimSpace(override.GatewayAgent.Provider) != "" {
		out.GatewayAgent.Provider = strings.TrimSpace(override.GatewayAgent.Provider)
	}
	if strings.TrimSpace(override.GatewayAgent.Mode) != "" {
		out.GatewayAgent.Mode = strings.TrimSpace(override.GatewayAgent.Mode)
	}
	if strings.TrimSpace(override.GatewayAgent.Model) != "" {
		out.GatewayAgent.Model = strings.TrimSpace(override.GatewayAgent.Model)
	}
	if strings.TrimSpace(override.GatewayAgent.Command) != "" {
		out.GatewayAgent.Command = strings.TrimSpace(override.GatewayAgent.Command)
	}
	if strings.TrimSpace(override.GatewayAgent.Output) != "" {
		out.GatewayAgent.Output = strings.TrimSpace(override.GatewayAgent.Output)
	}
	if strings.TrimSpace(override.GatewayAgent.ResumeOutput) != "" {
		out.GatewayAgent.ResumeOutput = strings.TrimSpace(override.GatewayAgent.ResumeOutput)
	}
	if override.GatewayAgent.TurnTimeoutMS > 0 {
		out.GatewayAgent.TurnTimeoutMS = override.GatewayAgent.TurnTimeoutMS
	}
	if override.notebookSet {
		if override.notebookEnabledSet {
			out.Notebook.Enabled = override.Notebook.Enabled
			out.notebookEnabledSet = true
		}
		if override.notebookAutoShapeSet {
			out.Notebook.AutoShape = override.Notebook.AutoShape
			out.notebookAutoShapeSet = true
		}
		if override.notebookShapeIntervalSet {
			out.Notebook.ShapeIntervalSec = override.Notebook.ShapeIntervalSec
			out.notebookShapeIntervalSet = true
		}
		out.notebookSet = true
	}
	out.GatewayAgent = normalizeGatewayAgentConfig(out.GatewayAgent)
	return out.WithDefaults()
}
