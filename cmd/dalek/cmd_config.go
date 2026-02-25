package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"

	"dalek/internal/app"
	"dalek/internal/repo"
)

type configScope string

const (
	configScopeDefault configScope = "default"
	configScopeGlobal  configScope = "global"
	configScopeLocal   configScope = "local"
	configScopeDB      configScope = "db"
)

const (
	configKeyDaemonInternalListen     = "daemon.internal.listen"
	configKeyDaemonPublicListen       = "daemon.public.listen"
	configKeyDaemonMaxConcurrent      = "daemon.max_concurrent"
	configKeyProjectMaxRunningWorkers = "project.max_running_workers"
	configKeyAgentProvider            = "agent.provider"
	configKeyAgentModel               = "agent.model"
)

type configKeyMeta struct {
	Key           string
	DefaultScope  configScope
	AllowedScopes []configScope
}

var configKeyOrder = []configKeyMeta{
	{
		Key:           configKeyDaemonInternalListen,
		DefaultScope:  configScopeGlobal,
		AllowedScopes: []configScope{configScopeGlobal},
	},
	{
		Key:           configKeyDaemonPublicListen,
		DefaultScope:  configScopeGlobal,
		AllowedScopes: []configScope{configScopeGlobal},
	},
	{
		Key:           configKeyDaemonMaxConcurrent,
		DefaultScope:  configScopeGlobal,
		AllowedScopes: []configScope{configScopeGlobal},
	},
	{
		Key:           configKeyProjectMaxRunningWorkers,
		DefaultScope:  configScopeDB,
		AllowedScopes: []configScope{configScopeDB},
	},
	{
		Key:           configKeyAgentProvider,
		DefaultScope:  configScopeLocal,
		AllowedScopes: []configScope{configScopeGlobal, configScopeLocal},
	},
	{
		Key:           configKeyAgentModel,
		DefaultScope:  configScopeLocal,
		AllowedScopes: []configScope{configScopeGlobal, configScopeLocal},
	},
}

type configHomePresence struct {
	DaemonInternalListen bool
	DaemonPublicListen   bool
	DaemonMaxConcurrent  bool
	AgentProvider        bool
	AgentModel           bool
}

type configLocalPresence struct {
	AgentProvider bool
	AgentModel    bool
}

type configEvalContext struct {
	homeCfg       app.HomeConfig
	homePresence  configHomePresence
	project       *app.Project
	localCfg      repo.Config
	localPresence configLocalPresence
}

type configSetContext struct {
	home     *app.Home
	homeCfg  app.HomeConfig
	project  *app.Project
	localCfg repo.Config
}

type configItem struct {
	Key    string      `json:"key"`
	Value  string      `json:"value"`
	Source configScope `json:"source"`
}

func cmdConfig(args []string) {
	if len(args) == 0 {
		printConfigUsage()
		os.Exit(2)
	}
	sub := strings.TrimSpace(args[0])
	switch sub {
	case "ls":
		cmdConfigList(args[1:])
	case "get":
		cmdConfigGet(args[1:])
	case "set":
		cmdConfigSet(args[1:])
	case "help", "-h", "--help":
		printConfigUsage()
		os.Exit(0)
	default:
		exitUsageError(globalOutput,
			fmt.Sprintf("未知 config 子命令: %s", sub),
			"config 命令组仅支持固定子命令",
			"运行 dalek config --help 查看可用子命令",
		)
	}
}

func printConfigUsage() {
	printGroupUsage("统一配置管理", "dalek config <command> [flags]", []string{
		"ls      列出已知配置项及生效来源层",
		"get     查看单个配置项",
		"set     设置配置项（支持 --global/--local）",
	})
	fmt.Fprintln(os.Stderr, "Use \"dalek config <command> --help\" for more information.")
}

