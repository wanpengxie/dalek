package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	agentprovider "dalek/internal/agent/provider"
	"dalek/internal/app"
	"dalek/internal/repo"
)

func cmdUpgradeConfig(args []string) {
	fs := flag.NewFlagSet("upgrade config", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		printSubcommandUsage(
			fs,
			"重建项目配置为 v3 格式（全局 providers map + 角色引用）",
			"dalek upgrade config [--worker <provider_key>] [--pm <provider_key>] [--gateway <provider_key>] [--project <name>]",
			"dalek upgrade config",
			"dalek upgrade config --worker codex --pm claude --gateway claude",
		)
	}
	home := fs.String("home", globalHome, "dalek Home 目录（默认 ~/.dalek）")
	project := fs.String("project", globalProject, "项目名（可选，默认从当前目录推断）")
	projectShort := fs.String("p", globalProject, "项目名（可选，默认从当前目录推断）")
	worker := fs.String("worker", "", "worker_agent 使用的 provider key（默认 codex）")
	pm := fs.String("pm", "", "pm_agent 使用的 provider key（默认 claude）")
	gateway := fs.String("gateway", "", "gateway_agent 使用的 provider key（默认 claude）")
	output := addOutputFlag(fs, "输出格式: text|json（默认 text）")
	parseFlagSetOrExit(fs, args, globalOutput, "upgrade config 参数解析失败", "运行 dalek upgrade config --help 查看参数")
	if strings.TrimSpace(*projectShort) != "" {
		*project = strings.TrimSpace(*projectShort)
	}
	out := parseOutputOrExit(*output, true)

	// 打开 Home 获取全局 providers map
	homeDir, err := app.ResolveHomeDir(*home)
	if err != nil {
		exitRuntimeError(out, "解析 Home 目录失败", err.Error(), "通过 --home 指定有效目录")
	}
	h, err := app.OpenHome(homeDir)
	if err != nil {
		exitRuntimeError(out, "打开 Home 失败", err.Error(), "检查 Home 目录权限")
	}

	// 全局配置：schema 更低时覆写（补 providers map），schema 相同则不动
	homeCfg := h.Config.WithDefaults()
	if homeCfg.SchemaVersion < app.CurrentHomeConfigSchemaVersion {
		if len(homeCfg.Providers) == 0 {
			homeCfg.Providers = repo.DefaultProviders()
		}
		homeCfg.SchemaVersion = app.CurrentHomeConfigSchemaVersion
		if err := app.WriteHomeConfigAtomic(h.ConfigPath, homeCfg); err != nil {
			exitRuntimeError(out, "写入 Home 配置失败", err.Error(), "检查文件权限")
		}
	} else if len(homeCfg.Providers) == 0 {
		// schema 已是最新但缺 providers（极端情况），补上但不碰其他字段
		homeCfg.Providers = repo.DefaultProviders()
		if err := app.WriteHomeConfigAtomic(h.ConfigPath, homeCfg); err != nil {
			exitRuntimeError(out, "写入 Home 配置失败", err.Error(), "检查文件权限")
		}
	}

	// 打开项目
	proj := strings.TrimSpace(*project)
	var p *app.Project
	if proj != "" {
		p, err = h.OpenProjectByName(proj)
	} else {
		wd, _ := os.Getwd()
		p, err = h.OpenProjectFromDir(wd)
	}
	if err != nil {
		exitRuntimeError(out, "打开项目失败", err.Error(), "使用 --project 指定项目名，或先运行 dalek init")
	}

	// 确定每个角色的 provider key
	workerKey := strings.TrimSpace(*worker)
	if workerKey == "" {
		workerKey = "codex"
	}
	pmKey := strings.TrimSpace(*pm)
	if pmKey == "" {
		pmKey = "claude"
	}
	gatewayKey := strings.TrimSpace(*gateway)
	if gatewayKey == "" {
		gatewayKey = "claude"
	}

	// 校验 provider key 在 providers map 中存在或是合法类型名
	for _, pair := range []struct{ role, key string }{
		{"worker", workerKey},
		{"pm", pmKey},
		{"gateway", gatewayKey},
	} {
		if err := validateProviderKeyExists(pair.key, homeCfg.Providers); err != nil {
			exitRuntimeError(out,
				fmt.Sprintf("%s provider key %q 无效", pair.role, pair.key),
				err.Error(),
				"检查全局 providers map 或使用合法 provider 类型名",
			)
		}
	}

	// 尝试读取现有项目配置中可保留的字段（branch_prefix, gateway 参数, notebook）
	// 注意：旧 v2 配置可能加载失败，此时用空值
	existingCfg := loadExistingConfigBestEffort(p.ConfigPath())

	// 构造新的 v3 项目配置
	newCfg := repo.Config{
		SchemaVersion: repo.CurrentProjectSchemaVersion,
		BranchPrefix:  existingCfg.BranchPrefix,
		WorkerAgent:   repo.RoleConfig{Provider: workerKey},
		PMAgent:       repo.RoleConfig{Provider: pmKey},
		GatewayAgent: repo.GatewayRoleConfig{
			Provider:      gatewayKey,
			Output:        existingCfg.GatewayAgent.Output,
			ResumeOutput:  existingCfg.GatewayAgent.ResumeOutput,
			TurnTimeoutMS: existingCfg.GatewayAgent.TurnTimeoutMS,
		},
		Notebook: existingCfg.Notebook,
	}

	// 写入
	if err := repo.WriteConfigAtomic(p.ConfigPath(), newCfg); err != nil {
		exitRuntimeError(out, "写入项目配置失败", err.Error(), "检查文件权限")
	}

	if out == outputJSON {
		printJSONOrExit(map[string]any{
			"schema":         "dalek.upgrade.config.v1",
			"project":        p.Name(),
			"schema_version": repo.CurrentProjectSchemaVersion,
			"worker_agent":   workerKey,
			"pm_agent":       pmKey,
			"gateway_agent":  gatewayKey,
			"providers":      homeCfg.Providers,
		})
		return
	}

	fmt.Println("upgrade config 完成:")
	fmt.Printf("  schema_version: %d\n", repo.CurrentProjectSchemaVersion)
	fmt.Printf("  worker_agent.provider:  %s\n", workerKey)
	fmt.Printf("  pm_agent.provider:      %s\n", pmKey)
	fmt.Printf("  gateway_agent.provider: %s\n", gatewayKey)
	fmt.Println()
	fmt.Println("providers (global):")
	for key, pc := range homeCfg.Providers {
		perm := strings.TrimSpace(pc.Permission)
		if perm == "" {
			perm = "auto"
		}
		fmt.Printf("  %s → %s / %s / %s\n", key, pc.Type, pc.Model, perm)
	}
	fmt.Println()
	fmt.Println("next: 运行 `dalek config ls` 确认配置。")
}

