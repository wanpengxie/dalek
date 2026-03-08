package main

import (
	"context"
	"dalek/internal/app"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
)

func cmdPM(args []string) {
	if len(args) == 0 {
		printPMUsage()
		os.Exit(2)
	}
	sub := strings.TrimSpace(args[0])
	switch sub {
	case "dashboard":
		cmdPMDashboard(args[1:])
	case "state":
		cmdPMState(args[1:])
	case "help", "-h", "--help":
		printPMUsage()
		os.Exit(0)
	default:
		exitUsageError(globalOutput,
			fmt.Sprintf("未知 pm 子命令: %s", sub),
			"pm 命令组仅支持固定子命令",
			"运行 dalek pm --help 查看可用命令",
		)
	}
}

func printPMUsage() {
	printGroupUsage("项目管理命令", "dalek pm <command> [flags]", []string{
		"dashboard   查看项目全局仪表盘",
		"state       查看或同步 PM 结构化状态",
	})
	fmt.Fprintln(os.Stderr, "Use \"dalek pm <command> --help\" for more information.")
}

func cmdPMDashboard(args []string) {
	fs := flag.NewFlagSet("pm dashboard", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		printSubcommandUsage(
			fs,
			"查看项目全局仪表盘",
			"dalek pm dashboard [--output text|json]",
			"dalek pm dashboard",
			"dalek pm dashboard -o json",
		)
	}
	home := fs.String("home", globalHome, "dalek Home 目录（默认 ~/.dalek）")
	proj := fs.String("project", globalProject, "项目名（可选）")
	projShort := fs.String("p", globalProject, "项目名（可选）")
	output := addOutputFlag(fs, "输出格式: text|json（默认 text）")
	parseFlagSetOrExit(fs, args, globalOutput, "pm dashboard 参数解析失败", "运行 dalek pm dashboard --help 查看参数")
	if strings.TrimSpace(*projShort) != "" {
		*proj = strings.TrimSpace(*projShort)
	}
	out := parseOutputOrExit(*output, true)

	p := mustOpenProjectWithOutput(out, *home, *proj)
	result, err := p.Dashboard(context.Background())
	if err != nil {
		exitRuntimeError(out,
			"读取 pm dashboard 失败",
			err.Error(),
			"稍后重试，或检查数据库状态",
		)
	}

	if out == outputJSON {
		if err := renderDashboardJSON(os.Stdout, result); err != nil {
			exitRuntimeError(out,
				"渲染 pm dashboard JSON 失败",
				err.Error(),
				"稍后重试，或检查输出目标是否可写",
			)
		}
		return
	}

	if err := renderDashboardText(os.Stdout, result); err != nil {
		exitRuntimeError(out,
			"渲染 pm dashboard 文本输出失败",
			err.Error(),
			"稍后重试，或检查输出目标是否可写",
		)
	}
}

func cmdPMState(args []string) {
	if len(args) == 0 {
		printPMStateUsage()
		os.Exit(2)
	}
	sub := strings.TrimSpace(args[0])
	switch sub {
	case "show":
		cmdPMStateShow(args[1:])
	case "sync":
		cmdPMStateSync(args[1:])
	case "help", "-h", "--help":
		printPMStateUsage()
		os.Exit(0)
	default:
		exitUsageError(globalOutput,
			fmt.Sprintf("未知 pm state 子命令: %s", sub),
			"pm state 仅支持固定子命令",
			"运行 dalek pm state --help 查看可用命令",
		)
	}
}

func printPMStateUsage() {
	printGroupUsage("PM 状态命令", "dalek pm state <command> [flags]", []string{
		"show        读取 PM 结构化状态文件",
		"sync        从当前项目运行态同步并写入 PM 结构化状态文件",
	})
	fmt.Fprintln(os.Stderr, "Use \"dalek pm state <command> --help\" for more information.")
}

func cmdPMStateShow(args []string) {
	fs := flag.NewFlagSet("pm state show", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		printSubcommandUsage(
			fs,
			"读取 PM 结构化状态文件",
			"dalek pm state show [--sync] [--output text|json]",
			"dalek pm state show",
			"dalek pm state show --sync -o json",
		)
	}
	home := fs.String("home", globalHome, "dalek Home 目录（默认 ~/.dalek）")
	proj := fs.String("project", globalProject, "项目名（可选）")
	projShort := fs.String("p", globalProject, "项目名（可选）")
	syncFirst := fs.Bool("sync", false, "读取前先同步一次 PM 结构化状态")
	output := addOutputFlag(fs, "输出格式: text|json（默认 text）")
	parseFlagSetOrExit(fs, args, globalOutput, "pm state show 参数解析失败", "运行 dalek pm state show --help 查看参数")
	if strings.TrimSpace(*projShort) != "" {
		*proj = strings.TrimSpace(*projShort)
	}
	out := parseOutputOrExit(*output, true)

	p := mustOpenProjectWithOutput(out, *home, *proj)
	state, err := loadOrSyncPMWorkspaceState(p, *syncFirst)
	if err != nil {
		exitRuntimeError(out,
			"读取 pm state 失败",
			err.Error(),
			"运行 dalek pm state sync 生成状态文件后重试",
		)
	}
	printPMWorkspaceStateOrExit(out, state)
}

func cmdPMStateSync(args []string) {
	fs := flag.NewFlagSet("pm state sync", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		printSubcommandUsage(
			fs,
			"从当前项目运行态同步并写入 PM 结构化状态文件",
			"dalek pm state sync [--output text|json]",
			"dalek pm state sync",
			"dalek pm state sync -o json",
		)
	}
	home := fs.String("home", globalHome, "dalek Home 目录（默认 ~/.dalek）")
	proj := fs.String("project", globalProject, "项目名（可选）")
	projShort := fs.String("p", globalProject, "项目名（可选）")
	output := addOutputFlag(fs, "输出格式: text|json（默认 text）")
	parseFlagSetOrExit(fs, args, globalOutput, "pm state sync 参数解析失败", "运行 dalek pm state sync --help 查看参数")
	if strings.TrimSpace(*projShort) != "" {
		*proj = strings.TrimSpace(*projShort)
	}
	out := parseOutputOrExit(*output, true)

	p := mustOpenProjectWithOutput(out, *home, *proj)
	state, err := p.SyncPMWorkspaceState(context.Background())
	if err != nil {
		exitRuntimeError(out,
			"同步 pm state 失败",
			err.Error(),
			"检查项目数据库与 .dalek/pm 目录状态后重试",
		)
	}
	printPMWorkspaceStateOrExit(out, state)
}

func loadOrSyncPMWorkspaceState(p *app.Project, syncFirst bool) (app.PMWorkspaceState, error) {
	if p == nil {
		return app.PMWorkspaceState{}, fmt.Errorf("project 为空")
	}
	if syncFirst {
		return p.SyncPMWorkspaceState(context.Background())
	}
	state, err := p.LoadPMWorkspaceState()
	if err == nil {
		return state, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return p.SyncPMWorkspaceState(context.Background())
	}
	return app.PMWorkspaceState{}, err
}

func printPMWorkspaceStateOrExit(out cliOutputFormat, state app.PMWorkspaceState) {
	if out == outputJSON {
		printJSONOrExit(state)
		return
	}
	if err := renderPMWorkspaceStateText(os.Stdout, state); err != nil {
		exitRuntimeError(out,
			"渲染 pm state 文本输出失败",
			err.Error(),
			"稍后重试，或检查输出目标是否可写",
		)
	}
}