func cmdConfigList(args []string) {
	fs := flag.NewFlagSet("config ls", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		printSubcommandUsage(
			fs,
			"列出配置项",
			"dalek config ls [--project <name>] [--output text|json]",
			"dalek config ls",
			"dalek config ls -p demo -o json",
		)
	}
	home := fs.String("home", globalHome, "dalek Home 目录（默认 ~/.dalek）")
	proj := fs.String("project", globalProject, "项目名（可选，默认从当前目录推断）")
	projShort := fs.String("p", globalProject, "项目名（可选，默认从当前目录推断）")
	output := addOutputFlag(fs, "输出格式: text|json（默认 text）")
	parseFlagSetOrExit(fs, args, globalOutput, "config ls 参数解析失败", "运行 dalek config ls --help 查看参数")
	if strings.TrimSpace(*projShort) != "" {
		*proj = strings.TrimSpace(*projShort)
	}
	out := parseOutputOrExit(*output, true)

	eval := mustBuildConfigEvalContext(out, *home, *proj, true)
	items := make([]configItem, 0, len(configKeyOrder))
	for _, meta := range configKeyOrder {
		value, source, err := resolveConfigValue(meta.Key, eval)
		if err != nil {
			exitRuntimeError(out,
				fmt.Sprintf("读取配置项失败: %s", meta.Key),
				err.Error(),
				"检查项目与配置文件状态后重试",
			)
		}
		items = append(items, configItem{
			Key:    meta.Key,
			Value:  value,
			Source: source,
		})
	}

	if out == outputJSON {
		printJSONOrExit(map[string]any{
			"schema":  "dalek.config.list.v1",
			"configs": items,
		})
		return
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "KEY\tVALUE\tSOURCE")
	for _, it := range items {
		fmt.Fprintf(tw, "%s\t%s\t%s\n", it.Key, it.Value, it.Source)
	}
	_ = tw.Flush()
}

func cmdConfigGet(args []string) {
	fs := flag.NewFlagSet("config get", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		printSubcommandUsage(
			fs,
			"查看单个配置项",
			"dalek config get <key> [--project <name>] [--output text|json]",
			"dalek config get daemon.internal.listen",
			"dalek config get project.max_running_workers -p demo -o json",
		)
	}
	home := fs.String("home", globalHome, "dalek Home 目录（默认 ~/.dalek）")
	proj := fs.String("project", globalProject, "项目名（可选，默认从当前目录推断）")
	projShort := fs.String("p", globalProject, "项目名（可选，默认从当前目录推断）")
	output := addOutputFlag(fs, "输出格式: text|json（默认 text）")
	flagArgs, rest, splitErr := splitConfigArgs(args,
		map[string]bool{
			"--home": true, "-home": true,
			"--project": true, "-project": true, "-p": true,
			"--output": true, "-o": true,
		},
		nil,
	)
	if splitErr != nil {
		exitUsageError(globalOutput,
			"config get 参数解析失败",
			splitErr.Error(),
			"运行 dalek config get --help 查看参数",
		)
	}
	parseFlagSetOrExit(fs, flagArgs, globalOutput, "config get 参数解析失败", "运行 dalek config get --help 查看参数")
	if strings.TrimSpace(*projShort) != "" {
		*proj = strings.TrimSpace(*projShort)
	}
	out := parseOutputOrExit(*output, true)

	if len(rest) != 1 {
		exitUsageError(out,
			"config get 需要且仅需要一个 key 参数",
			"参数数量不正确",
			"例如: dalek config get daemon.internal.listen",
		)
	}
	key := normalizeConfigKey(rest[0])
	if _, ok := findConfigKeyMeta(key); !ok {
		exitUsageError(out,
			fmt.Sprintf("未知配置键: %s", strings.TrimSpace(rest[0])),
			"不在支持的配置键列表中",
			"运行 dalek config ls 查看可用 key",
		)
	}

	eval := mustBuildConfigEvalContext(out, *home, *proj, true)
	value, source, err := resolveConfigValue(key, eval)
	if err != nil {
		exitRuntimeError(out,
			fmt.Sprintf("读取配置项失败: %s", key),
			err.Error(),
			"检查项目与配置文件状态后重试",
		)
	}

	if out == outputJSON {
		printJSONOrExit(map[string]any{
			"schema": "dalek.config.get.v1",
			"key":    key,
			"value":  value,
			"source": source,
		})
		return
	}
	fmt.Printf("%s=%s (source=%s)\n", key, value, source)
}