// loadExistingConfigBestEffort 尝试从项目配置文件中提取可保留字段。
// 旧 v2 配置文件可能无法通过 LoadConfig 校验，因此用宽松的 JSON 反序列化。
func loadExistingConfigBestEffort(configPath string) repo.Config {
	b, err := os.ReadFile(configPath)
	if err != nil {
		return repo.Config{}
	}
	// 用宽松的结构体解析，只提取需要保留的字段
	var raw struct {
		BranchPrefix string `json:"branch_prefix"`
		GatewayAgent struct {
			Output        string `json:"output"`
			ResumeOutput  string `json:"resume_output"`
			TurnTimeoutMS int    `json:"turn_timeout_ms"`
		} `json:"gateway_agent"`
		Notebook repo.NotebookConfig `json:"notebook"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return repo.Config{}
	}
	return repo.Config{
		BranchPrefix: raw.BranchPrefix,
		GatewayAgent: repo.GatewayRoleConfig{
			Output:        raw.GatewayAgent.Output,
			ResumeOutput:  raw.GatewayAgent.ResumeOutput,
			TurnTimeoutMS: raw.GatewayAgent.TurnTimeoutMS,
		},
		Notebook: raw.Notebook,
	}
}

func validateProviderKeyExists(key string, providers map[string]repo.ProviderConfig) error {
	if _, ok := providers[key]; ok {
		return nil
	}
	// fallback: provider type name
	normalized := agentprovider.NormalizeProvider(key)
	if agentprovider.IsSupportedProvider(normalized) {
		return nil
	}
	keys := make([]string, 0, len(providers))
	for k := range providers {
		keys = append(keys, k)
	}
	return fmt.Errorf("不在 providers map 中（可用: %s）", strings.Join(keys, ", "))
}
