package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

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
			"升级配置为最新格式（全局 providers map + 角色引用）",
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

	homeDir, err := app.ResolveHomeDir(*home)
	if err != nil {
		exitRuntimeError(out, "解析 Home 目录失败", err.Error(), "通过 --home 指定有效目录")
	}
	h, err := app.OpenHome(homeDir)
	if err != nil {
		exitRuntimeError(out, "打开 Home 失败", err.Error(), "检查 Home 目录权限")
	}

	// ── 全局配置升级 ──────────────────────────────────────────────
	homeCfg, homeUpgraded := upgradeHomeConfig(h.ConfigPath, out)

	// ── 打开项目 ────────────────────────────────────────────────
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

	// ── 项目配置升级 ────────────────────────────────────────────
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

	// 校验 provider key
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

	projectUpgraded := upgradeProjectConfig(p.ConfigPath(), workerKey, pmKey, gatewayKey, out)

	// ── 输出 ────────────────────────────────────────────────────
	if out == outputJSON {
		printJSONOrExit(map[string]any{
			"schema":           "dalek.upgrade.config.v1",
			"project":          p.Name(),
			"schema_version":   repo.CurrentProjectSchemaVersion,
			"worker_agent":     workerKey,
			"pm_agent":         pmKey,
			"gateway_agent":    gatewayKey,
			"providers":        homeCfg.Providers,
			"home_upgraded":    homeUpgraded,
			"project_upgraded": projectUpgraded,
		})
		return
	}

	if !homeUpgraded && !projectUpgraded {
		fmt.Println("配置已是最新，无需升级。")
		return
	}
	fmt.Println("upgrade config 完成:")
	if homeUpgraded {
		fmt.Printf("  全局配置: schema %d (已升级)\n", app.CurrentHomeConfigSchemaVersion)
	}
	if projectUpgraded {
		fmt.Printf("  项目配置: schema %d (已升级)\n", repo.CurrentProjectSchemaVersion)
	}
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

// ── 全局配置升级 ────────────────────────────────────────────────────
//
// 流程：
//  1. 从文件读原始 JSON → 取 schema_version（不经过 WithDefaults）
//  2. schema == current → 跳过
//  3. schema < current →
//     a. backup 旧文件
//     b. 生成 default 模板
//     c. 从旧 JSON 按字段映射覆盖到模板
//     d. 写入
func upgradeHomeConfig(configPath string, out cliOutputFormat) (app.HomeConfig, bool) {
	rawBytes, _ := os.ReadFile(configPath)
	rawSchema := readRawSchemaVersion(rawBytes)

	if rawSchema >= app.CurrentHomeConfigSchemaVersion {
		// 已是最新，直接用 WithDefaults 返回（补内存默认值但不写文件）
		cfg, _, _, _ := app.LoadHomeConfig(configPath)
		return cfg.WithDefaults(), false
	}

	// backup
	backupConfigFile(configPath)

	// 新模板
	newCfg := app.DefaultHomeConfig()
	newCfg.Providers = repo.DefaultProviders()

	// 从旧 JSON 映射字段
	if len(rawBytes) > 0 {
		overlayHomeConfigFromRaw(rawBytes, &newCfg)
	}

	if err := app.WriteHomeConfigAtomic(configPath, newCfg); err != nil {
		exitRuntimeError(out, "写入全局配置失败", err.Error(), "检查文件权限")
	}
	return newCfg.WithDefaults(), true
}

