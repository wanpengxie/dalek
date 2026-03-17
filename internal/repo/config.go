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
	SchemaVersion       int                        `json:"schema_version"`
	BranchPrefix        string                     `json:"branch_prefix"`
	RefreshIntervalMS   int                        `json:"refresh_interval_ms"`
	WorkerAgent         AgentExecConfig            `json:"worker_agent"`
	PMAgent             AgentExecConfig            `json:"pm_agent"`
	ManagerCommand      string                     `json:"manager_command"`
	PMDispatchTimeoutMS int                        `json:"pm_dispatch_timeout_ms"`
	GatewayAgent        GatewayAgentConfig         `json:"gateway_agent"`
	MultiNode           MultiNodeConfig            `json:"multi_node"`
	RunTargets          map[string]RunTargetConfig `json:"run_targets,omitempty"`
	Notebook            NotebookConfig             `json:"notebook"`

	notebookSet              bool
	notebookEnabledSet       bool
	notebookAutoShapeSet     bool
	notebookShapeIntervalSet bool
}

type AgentExecConfig struct {
	Provider        string   `json:"provider"`
	Mode            string   `json:"mode,omitempty"`
	Model           string   `json:"model,omitempty"`
	ReasoningEffort string   `json:"reasoning_effort,omitempty"`
	ExtraFlags      []string `json:"extra_flags,omitempty"`
	// Command 可选，用于覆盖 provider 可执行文件路径（例如自定义 codex/claude 二进制）。
	Command string `json:"command,omitempty"`
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

type MultiNodeConfig struct {
	AutoRoute                bool   `json:"auto_route"`
	DevBaseURL               string `json:"dev_base_url,omitempty"`
	DevProjectName           string `json:"dev_project_name,omitempty"`
	RunBaseURL               string `json:"run_base_url,omitempty"`
	RunProjectName           string `json:"run_project_name,omitempty"`
	AutoLinkLatestRunFailure bool   `json:"auto_link_latest_run_failure"`
}

type RunTargetConfig struct {
	Description        string   `json:"description,omitempty"`
	Command            []string `json:"command,omitempty"`
	TimeoutMS          int64    `json:"timeout_ms,omitempty"`
	PreflightCommand   []string `json:"preflight_command,omitempty"`
	PreflightTimeoutMS int64    `json:"preflight_timeout_ms,omitempty"`
	BootstrapCommand   []string `json:"bootstrap_command,omitempty"`
	BootstrapTimeoutMS int64    `json:"bootstrap_timeout_ms,omitempty"`
	RepairCommand      []string `json:"repair_command,omitempty"`
	RepairTimeoutMS    int64    `json:"repair_timeout_ms,omitempty"`
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
	out.MultiNode = normalizeMultiNodeConfig(out.MultiNode)
	out.RunTargets = normalizeRunTargetConfigs(out.RunTargets)
	if len(out.RunTargets) == 0 {
		out.RunTargets = defaultRunTargetConfigs()
	}
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
	case provider.ProviderClaude, provider.ProviderGemini:
		// claude/gemini 不使用 reasoning_effort，避免跨 provider 残留。
		out.ReasoningEffort = ""
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

func defaultRunTargetConfigs() map[string]RunTargetConfig {
	return map[string]RunTargetConfig{
		"test": {
			Description:        "Run the default project test suite.",
			Command:            []string{"go", "test", "./..."},
			TimeoutMS:          20 * 60 * 1000,
			PreflightCommand:   []string{"go", "test", "./..."},
			PreflightTimeoutMS: 2 * 60 * 1000,
		},
		"lint": {
			Description:        "Run the default project linter entrypoint.",
			Command:            []string{"golangci-lint", "run"},
			TimeoutMS:          10 * 60 * 1000,
			PreflightCommand:   []string{"golangci-lint", "run"},
			PreflightTimeoutMS: 60 * 1000,
		},
		"build": {
			Description:        "Run the default project build entrypoint.",
			Command:            []string{"go", "build", "./..."},
			TimeoutMS:          15 * 60 * 1000,
			PreflightCommand:   []string{"go", "build", "./..."},
			PreflightTimeoutMS: 60 * 1000,
		},
	}
}

func normalizeMultiNodeConfig(in MultiNodeConfig) MultiNodeConfig {
	out := in
	out.DevBaseURL = strings.TrimSpace(out.DevBaseURL)
	out.DevProjectName = strings.TrimSpace(out.DevProjectName)
	out.RunBaseURL = strings.TrimSpace(out.RunBaseURL)
	out.RunProjectName = strings.TrimSpace(out.RunProjectName)
	if out.DevProjectName == "" {
		out.DevProjectName = ""
	}
	if out.RunProjectName == "" {
		out.RunProjectName = ""
	}
	if out.DevBaseURL == "" && out.RunBaseURL == "" {
		out.AutoRoute = false
	}
	if !out.AutoLinkLatestRunFailure {
		out.AutoLinkLatestRunFailure = true
	}
	return out
}

func normalizeRunTargetConfigs(in map[string]RunTargetConfig) map[string]RunTargetConfig {
	if len(in) == 0 {
		return map[string]RunTargetConfig{}
	}
	out := make(map[string]RunTargetConfig, len(in))
	for name, cfg := range in {
		key := strings.TrimSpace(strings.ToLower(name))
		if key == "" {
			continue
		}
		cfg.Description = strings.TrimSpace(cfg.Description)
		if cfg.TimeoutMS < 0 {
			cfg.TimeoutMS = 0
		}
		if cfg.PreflightTimeoutMS < 0 {
			cfg.PreflightTimeoutMS = 0
		}
		if cfg.BootstrapTimeoutMS < 0 {
			cfg.BootstrapTimeoutMS = 0
		}
		if cfg.RepairTimeoutMS < 0 {
			cfg.RepairTimeoutMS = 0
		}
		if len(cfg.Command) > 0 {
			args := make([]string, 0, len(cfg.Command))
			for _, arg := range cfg.Command {
				arg = strings.TrimSpace(arg)
				if arg == "" {
					continue
				}
				args = append(args, arg)
			}
			cfg.Command = args
		}
		if len(cfg.PreflightCommand) > 0 {
			args := make([]string, 0, len(cfg.PreflightCommand))
			for _, arg := range cfg.PreflightCommand {
				arg = strings.TrimSpace(arg)
				if arg == "" {
					continue
				}
				args = append(args, arg)
			}
			cfg.PreflightCommand = args
		}
		if len(cfg.BootstrapCommand) > 0 {
			args := make([]string, 0, len(cfg.BootstrapCommand))
			for _, arg := range cfg.BootstrapCommand {
				arg = strings.TrimSpace(arg)
				if arg == "" {
					continue
				}
				args = append(args, arg)
			}
			cfg.BootstrapCommand = args
		}
		if len(cfg.RepairCommand) > 0 {
			args := make([]string, 0, len(cfg.RepairCommand))
			for _, arg := range cfg.RepairCommand {
				arg = strings.TrimSpace(arg)
				if arg == "" {
					continue
				}
				args = append(args, arg)
			}
			cfg.RepairCommand = args
		}
		out[key] = cfg
	}
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
	if override.MultiNode.AutoRoute {
		out.MultiNode.AutoRoute = true
	}
	if strings.TrimSpace(override.MultiNode.DevBaseURL) != "" {
		out.MultiNode.DevBaseURL = strings.TrimSpace(override.MultiNode.DevBaseURL)
	}
	if strings.TrimSpace(override.MultiNode.DevProjectName) != "" {
		out.MultiNode.DevProjectName = strings.TrimSpace(override.MultiNode.DevProjectName)
	}
	if strings.TrimSpace(override.MultiNode.RunBaseURL) != "" {
		out.MultiNode.RunBaseURL = strings.TrimSpace(override.MultiNode.RunBaseURL)
	}
	if strings.TrimSpace(override.MultiNode.RunProjectName) != "" {
		out.MultiNode.RunProjectName = strings.TrimSpace(override.MultiNode.RunProjectName)
	}
	if len(override.RunTargets) > 0 {
		out.RunTargets = normalizeRunTargetConfigs(override.RunTargets)
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
