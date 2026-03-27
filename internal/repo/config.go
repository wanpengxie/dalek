package repo

import (
	"dalek/internal/agent/provider"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ProviderConfig 是全局 providers map 中的一个命名配置条目。
// key 为任意用户标签（如 "claude"、"sonnet"、"codex"），每条通过 Type 字段声明底层 provider 类型。
type ProviderConfig struct {
	Type            string   `json:"type"`                       // "codex" | "claude" | "gemini"
	Model           string   `json:"model,omitempty"`            // 模型名称
	ReasoningEffort string   `json:"reasoning_effort,omitempty"` // codex only
	Permission      string   `json:"permission,omitempty"`       // "auto" | "bypass", 默认 "auto"
	ExtraFlags      []string `json:"extra_flags,omitempty"`
}

// RoleConfig 角色配置，只引用全局 providers map 的 key。
type RoleConfig struct {
	Provider string `json:"provider"` // 对应全局 providers map 的 key
}

// GatewayRoleConfig gateway 角色配置 + gateway 特有字段。
type GatewayRoleConfig struct {
	Provider      string `json:"provider"`                  // 对应全局 providers map 的 key
	Output        string `json:"output,omitempty"`          // gateway 输出格式
	ResumeOutput  string `json:"resume_output,omitempty"`   // gateway 续跑输出格式
	TurnTimeoutMS int    `json:"turn_timeout_ms,omitempty"` // 单轮超时（毫秒）
}

// Config 是项目配置（.dalek/config.json, schema_version=3）。
// 角色只引用 provider key，不携带任何 provider 参数。
type Config struct {
	SchemaVersion     int               `json:"schema_version"`
	BranchPrefix      string            `json:"branch_prefix"`
	RefreshIntervalMS int               `json:"refresh_interval_ms"`
	WorkerAgent       RoleConfig        `json:"worker_agent"`
	PMAgent           RoleConfig        `json:"pm_agent"`
	ManagerCommand    string            `json:"manager_command"`
	GatewayAgent      GatewayRoleConfig `json:"gateway_agent"`
	Notebook          NotebookConfig    `json:"notebook"`

	notebookSet              bool
	notebookEnabledSet       bool
	notebookAutoShapeSet     bool
	notebookShapeIntervalSet bool
}

type NotebookConfig struct {
	Enabled          bool `json:"enabled"`
	AutoShape        bool `json:"auto_shape"`
	ShapeIntervalSec int  `json:"shape_interval_sec"`
}

const (
	defaultWorkerProvider      = provider.ProviderCodex
	defaultPMProvider          = provider.ProviderClaude
	defaultNotebookEnabled     = true
	defaultNotebookAutoShape   = true
	defaultNotebookIntervalSec = 60
)

// CurrentProjectSchemaVersion 是项目结构迁移版本号（非 binary semver）。
// v3: 角色只引用 provider key，不携带 model/mode/permission 等参数。
const CurrentProjectSchemaVersion = 3

var projectConfigDeprecationWarnf = func(format string, args ...any) {
	// 保留变量以便测试覆盖，但 v3 不再有 deprecation warning（旧格式直接 error）。
	_ = fmt.Sprintf(format, args...)
}

// deprecatedAgentFields 是 worker_agent/pm_agent 中不再允许的字段名（v2 遗留）。
var deprecatedAgentFields = []string{
	"mode", "command", "model", "reasoning_effort", "extra_flags",
	"danger_full_access", "bypass_permissions",
}

// deprecatedGatewayFields 是 gateway_agent 中不再允许的字段名（v2 遗留）。
var deprecatedGatewayFields = []string{
	"mode", "command", "model",
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
	out.WorkerAgent.Provider = strings.TrimSpace(out.WorkerAgent.Provider)
	out.PMAgent.Provider = strings.TrimSpace(out.PMAgent.Provider)
	out.GatewayAgent.Provider = strings.TrimSpace(out.GatewayAgent.Provider)
	out.GatewayAgent.Output = strings.TrimSpace(out.GatewayAgent.Output)
	out.GatewayAgent.ResumeOutput = strings.TrimSpace(out.GatewayAgent.ResumeOutput)
	if out.WorkerAgent.Provider == "" {
		out.WorkerAgent.Provider = defaultWorkerProvider
	}
	if out.PMAgent.Provider == "" {
		out.PMAgent.Provider = defaultPMProvider
	}
	if out.GatewayAgent.Provider == "" {
		out.GatewayAgent.Provider = defaultPMProvider
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

func LoadConfig(path string) (Config, bool, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Config{}, false, err
	}
	if err := ValidateNoDeprecatedProjectFields(b); err != nil {
		return Config{}, false, err
	}
	var cfg Config
	if err := json.Unmarshal(b, &cfg); err != nil {
		return Config{}, false, err
	}
	if cfg.SchemaVersion > 0 && cfg.SchemaVersion < CurrentProjectSchemaVersion {
		return Config{}, false, fmt.Errorf(
			"config schema v%d 已不再支持\nCause: 当前配置格式已废弃，无法自动迁移\nFix:   运行 `dalek upgrade config` 重新生成配置",
			cfg.SchemaVersion,
		)
	}
	needsRewrite := cfg.SchemaVersion <= 0 ||
		strings.TrimSpace(cfg.WorkerAgent.Provider) == "" ||
		strings.TrimSpace(cfg.PMAgent.Provider) == "" ||
		!cfg.notebookSet ||
		!cfg.notebookEnabledSet ||
		!cfg.notebookAutoShapeSet ||
		!cfg.notebookShapeIntervalSet
	return cfg.WithDefaults(), needsRewrite, nil
}

// ValidateNoDeprecatedProjectFields 检测 raw JSON 中是否包含 v2 已废弃字段。
// 若检测到旧字段，返回 error 指引用户运行 `dalek upgrade config`。
func ValidateNoDeprecatedProjectFields(raw []byte) error {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(raw, &root); err != nil {
		return nil // 让正常 unmarshal 处理语法错误
	}
	for _, agentKey := range []string{"worker_agent", "pm_agent"} {
		agentRaw, ok := root[agentKey]
		if !ok {
			continue
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(agentRaw, &fields); err != nil {
			continue
		}
		for _, f := range deprecatedAgentFields {
			if _, exists := fields[f]; exists {
				return fmt.Errorf(
					"config schema v2 已不再支持：字段 %s.%s 已废弃\nFix:   运行 `dalek upgrade config` 重新生成配置",
					agentKey, f,
				)
			}
		}
	}
	if gaRaw, ok := root["gateway_agent"]; ok {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(gaRaw, &fields); err == nil {
			for _, f := range deprecatedGatewayFields {
				if _, exists := fields[f]; exists {
					return fmt.Errorf(
						"config schema v2 已不再支持：字段 gateway_agent.%s 已废弃\nFix:   运行 `dalek upgrade config` 重新生成配置",
						f,
					)
				}
			}
		}
	}
	return nil
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
	if strings.TrimSpace(override.WorkerAgent.Provider) != "" {
		out.WorkerAgent.Provider = strings.TrimSpace(override.WorkerAgent.Provider)
	}
	if strings.TrimSpace(override.PMAgent.Provider) != "" {
		out.PMAgent.Provider = strings.TrimSpace(override.PMAgent.Provider)
	}
	if strings.TrimSpace(override.ManagerCommand) != "" {
		out.ManagerCommand = strings.TrimSpace(override.ManagerCommand)
	}
	if strings.TrimSpace(override.GatewayAgent.Provider) != "" {
		out.GatewayAgent.Provider = strings.TrimSpace(override.GatewayAgent.Provider)
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
	return out.WithDefaults()
}

// DefaultProviders 返回默认的全局 providers map。
// key 为 provider 类型名，方便默认配置下 provider key == provider type。
func DefaultProviders() map[string]ProviderConfig {
	return map[string]ProviderConfig{
		provider.ProviderCodex: {
			Type:            provider.ProviderCodex,
			Model:           provider.DefaultModel(provider.ProviderCodex),
			ReasoningEffort: provider.DefaultReasoningEffort(provider.ProviderCodex),
			Permission:      "bypass",
		},
		provider.ProviderClaude: {
			Type:       provider.ProviderClaude,
			Model:      provider.DefaultModel(provider.ProviderClaude),
			Permission: "bypass",
		},
		provider.ProviderGemini: {
			Type:       provider.ProviderGemini,
			Model:      provider.DefaultModel(provider.ProviderGemini),
			Permission: "auto",
		},
	}
}
