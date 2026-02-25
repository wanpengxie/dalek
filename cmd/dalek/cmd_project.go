package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"dalek/internal/app"
	"dalek/internal/ui/tui"
)

func cmdInit(args []string) {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		printSubcommandUsage(
			fs,
			"初始化当前 git 仓库为 dalek 项目",
			"dalek init [--name NAME] [--socket dalek] [--prefix ts/<project>/]",
			"dalek init",
			"dalek init --name demo --socket dalek",
		)
	}
	home := fs.String("home", globalHome, "dalek Home 目录（默认 ~/.dalek；用于 registry/worktrees）")
	name := fs.String("name", "", "project 名（可选）")
	socket := fs.String("socket", "", "tmux socket 名（tmux -L；可选，默认 dalek）")
	prefix := fs.String("prefix", "", "分支前缀（可选；默认 ts/<projectKey>/）")
	parseFlagSetOrExit(fs, args, globalOutput, "init 参数解析失败", "运行 dalek init --help 查看参数")

	wd, err := os.Getwd()
	if err != nil {
		exitRuntimeError(globalOutput, "无法获取当前目录", err.Error(), "检查工作目录权限后重试")
	}

	cfg := app.ProjectConfig{TmuxSocket: *socket, BranchPrefix: *prefix, RefreshIntervalMS: 1000}
	applyGlobalAgentConfig(&cfg)

	homeDir, err := app.ResolveHomeDir(*home)
	if err != nil {
		exitRuntimeError(globalOutput, "解析 --home 失败", err.Error(), "指定有效 Home 目录或设置 DALEK_HOME")
	}
	h, err := app.OpenHome(homeDir)
	if err != nil {
		exitRuntimeError(globalOutput, "打开 Home 失败", err.Error(), "检查 Home 目录权限与文件完整性")
	}

	p, err := h.InitProjectFromDir(wd, *name, cfg)
	if err != nil {
		exitRuntimeError(globalOutput, "初始化项目失败", err.Error(), "确认当前目录是 git 仓库并重试")
	}

	fmt.Fprintf(os.Stderr, "初始化完成：project=%s  repo=%s\n", p.Name(), p.RepoRoot())
	fmt.Fprintf(os.Stderr, "Home：%s\n", homeDir)
	fmt.Fprintln(os.Stderr, "下一步：直接运行 `dalek` 打开 TUI。")
}

func runTUI(args []string) {
	fs := flag.NewFlagSet("tui", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		printSubcommandUsage(
			fs,
			"启动交互式 TUI",
			"dalek tui [--project NAME]",
			"dalek tui",
			"dalek tui --project demo",
		)
	}
	home := fs.String("home", globalHome, "dalek Home 目录（默认 ~/.dalek；用于 registry/worktrees）")
	proj := fs.String("project", globalProject, "进入指定 project（可选）")
	parseFlagSetOrExit(fs, args, globalOutput, "tui 参数解析失败", "运行 dalek tui --help 查看参数")

	homeDir, err := app.ResolveHomeDir(*home)
	if err != nil {
		exitRuntimeError(globalOutput, "解析 --home 失败", err.Error(), "指定有效 Home 目录或设置 DALEK_HOME")
	}
	h, err := app.OpenHome(homeDir)
	if err != nil {
		exitRuntimeError(globalOutput, "打开 Home 失败", err.Error(), "检查 Home 目录权限与文件完整性")
	}
	if err := tui.Run(h, strings.TrimSpace(*proj)); err != nil {
		exitRuntimeError(globalOutput, "TUI 退出（错误）", err.Error(), "检查终端环境后重试")
	}
}

func cmdProject(args []string) {
	if len(args) == 0 {
		printProjectUsage()
		os.Exit(2)
	}
	sub := strings.TrimSpace(args[0])
	switch sub {
	case "ls":
		cmdProjectList(args[1:])
	case "add":
		cmdProjectAdd(args[1:])
	case "rm":
		cmdProjectRemove(args[1:])
	case "help", "-h", "--help":
		printProjectUsage()
		os.Exit(0)
	default:
		exitUsageError(globalOutput,
			fmt.Sprintf("未知 project 子命令: %s", sub),
			"project 命令组仅支持 ls|add|rm",
			"运行 dalek project --help 查看可用命令",
		)
	}
}

func printProjectUsage() {
	printGroupUsage("项目注册管理", "dalek project <command> [flags]", []string{
		"ls       列出已注册项目",
		"add      添加项目",
		"rm       删除项目",
	})
	fmt.Fprintln(os.Stderr, "Use \"dalek project <command> --help\" for more information.")
}

