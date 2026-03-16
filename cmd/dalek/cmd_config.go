package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"dalek/internal/app"
	"dalek/internal/config"
)

type configScope = config.Scope

const (
	configScopeDefault configScope = config.ScopeDefault
	configScopeGlobal  configScope = config.ScopeGlobal
	configScopeLocal   configScope = config.ScopeLocal
	configScopeDB      configScope = config.ScopeDB
)

const (
	configKeyDaemonInternalListen     = config.ConfigKeyDaemonInternalListen
	configKeyDaemonPublicListen       = config.ConfigKeyDaemonPublicListen
	configKeyDaemonMaxConcurrent      = config.ConfigKeyDaemonMaxConcurrent
	configKeyProjectMaxRunningWorkers = config.ConfigKeyProjectMaxRunningWorkers
	configKeyAgentProvider            = config.ConfigKeyAgentProvider
	configKeyAgentModel               = config.ConfigKeyAgentModel
)

type configKeyMeta = config.KeyMeta

var configKeyOrder = config.KeyOrder

type configEvalContext = config.EvalContext

type configSetContext = config.SetContext

type configItem struct {
	Key    string      `json:"key"`
	Value  string      `json:"value"`
	Source configScope `json:"source"`
}

type appProjectConfigAdapter struct {
	project *app.Project
}

func (a appProjectConfigAdapter) ConfigPath() string {
	if a.project == nil {
		return ""
	}
	return a.project.ConfigPath()
}

func (a appProjectConfigAdapter) GetMaxRunningWorkers(ctx context.Context) (int, error) {
	if a.project == nil {
		return 0, fmt.Errorf("project 为空")
	}
	st, err := a.project.GetPMState(ctx)
	if err != nil {
		return 0, err
	}
	return st.MaxRunningWorkers, nil
}

func (a appProjectConfigAdapter) SetMaxRunningWorkers(ctx context.Context, n int) (int, error) {
	if a.project == nil {
		return 0, fmt.Errorf("project 为空")
	}
	st, err := a.project.SetMaxRunningWorkers(ctx, n)
	if err != nil {
		return 0, err
	}
	return st.MaxRunningWorkers, nil
}

func toConfigHomeCfg(cfg app.HomeConfig) config.HomeConfig {
	v := cfg.WithDefaults()
	return config.HomeConfig{
		Agent: config.HomeAgentConfig{
			Provider: strings.TrimSpace(v.Agent.Provider),
			Model:    strings.TrimSpace(v.Agent.Model),
		},
		Daemon: config.HomeDaemonConfig{
			MaxConcurrent: v.Daemon.MaxConcurrent,
			Internal: config.HomeDaemonInternalConfig{
				Listen:     strings.TrimSpace(v.Daemon.Internal.Listen),
				AllowCIDRs: append([]string(nil), v.Daemon.Internal.AllowCIDRs...),
			},
			Public: config.HomeDaemonPublicConfig{
				Listen: strings.TrimSpace(v.Daemon.Public.Listen),
			},
		},
	}
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
	homeCfg := homeHandle.Config.WithDefaults()
	setCtx := &configSetContext{
		HomeConfigPath: homeHandle.ConfigPath,
		HomeCfg:        toConfigHomeCfg(homeCfg),
		WriteHomeConfig: func(path string, cfg config.HomeConfig) error {
			next := homeCfg
			normalized := cfg.WithDefaults()
			next.Daemon.Internal.Listen = strings.TrimSpace(normalized.Daemon.Internal.Listen)
			next.Daemon.Internal.AllowCIDRs = append([]string(nil), normalized.Daemon.Internal.AllowCIDRs...)
			next.Daemon.Public.Listen = strings.TrimSpace(normalized.Daemon.Public.Listen)
			next.Daemon.MaxConcurrent = normalized.Daemon.MaxConcurrent
			next.Agent.Provider = strings.TrimSpace(normalized.Agent.Provider)
			next.Agent.Model = strings.TrimSpace(normalized.Agent.Model)
			if err := app.WriteHomeConfigAtomic(path, next); err != nil {
				return err
			}
			homeCfg = next.WithDefaults()
			homeHandle.Config = homeCfg
			return nil
		},
	}
	if scope == configScopeLocal || scope == configScopeDB {
		p := mustOpenProjectForConfig(out, homeHandle, *proj)
		adapter := appProjectConfigAdapter{project: p}
		setCtx.Project = adapter
		localCfg, err := config.LoadProjectConfigFromProject(p)
		if err != nil {
			exitRuntimeError(out,
				"读取项目配置失败",
				err.Error(),
				"检查项目 .dalek/config.json 后重试",
			)
		}
		setCtx.LocalCfg = localCfg
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
	return config.ResolveValue(key, eval)
}

func setConfigValue(ctx *configSetContext, key string, scope configScope, rawValue string) (string, error) {
	return config.SetValue(ctx, key, scope, rawValue)
}

func mustBuildConfigEvalContext(out cliOutputFormat, homeFlag, projectFlag string, requireProject bool) *configEvalContext {
	home := mustOpenHomeForConfig(out, homeFlag)
	homePresence, err := config.LoadHomeConfigPresence(home.ConfigPath)
	if err != nil {
		exitRuntimeError(out,
			"读取 Home 配置来源层失败",
			err.Error(),
			"检查 Home 配置文件格式后重试",
		)
	}
	eval := &configEvalContext{
		HomeCfg:      toConfigHomeCfg(home.Config.WithDefaults()),
		HomePresence: homePresence,
	}
	if !requireProject {
		return eval
	}

	project := mustOpenProjectForConfig(out, home, projectFlag)
	localCfg, err := config.LoadProjectConfigFromProject(project)
	if err != nil {
		exitRuntimeError(out,
			"读取项目配置失败",
			err.Error(),
			"检查项目 .dalek/config.json 后重试",
		)
	}
	localPresence, err := config.LoadLocalConfigPresenceFromProject(project)
	if err != nil {
		exitRuntimeError(out,
			"读取项目配置来源层失败",
			err.Error(),
			"检查项目配置文件格式后重试",
		)
	}
	eval.Project = appProjectConfigAdapter{project: project}
	eval.LocalCfg = localCfg
	eval.LocalPresence = localPresence
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

func normalizeConfigKey(raw string) string {
	return config.NormalizeKey(raw)
}

func findConfigKeyMeta(key string) (configKeyMeta, bool) {
	return config.FindKeyMeta(key)
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
	return config.ContainsScope(scopes, target)
}

func joinConfigScopes(scopes []configScope) string {
	return config.JoinScopes(scopes)
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