func cmdConfigSet(args []string) {
	fs := flag.NewFlagSet("config set", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		printSubcommandUsage(
			fs,
			"设置配置项",
			"dalek config set <key> <value> [--global|--local] [--project <name>] [--output text|json]",
			"dalek config set daemon.max_concurrent 8 --global",
			"dalek config set agent.provider claude --local -p demo",
		)
	}
	home := fs.String("home", globalHome, "dalek Home 目录（默认 ~/.dalek）")
	proj := fs.String("project", globalProject, "项目名（可选，默认从当前目录推断）")
	projShort := fs.String("p", globalProject, "项目名（可选，默认从当前目录推断）")
	forceGlobal := fs.Bool("global", false, "强制写入全局层（~/.dalek/config.json）")
	forceLocal := fs.Bool("local", false, "强制写入项目层（.dalek/config.json）")
	output := addOutputFlag(fs, "输出格式: text|json（默认 text）")
	flagArgs, rest, splitErr := splitConfigArgs(args,
		map[string]bool{
			"--home": true, "-home": true,
			"--project": true, "-project": true, "-p": true,
			"--output": true, "-o": true,
		},
		map[string]bool{
			"--global": true,
			"--local":  true,
		},
	)
	if splitErr != nil {
		exitUsageError(globalOutput,
			"config set 参数解析失败",
			splitErr.Error(),
			"运行 dalek config set --help 查看参数",
		)
	}
	parseFlagSetOrExit(fs, flagArgs, globalOutput, "config set 参数解析失败", "运行 dalek config set --help 查看参数")
	if strings.TrimSpace(*projShort) != "" {
		*proj = strings.TrimSpace(*projShort)
	}
	out := parseOutputOrExit(*output, true)

	if len(rest) != 2 {
		exitUsageError(out,
			"config set 需要 key 和 value 两个参数",
			"参数数量不正确",
			"例如: dalek config set daemon.max_concurrent 8",
		)
	}
	key := normalizeConfigKey(rest[0])
	rawValue := strings.TrimSpace(rest[1])
	if rawValue == "" {
		exitUsageError(out,
			"value 不能为空",
			"配置值为空",
			"请提供非空 value，例如: dalek config set agent.model gpt-5.3-codex",
		)
	}

	meta, ok := findConfigKeyMeta(key)
	if !ok {
		exitUsageError(out,
			fmt.Sprintf("未知配置键: %s", strings.TrimSpace(rest[0])),
			"不在支持的配置键列表中",
			"运行 dalek config ls 查看可用 key",
		)
	}
	scope := resolveConfigSetScopeOrExit(out, meta, *forceGlobal, *forceLocal)

	homeHandle := mustOpenHomeForConfig(out, *home)
	setCtx := &configSetContext{
		home:    homeHandle,
		homeCfg: homeHandle.Config.WithDefaults(),
	}
	if scope == configScopeLocal || scope == configScopeDB {
		p := mustOpenProjectForConfig(out, homeHandle, *proj)
		setCtx.project = p
		localCfg, err := loadProjectConfigFromProject(p)
		if err != nil {
			exitRuntimeError(out,
				"读取项目配置失败",
				err.Error(),
				"检查项目 .dalek/config.json 后重试",
			)
		}
		setCtx.localCfg = localCfg
	}

	normalizedValue, err := setConfigValue(setCtx, key, scope, rawValue)
	if err != nil {
		exitRuntimeError(out,
			fmt.Sprintf("写入配置项失败: %s", key),
			err.Error(),
			"检查参数与配置文件权限后重试",
		)
	}

	if out == outputJSON {
		printJSONOrExit(map[string]any{
			"schema": "dalek.config.set.v1",
			"key":    key,
			"value":  normalizedValue,
			"source": scope,
		})
		return
	}
	fmt.Printf("%s=%s (source=%s)\n", key, normalizedValue, scope)
}

