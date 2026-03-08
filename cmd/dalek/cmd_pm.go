package main

import (
	"context"
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
