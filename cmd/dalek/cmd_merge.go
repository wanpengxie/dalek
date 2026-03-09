package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"dalek/internal/app"
)

func cmdMerge(args []string) {
	if len(args) == 0 {
		printMergeUsage()
		os.Exit(2)
	}
	sub := strings.TrimSpace(args[0])
	switch sub {
	case "ls":
		cmdMergeList(args[1:])
	case "propose", "approve", "discard", "merged":
		if isHelpArgs(args[1:]) {
			printMergeDeprecatedSubUsage(sub)
			os.Exit(0)
		}
		exitMergeDeprecated(sub)
	case "help", "-h", "--help":
		printMergeUsage()
		os.Exit(0)
	default:
		exitUsageError(globalOutput,
			fmt.Sprintf("未知 merge 子命令: %s", sub),
			"merge 命令组仅支持固定子命令",
			"运行 dalek merge --help 查看可用命令",
		)
	}
}

func printMergeUsage() {
	printGroupUsage("合并队列（已废弃）", "dalek merge <command> [flags]", []string{
		"ls         列出历史 merge 记录（只读）",
	})
	fmt.Fprintln(os.Stderr, "提示: merge 写操作已迁移到 ticket integration 语义，请使用 dalek ticket integration。")
	fmt.Fprintln(os.Stderr, "Use \"dalek merge <command> --help\" for more information.")
}

func cmdMergeList(args []string) {
	fs := flag.NewFlagSet("merge ls", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		printSubcommandUsage(
			fs,
			"列出 merge 历史记录（只读）",
			"dalek merge ls [--status STATUS] [-n 200] [--output text|json]",
			"dalek merge ls",
			"dalek merge ls --status proposed -o json",
		)
	}
	home := fs.String("home", globalHome, "dalek Home 目录（默认 ~/.dalek）")
	proj := fs.String("project", globalProject, "项目名（可选）")
	projShort := fs.String("p", globalProject, "项目名（可选）")
	status := fs.String("status", "", "过滤状态（可选）：proposed|ready|approved|merged|discarded|blocked|checks_running")
	limit := fs.Int("n", 200, "最多显示条数")
	output := addOutputFlag(fs, "输出格式: text|json（默认 text）")
	parseFlagSetOrExit(fs, args, globalOutput, "merge ls 参数解析失败", "运行 dalek merge ls --help 查看参数")
	if strings.TrimSpace(*projShort) != "" {
		*proj = strings.TrimSpace(*projShort)
	}
	out := parseOutputOrExit(*output, true)
	if *limit <= 0 {
		exitUsageError(out,
			"非法参数 --n",
			"--n 必须大于 0",
			"例如: dalek merge ls -n 200",
		)
	}

	st, err := app.ParseMergeStatus(*status)
	if err != nil {
		exitUsageError(out,
			"非法参数 --status",
			err.Error(),
			"改为 proposed|ready|approved|merged|discarded|blocked|checks_running 之一",
		)
	}

	p := mustOpenProjectWithOutput(out, *home, *proj)
	items, err := p.ListMergeItems(context.Background(), app.ListMergeOptions{Status: st, Limit: *limit})
	if err != nil {
		exitRuntimeError(out,
			"查询 merge 列表失败",
			err.Error(),
			"稍后重试，或检查数据库状态",
		)
	}

	if out == outputJSON {
		printJSONOrExit(map[string]any{
			"schema": "dalek.merge.list.v1",
			"items":  items,
		})
		return
	}
	if len(items) == 0 {
		fmt.Println("(empty)")
		return
	}
	for _, it := range items {
		ab := strings.TrimSpace(it.ApprovedBy)
		if ab == "" {
			ab = "-"
		}
		fmt.Printf("merge#%d  %s  t%d  branch=%s  approved_by=%s\n", it.ID, it.Status, it.TicketID, strings.TrimSpace(it.Branch), ab)
	}
}

func exitMergeDeprecated(sub string) {
	sub = strings.TrimSpace(sub)
	exitUsageError(globalOutput,
		fmt.Sprintf("merge %s 已废弃", sub),
		"merge 写操作不再作为 PM 可见流程",
		"改用 dalek ticket integration status|abandon",
	)
}

func isHelpArgs(args []string) bool {
	for _, a := range args {
		switch strings.TrimSpace(a) {
		case "help", "-h", "--help":
			return true
		}
	}
	return false
}

func printMergeDeprecatedSubUsage(sub string) {
	out := os.Stderr
	fmt.Fprintf(out, "merge %s（已废弃）\n", strings.TrimSpace(sub))
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Usage:")
	fmt.Fprintln(out, "  dalek ticket integration status --ticket <id>")
	fmt.Fprintln(out, "  dalek ticket integration abandon --ticket <id> --reason \"...\"")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "该子命令不再提供写操作。")
}

func cmdMergePropose(args []string) {
	fs := flag.NewFlagSet("merge propose", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		printSubcommandUsage(
			fs,
			"提议 merge",
			"dalek merge propose --ticket <id>",
			"dalek merge propose --ticket 1",
			"dalek merge propose --ticket 1 --project demo",
		)
	}
	home := fs.String("home", globalHome, "dalek Home 目录（默认 ~/.dalek）")
	proj := fs.String("project", globalProject, "项目名（可选）")
	projShort := fs.String("p", globalProject, "项目名（可选）")
	ticket := fs.Uint("ticket", 0, "ticket ID（必填）")
	parseFlagSetOrExit(fs, args, globalOutput, "merge propose 参数解析失败", "运行 dalek merge propose --help 查看参数")
	if strings.TrimSpace(*projShort) != "" {
		*proj = strings.TrimSpace(*projShort)
	}
	if *ticket == 0 {
		exitUsageError(globalOutput,
			"缺少必填参数 --ticket",
			"merge propose 需要 ticket ID",
			"dalek merge propose --ticket 1",
		)
	}
	p := mustOpenProjectWithOutput(globalOutput, *home, *proj)
	mi, err := p.ProposeMerge(context.Background(), uint(*ticket))
	if err != nil {
		exitRuntimeError(globalOutput,
			"merge propose 失败",
			err.Error(),
			"确认 ticket 存在并重试",
		)
	}
	fmt.Printf("proposed merge#%d  t%d  branch=%s\n", mi.ID, mi.TicketID, strings.TrimSpace(mi.Branch))
}