func resolveConfigValue(key string, eval *configEvalContext) (string, configScope, error) {
	if eval == nil {
		return "", configScopeDefault, fmt.Errorf("config eval context 为空")
	}
	switch normalizeConfigKey(key) {
	case configKeyDaemonInternalListen:
		value := strings.TrimSpace(eval.homeCfg.WithDefaults().Daemon.Internal.Listen)
		if eval.homePresence.DaemonInternalListen {
			return value, configScopeGlobal, nil
		}
		return value, configScopeDefault, nil
	case configKeyDaemonPublicListen:
		value := strings.TrimSpace(eval.homeCfg.WithDefaults().Daemon.Public.Listen)
		if eval.homePresence.DaemonPublicListen {
			return value, configScopeGlobal, nil
		}
		return value, configScopeDefault, nil
	case configKeyDaemonMaxConcurrent:
		value := strconv.Itoa(eval.homeCfg.WithDefaults().Daemon.MaxConcurrent)
		if eval.homePresence.DaemonMaxConcurrent {
			return value, configScopeGlobal, nil
		}
		return value, configScopeDefault, nil
	case configKeyProjectMaxRunningWorkers:
		if eval.project == nil {
			return "", configScopeDB, fmt.Errorf("project 为空")
		}
		st, err := eval.project.GetPMState(context.Background())
		if err != nil {
			return "", configScopeDB, err
		}
		return strconv.Itoa(st.MaxRunningWorkers), configScopeDB, nil
	case configKeyAgentProvider:
		effective := buildEffectiveProjectConfig(eval.homeCfg, eval.localCfg)
		value := strings.TrimSpace(strings.ToLower(effective.WorkerAgent.Provider))
		if eval.localPresence.AgentProvider {
			return value, configScopeLocal, nil
		}
		if eval.homePresence.AgentProvider {
			return value, configScopeGlobal, nil
		}
		return value, configScopeDefault, nil
	case configKeyAgentModel:
		effective := buildEffectiveProjectConfig(eval.homeCfg, eval.localCfg)
		value := strings.TrimSpace(effective.WorkerAgent.Model)
		if eval.localPresence.AgentModel {
			return value, configScopeLocal, nil
		}
		if eval.homePresence.AgentModel {
			return value, configScopeGlobal, nil
		}
		return value, configScopeDefault, nil
	default:
		return "", configScopeDefault, fmt.Errorf("未知配置键: %s", key)
	}
}