// overlayHomeConfigFromRaw 从旧配置 JSON 提取可保留字段，覆盖到新模板上。
// 字段映射规则：
//   - daemon.*           → 1:1 直接覆盖
//   - gateway.*          → 1:1 直接覆盖
//   - agent.provider/model → 1:1 保留（旧兼容字段）
//   - providers          → 如果旧配置有自定义 providers，保留用户的，不用 default
func overlayHomeConfigFromRaw(rawBytes []byte, target *app.HomeConfig) {
	var old app.HomeConfig
	if err := json.Unmarshal(rawBytes, &old); err != nil {
		return
	}

	// daemon: 1:1 覆盖非零值
	if old.Daemon.PIDFile != "" {
		target.Daemon.PIDFile = old.Daemon.PIDFile
	}
	if old.Daemon.LockFile != "" {
		target.Daemon.LockFile = old.Daemon.LockFile
	}
	if old.Daemon.LogFile != "" {
		target.Daemon.LogFile = old.Daemon.LogFile
	}
	if old.Daemon.MaxConcurrent > 0 {
		target.Daemon.MaxConcurrent = old.Daemon.MaxConcurrent
	}
	if old.Daemon.Internal.Listen != "" {
		target.Daemon.Internal.Listen = old.Daemon.Internal.Listen
	}
	if old.Daemon.Public.Listen != "" {
		target.Daemon.Public.Listen = old.Daemon.Public.Listen
	}
	target.Daemon.Public.Feishu = old.Daemon.Public.Feishu
	target.Daemon.Public.Ingress = old.Daemon.Public.Ingress
	if old.Daemon.Web.Listen != "" {
		target.Daemon.Web.Listen = old.Daemon.Web.Listen
	}
	if old.Daemon.Notebook.WorkerCount > 0 {
		target.Daemon.Notebook.WorkerCount = old.Daemon.Notebook.WorkerCount
	}

	// gateway: 1:1 覆盖
	if old.Gateway.Listen != "" {
		target.Gateway.Listen = old.Gateway.Listen
	}
	if old.Gateway.InternalListen != "" {
		target.Gateway.InternalListen = old.Gateway.InternalListen
	}
	if len(old.Gateway.InternalAllowCIDRs) > 0 {
		target.Gateway.InternalAllowCIDRs = old.Gateway.InternalAllowCIDRs
	}
	if old.Gateway.QueueDepth > 0 {
		target.Gateway.QueueDepth = old.Gateway.QueueDepth
	}
	if old.Gateway.DBPath != "" {
		target.Gateway.DBPath = old.Gateway.DBPath
	}
	if old.Gateway.AuthToken != "" {
		target.Gateway.AuthToken = old.Gateway.AuthToken
	}
	target.Gateway.Feishu = old.Gateway.Feishu
	target.Gateway.Tunnel = old.Gateway.Tunnel

	// agent: 保留旧兼容字段
	if old.Agent.Provider != "" {
		target.Agent.Provider = old.Agent.Provider
	}
	if old.Agent.Model != "" {
		target.Agent.Model = old.Agent.Model
	}

	// providers: 如果旧配置有自定义的 providers，用旧的替代 default
	if len(old.Providers) > 0 {
		target.Providers = old.Providers
	}
}

// ── 项目配置升级 ────────────────────────────────────────────────────
//
// 流程同上：读原始 schema → 跳过/backup+模板+映射+写入
func upgradeProjectConfig(configPath, workerKey, pmKey, gatewayKey string, out cliOutputFormat) bool {
	rawBytes, _ := os.ReadFile(configPath)
	rawSchema := readRawSchemaVersion(rawBytes)

	if rawSchema >= repo.CurrentProjectSchemaVersion {
		return false
	}

	backupConfigFile(configPath)

	// 新模板
	newCfg := repo.Config{
		SchemaVersion: repo.CurrentProjectSchemaVersion,
		WorkerAgent:   repo.RoleConfig{Provider: workerKey},
		PMAgent:       repo.RoleConfig{Provider: pmKey},
		GatewayAgent:  repo.GatewayRoleConfig{Provider: gatewayKey},
	}

	// 从旧 JSON 映射字段
	if len(rawBytes) > 0 {
		overlayProjectConfigFromRaw(rawBytes, &newCfg)
	}

	if err := repo.WriteConfigAtomic(configPath, newCfg); err != nil {
		exitRuntimeError(out, "写入项目配置失败", err.Error(), "检查文件权限")
	}
	return true
}