func cmdProjectList(args []string) {
	fs := flag.NewFlagSet("project ls", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		printSubcommandUsage(
			fs,
			"列出已注册项目",
			"dalek project ls [--output text|json]",
			"dalek project ls",
			"dalek project ls -o json",
		)
	}
	home := fs.String("home", globalHome, "dalek Home 目录（默认 ~/.dalek；用于 registry/worktrees）")
	output := addOutputFlag(fs, "输出格式: text|json（默认 text）")
	parseFlagSetOrExit(fs, args, globalOutput, "project ls 参数解析失败", "运行 dalek project ls --help 查看参数")
	out := parseOutputOrExit(*output, true)

	homeDir, err := app.ResolveHomeDir(*home)
	if err != nil {
		exitRuntimeError(out, "解析 --home 失败", err.Error(), "指定有效 Home 目录或设置 DALEK_HOME")
	}
	h, err := app.OpenHome(homeDir)
	if err != nil {
		exitRuntimeError(out, "打开 Home 失败", err.Error(), "检查 Home 目录权限与文件完整性")
	}

	ps, err := h.ListProjects()
	if err != nil {
		exitRuntimeError(out, "读取 registry 失败", err.Error(), "检查 Home 下 registry 文件后重试")
	}

	if out == outputJSON {
		printJSONOrExit(map[string]any{
			"schema":   "dalek.project.list.v1",
			"projects": ps,
		})
		return
	}

	if len(ps) == 0 {
		fmt.Println("(empty)")
		return
	}
	for _, p := range ps {
		fmt.Printf("%s\t%s\n", p.Name, p.RepoRoot)
	}
}

func cmdProjectAdd(args []string) {
	fs := flag.NewFlagSet("project add", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		printSubcommandUsage(
			fs,
			"添加项目",
			"dalek project add --path <repo> [--name NAME]",
			"dalek project add --path /path/to/repo",
			"dalek project add --path . --name demo",
		)
	}
	home := fs.String("home", globalHome, "dalek Home 目录（默认 ~/.dalek；用于 registry/worktrees）")
	name := fs.String("name", "", "project 名（可选）")
	path := fs.String("path", "", "git repo 路径（必填）")
	socket := fs.String("socket", "", "tmux socket 名（tmux -L；可选，默认 dalek）")
	prefix := fs.String("prefix", "", "分支前缀（可选；默认 ts/<projectKey>/）")
	parseFlagSetOrExit(fs, args, globalOutput, "project add 参数解析失败", "运行 dalek project add --help 查看参数")
	if strings.TrimSpace(*path) == "" {
		exitUsageError(globalOutput, "缺少必填参数 --path", "project add 需要 git 仓库路径", "dalek project add --path /path/to/repo")
	}

	homeDir, err := app.ResolveHomeDir(*home)
	if err != nil {
		exitRuntimeError(globalOutput, "解析 --home 失败", err.Error(), "指定有效 Home 目录或设置 DALEK_HOME")
	}
	h, err := app.OpenHome(homeDir)
	if err != nil {
		exitRuntimeError(globalOutput, "打开 Home 失败", err.Error(), "检查 Home 目录权限与文件完整性")
	}

	cfg := app.ProjectConfig{TmuxSocket: *socket, BranchPrefix: *prefix, RefreshIntervalMS: 1000}
	applyGlobalAgentConfig(&cfg)
	p, err := h.InitProjectFromDir(*path, *name, cfg)
	if err != nil {
		exitRuntimeError(globalOutput, "添加项目失败", err.Error(), "确认路径为有效 git 仓库并重试")
	}
	fmt.Printf("%s\n", p.Name())
}

func cmdProjectRemove(args []string) {
	fs := flag.NewFlagSet("project rm", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		printSubcommandUsage(
			fs,
			"删除项目",
			"dalek project rm --name <project>",
			"dalek project rm --name demo",
			"dalek project rm --name demo --home ~/.dalek",
		)
	}
	home := fs.String("home", globalHome, "dalek Home 目录（默认 ~/.dalek；用于 registry/worktrees）")
	name := fs.String("name", "", "project 名（必填）")
	parseFlagSetOrExit(fs, args, globalOutput, "project rm 参数解析失败", "运行 dalek project rm --help 查看参数")
	if strings.TrimSpace(*name) == "" {
		exitUsageError(globalOutput, "缺少必填参数 --name", "project rm 需要 project 名", "dalek project rm --name my-project")
	}

	homeDir, err := app.ResolveHomeDir(*home)
	if err != nil {
		exitRuntimeError(globalOutput, "解析 --home 失败", err.Error(), "指定有效 Home 目录或设置 DALEK_HOME")
	}
	h, err := app.OpenHome(homeDir)
	if err != nil {
		exitRuntimeError(globalOutput, "打开 Home 失败", err.Error(), "检查 Home 目录权限与文件完整性")
	}

	r, err := h.LoadRegistry()
	if err != nil {
		exitRuntimeError(globalOutput, "读取 registry 失败", err.Error(), "检查 Home 下 registry 文件后重试")
	}
	out := make([]app.RegisteredProject, 0, len(r.Projects))
	for _, p := range r.Projects {
		if p.Name == strings.TrimSpace(*name) {
			continue
		}
		out = append(out, p)
	}
	r.Projects = out
	if err := h.SaveRegistry(r); err != nil {
		exitRuntimeError(globalOutput, "写入 registry 失败", err.Error(), "检查 Home 目录可写权限")
	}
	fmt.Printf("%s removed\n", strings.TrimSpace(*name))
}

func applyGlobalAgentConfig(cfg *app.ProjectConfig) {
	if cfg == nil {
		return
	}
	app.ApplyAgentProviderModelOverride(cfg, globalAgentProvider, globalAgentModel)
}