func setConfigValue(ctx *configSetContext, key string, scope configScope, rawValue string) (string, error) {
	if ctx == nil {
		return "", fmt.Errorf("config set context 为空")
	}
	switch normalizeConfigKey(key) {
	case configKeyDaemonInternalListen:
		if scope != configScopeGlobal {
			return "", fmt.Errorf("%s 仅支持 global 层", key)
		}
		ctx.homeCfg.Daemon.Internal.Listen = strings.TrimSpace(rawValue)
		if strings.TrimSpace(ctx.homeCfg.Daemon.Internal.Listen) == "" {
			return "", fmt.Errorf("daemon.internal.listen 不能为空")
		}
		if err := app.WriteHomeConfigAtomic(ctx.home.ConfigPath, ctx.homeCfg); err != nil {
			return "", err
		}
		return ctx.homeCfg.WithDefaults().Daemon.Internal.Listen, nil
	case configKeyDaemonPublicListen:
		if scope != configScopeGlobal {
			return "", fmt.Errorf("%s 仅支持 global 层", key)
		}
		ctx.homeCfg.Daemon.Public.Listen = strings.TrimSpace(rawValue)
		if strings.TrimSpace(ctx.homeCfg.Daemon.Public.Listen) == "" {
			return "", fmt.Errorf("daemon.public.listen 不能为空")
		}
		if err := app.WriteHomeConfigAtomic(ctx.home.ConfigPath, ctx.homeCfg); err != nil {
			return "", err
		}
		return ctx.homeCfg.WithDefaults().Daemon.Public.Listen, nil
	case configKeyDaemonMaxConcurrent:
		if scope != configScopeGlobal {
			return "", fmt.Errorf("%s 仅支持 global 层", key)
		}
		n, err := strconv.Atoi(strings.TrimSpace(rawValue))
		if err != nil {
			return "", fmt.Errorf("daemon.max_concurrent 必须是正整数: %w", err)
		}
		if n <= 0 {
			return "", fmt.Errorf("daemon.max_concurrent 必须大于 0")
		}
		ctx.homeCfg.Daemon.MaxConcurrent = n
		if err := app.WriteHomeConfigAtomic(ctx.home.ConfigPath, ctx.homeCfg); err != nil {
			return "", err
		}
		return strconv.Itoa(ctx.homeCfg.WithDefaults().Daemon.MaxConcurrent), nil
	case configKeyProjectMaxRunningWorkers:
		if scope != configScopeDB {
			return "", fmt.Errorf("%s 仅支持 db 层", key)
		}
		if ctx.project == nil {
			return "", fmt.Errorf("project 为空")
		}
		n, err := strconv.Atoi(strings.TrimSpace(rawValue))
		if err != nil {
			return "", fmt.Errorf("project.max_running_workers 必须是整数: %w", err)
		}
		if n < 1 || n > 32 {
			return "", fmt.Errorf("project.max_running_workers 取值范围为 1-32")
		}
		st, err := ctx.project.SetMaxRunningWorkers(context.Background(), n)
		if err != nil {
			return "", err
		}
		return strconv.Itoa(st.MaxRunningWorkers), nil
	case configKeyAgentProvider:
		provider := strings.TrimSpace(strings.ToLower(rawValue))
		if provider != "codex" && provider != "claude" {
			return "", fmt.Errorf("agent.provider 仅支持 codex 或 claude")
		}
		switch scope {
		case configScopeGlobal:
			ctx.homeCfg.Agent.Provider = provider
			if err := app.WriteHomeConfigAtomic(ctx.home.ConfigPath, ctx.homeCfg); err != nil {
				return "", err
			}
			return strings.TrimSpace(strings.ToLower(ctx.homeCfg.WithDefaults().Agent.Provider)), nil
		case configScopeLocal:
			if ctx.project == nil {
				return "", fmt.Errorf("project 为空")
			}
			pc := app.ProjectConfig(ctx.localCfg)
			app.ApplyAgentProviderModelOverride(&pc, provider, "")
			next := repo.Config(pc).WithDefaults()
			if err := repo.WriteConfigAtomic(repo.NewLayout(ctx.project.RepoRoot()).ConfigPath, next); err != nil {
				return "", err
			}
			return strings.TrimSpace(strings.ToLower(next.WorkerAgent.Provider)), nil
		default:
			return "", fmt.Errorf("agent.provider 不支持 scope=%s", scope)
		}
	case configKeyAgentModel:
		model := strings.TrimSpace(rawValue)
		if model == "" {
			return "", fmt.Errorf("agent.model 不能为空")
		}
		switch scope {
		case configScopeGlobal:
			ctx.homeCfg.Agent.Model = model
			if err := app.WriteHomeConfigAtomic(ctx.home.ConfigPath, ctx.homeCfg); err != nil {
				return "", err
			}
			return strings.TrimSpace(ctx.homeCfg.WithDefaults().Agent.Model), nil
		case configScopeLocal:
			if ctx.project == nil {
				return "", fmt.Errorf("project 为空")
			}
			next := ctx.localCfg.WithDefaults()
			next.WorkerAgent.Model = model
			next.PMAgent.Model = model
			if err := repo.WriteConfigAtomic(repo.NewLayout(ctx.project.RepoRoot()).ConfigPath, next); err != nil {
				return "", err
			}
			return strings.TrimSpace(next.WorkerAgent.Model), nil
		default:
			return "", fmt.Errorf("agent.model 不支持 scope=%s", scope)
		}
	default:
		return "", fmt.Errorf("未知配置键: %s", key)
	}
}