// overlayProjectConfigFromRaw 从旧项目配置提取可保留字段。
// 字段映射规则：
//   - branch_prefix             → 1:1
//   - refresh_interval_ms       → 1:1
//   - manager_command           → 1:1
//   - gateway_agent.output/resume_output/turn_timeout_ms → 1:1
//   - notebook.*                → 1:1
//   - worker_agent.provider     → 提取 provider key（v2 中是类型名如 "codex"）
//   - pm_agent.provider         → 同上
//   - gateway_agent.provider    → 同上
//   - mode/model/command/bypass_permissions/danger_full_access → 丢弃
func overlayProjectConfigFromRaw(rawBytes []byte, target *repo.Config) {
	var raw struct {
		BranchPrefix    string `json:"branch_prefix"`
		RefreshInterval int    `json:"refresh_interval_ms"`
		ManagerCommand  string `json:"manager_command"`
		WorkerAgent     struct {
			Provider string `json:"provider"`
		} `json:"worker_agent"`
		PMAgent struct {
			Provider string `json:"provider"`
		} `json:"pm_agent"`
		GatewayAgent struct {
			Provider      string `json:"provider"`
			Output        string `json:"output"`
			ResumeOutput  string `json:"resume_output"`
			TurnTimeoutMS int    `json:"turn_timeout_ms"`
		} `json:"gateway_agent"`
		Notebook repo.NotebookConfig `json:"notebook"`
	}
	if err := json.Unmarshal(rawBytes, &raw); err != nil {
		return
	}

	// 1:1 字段
	if raw.BranchPrefix != "" {
		target.BranchPrefix = raw.BranchPrefix
	}
	if raw.RefreshInterval > 0 {
		target.RefreshIntervalMS = raw.RefreshInterval
	}
	if raw.ManagerCommand != "" {
		target.ManagerCommand = raw.ManagerCommand
	}
	target.Notebook = raw.Notebook

	// gateway 特有字段
	if raw.GatewayAgent.Output != "" {
		target.GatewayAgent.Output = raw.GatewayAgent.Output
	}
	if raw.GatewayAgent.ResumeOutput != "" {
		target.GatewayAgent.ResumeOutput = raw.GatewayAgent.ResumeOutput
	}
	if raw.GatewayAgent.TurnTimeoutMS > 0 {
		target.GatewayAgent.TurnTimeoutMS = raw.GatewayAgent.TurnTimeoutMS
	}

	// provider key：从旧配置继承（v2 中是 provider 类型名），如果用户没通过 CLI 指定则覆盖
	if p := strings.TrimSpace(raw.WorkerAgent.Provider); p != "" && target.WorkerAgent.Provider == "codex" {
		target.WorkerAgent.Provider = p
	}
	if p := strings.TrimSpace(raw.PMAgent.Provider); p != "" && target.PMAgent.Provider == "claude" {
		target.PMAgent.Provider = p
	}
	if p := strings.TrimSpace(raw.GatewayAgent.Provider); p != "" && target.GatewayAgent.Provider == "claude" {
		target.GatewayAgent.Provider = p
	}
}

// ── 通用工具 ────────────────────────────────────────────────────────

func readRawSchemaVersion(rawBytes []byte) int {
	if len(rawBytes) == 0 {
		return 0
	}
	var raw struct {
		SchemaVersion int `json:"schema_version"`
	}
	if err := json.Unmarshal(rawBytes, &raw); err != nil {
		return 0
	}
	return raw.SchemaVersion
}

func backupConfigFile(path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	dir := filepath.Dir(path)
	backupName := filepath.Base(path) + ".bak." + time.Now().Format("20060102-150405")
	_ = os.WriteFile(filepath.Join(dir, backupName), data, 0o644)
}

func validateProviderKeyExists(key string, providers map[string]repo.ProviderConfig) error {
	if _, ok := providers[key]; ok {
		return nil
	}
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