func cmdMergeApprove(args []string) {
	fs := flag.NewFlagSet("merge approve", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		printSubcommandUsage(
			fs,
			"审批 merge 项",
			"dalek merge approve --id <id> [--by cto]",
			"dalek merge approve --id 1 --by cto",
			"dalek merge approve --id 1 --project demo",
		)
	}
	home := fs.String("home", globalHome, "dalek Home 目录（默认 ~/.dalek）")
	proj := fs.String("project", globalProject, "项目名（可选）")
	projShort := fs.String("p", globalProject, "项目名（可选）")
	id := fs.Uint("id", 0, "merge item ID（必填）")
	by := fs.String("by", "cto", "审批人标识（可选）")
	parseFlagSetOrExit(fs, args, globalOutput, "merge approve 参数解析失败", "运行 dalek merge approve --help 查看参数")
	if strings.TrimSpace(*projShort) != "" {
		*proj = strings.TrimSpace(*projShort)
	}
	if *id == 0 {
		exitUsageError(globalOutput,
			"缺少必填参数 --id",
			"merge approve 需要 merge item ID",
			"dalek merge approve --id 1 --by cto",
		)
	}
	p := mustOpenProjectWithOutput(globalOutput, *home, *proj)
	if err := p.ApproveMerge(context.Background(), uint(*id), *by); err != nil {
		exitRuntimeError(globalOutput,
			fmt.Sprintf("merge#%d approve 失败", *id),
			err.Error(),
			"先运行 dalek merge ls 确认 id 和状态",
		)
	}
	fmt.Printf("approved merge#%d by %s\n", *id, strings.TrimSpace(*by))
}

func cmdMergeDiscard(args []string) {
	fs := flag.NewFlagSet("merge discard", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		printSubcommandUsage(
			fs,
			"丢弃 merge 项",
			"dalek merge discard --id <id> [--reason 文本]",
			"dalek merge discard --id 1 --reason 需求变更",
			"dalek merge discard --id 1 --project demo",
		)
	}
	home := fs.String("home", globalHome, "dalek Home 目录（默认 ~/.dalek）")
	proj := fs.String("project", globalProject, "项目名（可选）")
	projShort := fs.String("p", globalProject, "项目名（可选）")
	id := fs.Uint("id", 0, "merge item ID（必填）")
	reason := fs.String("reason", "", "丢弃原因（可选，仅用于输出）")
	parseFlagSetOrExit(fs, args, globalOutput, "merge discard 参数解析失败", "运行 dalek merge discard --help 查看参数")
	if strings.TrimSpace(*projShort) != "" {
		*proj = strings.TrimSpace(*projShort)
	}
	if *id == 0 {
		exitUsageError(globalOutput,
			"缺少必填参数 --id",
			"merge discard 需要 merge item ID",
			"dalek merge discard --id 1 --reason 需求变更",
		)
	}
	p := mustOpenProjectWithOutput(globalOutput, *home, *proj)
	if err := p.DiscardMerge(context.Background(), uint(*id), *reason); err != nil {
		exitRuntimeError(globalOutput,
			fmt.Sprintf("discard merge#%d 失败", *id),
			err.Error(),
			"先运行 dalek merge ls 确认 id 和状态",
		)
	}
	if strings.TrimSpace(*reason) == "" {
		fmt.Printf("discarded merge#%d\n", *id)
		return
	}
	fmt.Printf("discarded merge#%d  reason=%s\n", *id, strings.TrimSpace(*reason))
}

func cmdMergeMarked(args []string) {
	fs := flag.NewFlagSet("merge merged", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		printSubcommandUsage(
			fs,
			"标记 merge 已合并",
			"dalek merge merged --id <id>",
			"dalek merge merged --id 1",
			"dalek merge merged --id 1 --project demo",
		)
	}
	home := fs.String("home", globalHome, "dalek Home 目录（默认 ~/.dalek）")
	proj := fs.String("project", globalProject, "项目名（可选）")
	projShort := fs.String("p", globalProject, "项目名（可选）")
	id := fs.Uint("id", 0, "merge item ID（必填）")
	parseFlagSetOrExit(fs, args, globalOutput, "merge merged 参数解析失败", "运行 dalek merge merged --help 查看参数")
	if strings.TrimSpace(*projShort) != "" {
		*proj = strings.TrimSpace(*projShort)
	}
	if *id == 0 {
		exitUsageError(globalOutput,
			"缺少必填参数 --id",
			"merge merged 需要 merge item ID",
			"dalek merge merged --id 1",
		)
	}
	p := mustOpenProjectWithOutput(globalOutput, *home, *proj)
	if err := p.MarkMergeMerged(context.Background(), uint(*id)); err != nil {
		exitRuntimeError(globalOutput,
			fmt.Sprintf("标记 merge#%d merged 失败", *id),
			err.Error(),
			"先运行 dalek merge ls 确认 id 和状态",
		)
	}
	fmt.Printf("merged merge#%d\n", *id)
}