func mustBuildConfigEvalContext(out cliOutputFormat, homeFlag, projectFlag string, requireProject bool) *configEvalContext {
	home := mustOpenHomeForConfig(out, homeFlag)
	homePresence, err := loadHomeConfigPresence(home.ConfigPath)
	if err != nil {
		exitRuntimeError(out,
			"读取 Home 配置来源层失败",
			err.Error(),
			"检查 Home 配置文件格式后重试",
		)
	}
	eval := &configEvalContext{
		homeCfg:      home.Config.WithDefaults(),
		homePresence: homePresence,
	}
	if !requireProject {
		return eval
	}

	project := mustOpenProjectForConfig(out, home, projectFlag)
	localCfg, err := loadProjectConfigFromProject(project)
	if err != nil {
		exitRuntimeError(out,
			"读取项目配置失败",
			err.Error(),
			"检查项目 .dalek/config.json 后重试",
		)
	}
	localPresence, err := loadLocalConfigPresence(repo.NewLayout(project.RepoRoot()).ConfigPath)
	if err != nil {
		exitRuntimeError(out,
			"读取项目配置来源层失败",
			err.Error(),
			"检查项目配置文件格式后重试",
		)
	}
	eval.project = project
	eval.localCfg = localCfg
	eval.localPresence = localPresence
	return eval
}

func mustOpenHomeForConfig(out cliOutputFormat, homeFlag string) *app.Home {
	homeDir, err := app.ResolveHomeDir(homeFlag)
	if err != nil {
		exitRuntimeError(out,
			"无法解析 dalek Home 目录",
			err.Error(),
			"通过 --home 指定有效目录，或设置 DALEK_HOME",
		)
	}
	home, err := app.OpenHome(homeDir)
	if err != nil {
		exitRuntimeError(out,
			"打开 Home 失败",
			err.Error(),
			"检查 Home 目录权限与文件完整性后重试",
		)
	}
	return home
}

func mustOpenProjectForConfig(out cliOutputFormat, home *app.Home, projectFlag string) *app.Project {
	if home == nil {
		exitRuntimeError(out,
			"打开项目失败",
			"Home 为空",
			"检查 Home 初始化后重试",
		)
	}
	proj := strings.TrimSpace(projectFlag)
	if proj != "" {
		p, err := home.OpenProjectByName(proj)
		if err != nil {
			exitRuntimeError(out,
				fmt.Sprintf("打开项目 %q 失败", proj),
				err.Error(),
				"运行 dalek project ls 确认项目名，或修正 --project",
			)
		}
		return p
	}

	wd, _ := os.Getwd()
	p, err := home.OpenProjectFromDir(wd)
	if err != nil {
		exitRuntimeError(out,
			"无法识别当前目录的项目",
			err.Error(),
			"使用 --project 指定项目名，或切换到已注册项目目录，或先运行 dalek init",
		)
	}
	return p
}

func loadProjectConfigFromProject(p *app.Project) (repo.Config, error) {
	if p == nil {
		return repo.Config{}, fmt.Errorf("project 为空")
	}
	cfgPath := repo.NewLayout(p.RepoRoot()).ConfigPath
	cfg, _, err := repo.LoadConfig(cfgPath)
	if err != nil {
		return repo.Config{}, err
	}
	return cfg.WithDefaults(), nil
}

func loadHomeConfigPresence(path string) (configHomePresence, error) {
	root, err := loadJSONRoot(path)
	if err != nil {
		return configHomePresence{}, err
	}
	return configHomePresence{
		DaemonInternalListen: jsonPathExists(root, "daemon", "internal", "listen"),
		DaemonPublicListen:   jsonPathExists(root, "daemon", "public", "listen"),
		DaemonMaxConcurrent:  jsonPathExists(root, "daemon", "max_concurrent"),
		AgentProvider:        jsonPathExists(root, "agent", "provider"),
		AgentModel:           jsonPathExists(root, "agent", "model"),
	}, nil
}

func loadLocalConfigPresence(path string) (configLocalPresence, error) {
	root, err := loadJSONRoot(path)
	if err != nil {
		return configLocalPresence{}, err
	}
	return configLocalPresence{
		AgentProvider: jsonPathExists(root, "worker_agent", "provider") || jsonPathExists(root, "pm_agent", "provider"),
		AgentModel:    jsonPathExists(root, "worker_agent", "model") || jsonPathExists(root, "pm_agent", "model"),
	}, nil
}

func loadJSONRoot(path string) (map[string]any, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return map[string]any{}, nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]any{}, nil
		}
		return nil, err
	}
	var root map[string]any
	if len(strings.TrimSpace(string(b))) == 0 {
		return map[string]any{}, nil
	}
	if err := json.Unmarshal(b, &root); err != nil {
		return nil, err
	}
	if root == nil {
		return map[string]any{}, nil
	}
	return root, nil
}

func jsonPathExists(root map[string]any, path ...string) bool {
	if len(path) == 0 {
		return false
	}
	var cur any = root
	for _, seg := range path {
		obj, ok := cur.(map[string]any)
		if !ok {
			return false
		}
		next, ok := obj[strings.TrimSpace(seg)]
		if !ok {
			return false
		}
		cur = next
	}
	return true
}

func buildEffectiveProjectConfig(homeCfg app.HomeConfig, localCfg repo.Config) repo.Config {
	merged := repo.Config{}.WithDefaults()
	pc := app.ProjectConfig(merged)
	app.ApplyAgentProviderModelOverride(&pc, homeCfg.Agent.Provider, homeCfg.Agent.Model)
	merged = repo.Config(pc)
	merged = repo.MergeConfig(merged, localCfg)
	return merged.WithDefaults()
}

func normalizeConfigKey(raw string) string {
	return strings.ToLower(strings.TrimSpace(raw))
}

func findConfigKeyMeta(key string) (configKeyMeta, bool) {
	key = normalizeConfigKey(key)
	for _, meta := range configKeyOrder {
		if meta.Key == key {
			return meta, true
		}
	}
	return configKeyMeta{}, false
}

func resolveConfigSetScopeOrExit(out cliOutputFormat, meta configKeyMeta, forceGlobal, forceLocal bool) configScope {
	if forceGlobal && forceLocal {
		exitUsageError(out,
			"--global 和 --local 不能同时指定",
			"scope 标志互斥",
			"删除其一后重试",
		)
	}
	scope := meta.DefaultScope
	if forceGlobal {
		scope = configScopeGlobal
	}
	if forceLocal {
		scope = configScopeLocal
	}
	if containsConfigScope(meta.AllowedScopes, scope) {
		return scope
	}
	exitUsageError(out,
		fmt.Sprintf("配置键 %s 不支持 scope=%s", meta.Key, scope),
		"指定的写入层与配置键不匹配",
		fmt.Sprintf("可用层: %s", joinConfigScopes(meta.AllowedScopes)),
	)
	return configScopeDefault
}

func containsConfigScope(scopes []configScope, target configScope) bool {
	for _, s := range scopes {
		if s == target {
			return true
		}
	}
	return false
}

func joinConfigScopes(scopes []configScope) string {
	if len(scopes) == 0 {
		return "-"
	}
	out := make([]string, 0, len(scopes))
	for _, s := range scopes {
		out = append(out, string(s))
	}
	return strings.Join(out, ", ")
}

func splitConfigArgs(args []string, valueFlags map[string]bool, boolFlags map[string]bool) ([]string, []string, error) {
	flagArgs := make([]string, 0, len(args))
	positional := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		token := strings.TrimSpace(args[i])
		if token == "" {
			continue
		}
		if strings.HasPrefix(token, "-") {
			if valueFlags[token] {
				if i+1 >= len(args) {
					return nil, nil, fmt.Errorf("flag %s 缺少值", token)
				}
				flagArgs = append(flagArgs, token, args[i+1])
				i++
				continue
			}
			if boolFlags[token] {
				flagArgs = append(flagArgs, token)
				continue
			}
		}
		positional = append(positional, args[i])
	}
	return flagArgs, positional, nil
}
