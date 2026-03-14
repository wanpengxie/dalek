package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"dalek/internal/app"
	"dalek/internal/contracts"
)

func cmdInbox(args []string) {
	if len(args) == 0 {
		printInboxUsage()
		os.Exit(2)
	}
	sub := strings.TrimSpace(args[0])
	switch sub {
	case "ls":
		cmdInboxList(args[1:])
	case "show":
		cmdInboxShow(args[1:])
	case "close":
		cmdInboxClose(args[1:])
	case "continue":
		cmdInboxReply(args[1:], contracts.InboxReplyContinue)
	case "done":
		cmdInboxReply(args[1:], contracts.InboxReplyDone)
	case "snooze":
		cmdInboxSnooze(args[1:])
	case "unsnooze":
		cmdInboxUnsnooze(args[1:])
	case "help", "-h", "--help":
		printInboxUsage()
		os.Exit(0)
	default:
		exitUsageError(globalOutput,
			fmt.Sprintf("未知 inbox 子命令: %s", sub),
			"inbox 命令组仅支持固定子命令",
			"运行 dalek inbox --help 查看可用命令",
		)
	}
}

func printInboxUsage() {
	printGroupUsage("人工待处理项管理", "dalek inbox <command> [flags]", []string{
		"ls         列出 inbox 项",
		"show       查看 inbox 详情",
		"close      关闭 inbox 项",
		"continue   回复 needs_user inbox 并继续执行",
		"done       回复 needs_user inbox 并发起 closeout-only 收尾执行",
		"snooze     延后处理 inbox 项",
		"unsnooze   取消延后",
	})
	fmt.Fprintln(os.Stderr, "Use \"dalek inbox <command> --help\" for more information.")
}

func cmdInboxList(args []string) {
	fs := flag.NewFlagSet("inbox ls", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		printSubcommandUsage(
			fs,
			"列出 inbox 项",
			"dalek inbox ls [--status open|done|snoozed] [-n 200] [-o text|json]",
			"dalek inbox ls",
			"dalek inbox ls --status open -o json",
		)
	}
	home := fs.String("home", globalHome, "dalek Home 目录（默认 ~/.dalek）")
	proj := fs.String("project", globalProject, "项目名（可选）")
	projShort := fs.String("p", globalProject, "项目名（可选）")
	status := fs.String("status", "open", "状态：open|done|snoozed")
	limit := fs.Int("n", 200, "最多显示条数")
	verbose := fs.Bool("v", false, "显示 body")
	output := addOutputFlag(fs, "输出格式: text|json（默认 text）")
	parseFlagSetOrExit(fs, args, globalOutput, "inbox ls 参数解析失败", "运行 dalek inbox ls --help 查看参数")
	if strings.TrimSpace(*projShort) != "" {
		*proj = strings.TrimSpace(*projShort)
	}
	out := parseOutputOrExit(*output, true)
	if *limit <= 0 {
		exitUsageError(out,
			"非法参数 --n",
			"--n 必须大于 0",
			"例如: dalek inbox ls -n 200",
		)
	}

	st, err := app.ParseInboxStatus(*status)
	if err != nil {
		exitUsageError(out,
			"非法参数 --status",
			err.Error(),
			"改为 open|done|snoozed 之一，例如 --status open",
		)
	}

	p := mustOpenProjectWithOutput(out, *home, *proj)
	items, err := p.ListInbox(context.Background(), app.ListInboxOptions{Status: st, Limit: *limit})
	if err != nil {
		exitRuntimeError(out,
			"查询 inbox 失败",
			err.Error(),
			"稍后重试，或检查数据库状态",
		)
	}

	if out == outputJSON {
		printJSONOrExit(map[string]any{
			"schema": "dalek.inbox.list.v1",
			"items":  items,
		})
		return
	}

	if len(items) == 0 {
		fmt.Println("(empty)")
		return
	}
	for _, it := range items {
		fmt.Printf("inbox#%d  %s/%s/%s  t%d w%d m%d  %s\n", it.ID, it.Status, it.Severity, it.Reason, it.TicketID, it.WorkerID, it.MergeItemID, strings.TrimSpace(it.Title))
		if *verbose && strings.TrimSpace(it.Body) != "" {
			fmt.Println(strings.TrimSpace(it.Body))
			fmt.Println("---")
		}
	}
}

func cmdInboxShow(args []string) {
	fs := flag.NewFlagSet("inbox show", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		printSubcommandUsage(
			fs,
			"查看 inbox 详情",
			"dalek inbox show --id <id> [--output text|json]",
			"dalek inbox show --id 1",
			"dalek inbox show --id 1 -o json",
		)
	}
	home := fs.String("home", globalHome, "dalek Home 目录（默认 ~/.dalek）")
	proj := fs.String("project", globalProject, "项目名（可选）")
	projShort := fs.String("p", globalProject, "项目名（可选）")
	id := fs.Uint("id", 0, "inbox ID（必填）")
	output := addOutputFlag(fs, "输出格式: text|json（默认 text）")
	parseFlagSetOrExit(fs, args, globalOutput, "inbox show 参数解析失败", "运行 dalek inbox show --help 查看参数")
	if strings.TrimSpace(*projShort) != "" {
		*proj = strings.TrimSpace(*projShort)
	}
	out := parseOutputOrExit(*output, true)
	if *id == 0 {
		exitUsageError(out,
			"缺少必填参数 --id",
			"inbox show 需要 inbox ID",
			"dalek inbox show --id 1",
		)
	}

	p := mustOpenProjectWithOutput(out, *home, *proj)
	it, err := p.GetInboxItem(context.Background(), uint(*id))
	if err != nil {
		exitRuntimeError(out,
			fmt.Sprintf("读取 inbox #%d 失败", *id),
			err.Error(),
			"先运行 dalek inbox ls 确认 id 后重试",
		)
	}
	if out == outputJSON {
		printJSONOrExit(map[string]any{
			"schema": "dalek.inbox.show.v1",
			"item":   it,
		})
		return
	}
	fmt.Printf("inbox#%d  %s/%s/%s  key=%s  t%d w%d m%d\n", it.ID, it.Status, it.Severity, it.Reason, strings.TrimSpace(it.Key), it.TicketID, it.WorkerID, it.MergeItemID)
	fmt.Println(strings.TrimSpace(it.Title))
	fmt.Println()
	fmt.Println(strings.TrimSpace(it.Body))
}

func cmdInboxClose(args []string) {
	fs := flag.NewFlagSet("inbox close", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		printSubcommandUsage(
			fs,
			"关闭 inbox 项",
			"dalek inbox close --id <id>",
			"dalek inbox close --id 1",
			"dalek inbox close --id 1 --project demo",
		)
	}
	home := fs.String("home", globalHome, "dalek Home 目录（默认 ~/.dalek）")
	proj := fs.String("project", globalProject, "项目名（可选）")
	projShort := fs.String("p", globalProject, "项目名（可选）")
	id := fs.Uint("id", 0, "inbox ID（必填）")
	parseFlagSetOrExit(fs, args, globalOutput, "inbox close 参数解析失败", "运行 dalek inbox close --help 查看参数")
	if strings.TrimSpace(*projShort) != "" {
		*proj = strings.TrimSpace(*projShort)
	}
	if *id == 0 {
		exitUsageError(globalOutput,
			"缺少必填参数 --id",
			"inbox close 需要 inbox ID",
			"dalek inbox close --id 1",
		)
	}
	p := mustOpenProjectWithOutput(globalOutput, *home, *proj)
	if err := p.CloseInboxItem(context.Background(), uint(*id)); err != nil {
		exitRuntimeError(globalOutput,
			fmt.Sprintf("关闭 inbox #%d 失败", *id),
			err.Error(),
			"先运行 dalek inbox ls 确认 id 后重试",
		)
	}
	fmt.Printf("closed inbox#%d\n", *id)
}

func cmdInboxSnooze(args []string) {
	fs := flag.NewFlagSet("inbox snooze", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		printSubcommandUsage(
			fs,
			"延后 inbox 项",
			"dalek inbox snooze --id <id> [--until 30m]",
			"dalek inbox snooze --id 1 --until 30m",
			"dalek inbox snooze --id 1 --until 2h --project demo",
		)
	}
	home := fs.String("home", globalHome, "dalek Home 目录（默认 ~/.dalek）")
	proj := fs.String("project", globalProject, "项目名（可选）")
	projShort := fs.String("p", globalProject, "项目名（可选）")
	id := fs.Uint("id", 0, "inbox ID（必填）")
	until := fs.Duration("until", 30*time.Minute, "延后时长（例如 30m）")
	parseFlagSetOrExit(fs, args, globalOutput, "inbox snooze 参数解析失败", "运行 dalek inbox snooze --help 查看参数")
	if strings.TrimSpace(*projShort) != "" {
		*proj = strings.TrimSpace(*projShort)
	}
	if *id == 0 {
		exitUsageError(globalOutput,
			"缺少必填参数 --id",
			"inbox snooze 需要 inbox ID",
			"dalek inbox snooze --id 1 --until 30m",
		)
	}
	if *until <= 0 {
		exitUsageError(globalOutput,
			"非法参数 --until",
			"--until 必须大于 0",
			"例如: dalek inbox snooze --id 1 --until 30m",
		)
	}
	t := time.Now().Add(*until)
	p := mustOpenProjectWithOutput(globalOutput, *home, *proj)
	if err := p.SnoozeInboxItem(context.Background(), uint(*id), t); err != nil {
		exitRuntimeError(globalOutput,
			fmt.Sprintf("snooze inbox #%d 失败", *id),
			err.Error(),
			"先运行 dalek inbox ls 确认 id 后重试",
		)
	}
	fmt.Printf("snoozed inbox#%d until %s\n", *id, t.Local().Format("01-02 15:04:05"))
}

func cmdInboxUnsnooze(args []string) {
	fs := flag.NewFlagSet("inbox unsnooze", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		printSubcommandUsage(
			fs,
			"取消 inbox 延后",
			"dalek inbox unsnooze --id <id>",
			"dalek inbox unsnooze --id 1",
			"dalek inbox unsnooze --id 1 --project demo",
		)
	}
	home := fs.String("home", globalHome, "dalek Home 目录（默认 ~/.dalek）")
	proj := fs.String("project", globalProject, "项目名（可选）")
	projShort := fs.String("p", globalProject, "项目名（可选）")
	id := fs.Uint("id", 0, "inbox ID（必填）")
	parseFlagSetOrExit(fs, args, globalOutput, "inbox unsnooze 参数解析失败", "运行 dalek inbox unsnooze --help 查看参数")
	if strings.TrimSpace(*projShort) != "" {
		*proj = strings.TrimSpace(*projShort)
	}
	if *id == 0 {
		exitUsageError(globalOutput,
			"缺少必填参数 --id",
			"inbox unsnooze 需要 inbox ID",
			"dalek inbox unsnooze --id 1",
		)
	}
	p := mustOpenProjectWithOutput(globalOutput, *home, *proj)
	if err := p.UnsnoozeInboxItem(context.Background(), uint(*id)); err != nil {
		exitRuntimeError(globalOutput,
			fmt.Sprintf("unsnooze inbox #%d 失败", *id),
			err.Error(),
			"先运行 dalek inbox ls 确认 id 后重试",
		)
	}
	fmt.Printf("unsnoozed inbox#%d\n", *id)
}

func cmdInboxReply(args []string, action contracts.InboxReplyAction) {
	name := string(action)
	fs := flag.NewFlagSet("inbox "+name, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		printSubcommandUsage(
			fs,
			"回复 needs_user inbox 并恢复执行",
			fmt.Sprintf("dalek inbox %s --id <id> --reply \"...\" [--output text|json]", name),
			fmt.Sprintf("dalek inbox %s --id 1 --reply \"资料已放到 /tmp/spec.md\"", name),
			fmt.Sprintf("dalek inbox %s --id 1 --reply \"可以按当前方案收尾\" -o json", name),
		)
	}
	home := fs.String("home", globalHome, "dalek Home 目录（默认 ~/.dalek）")
	proj := fs.String("project", globalProject, "项目名（可选）")
	projShort := fs.String("p", globalProject, "项目名（可选）")
	id := fs.Uint("id", 0, "inbox ID（必填）")
	reply := fs.String("reply", "", "原样 markdown 回复（必填）")
	timeout := fs.Duration("timeout", 10*time.Second, "请求超时（例如 10s）")
	output := addOutputFlag(fs, "输出格式: text|json（默认 text）")
	parseFlagSetOrExit(fs, args, globalOutput, "inbox reply 参数解析失败", fmt.Sprintf("运行 dalek inbox %s --help 查看参数", name))
	if strings.TrimSpace(*projShort) != "" {
		*proj = strings.TrimSpace(*projShort)
	}
	out := parseOutputOrExit(*output, true)
	if *id == 0 {
		exitUsageError(out,
			"缺少必填参数 --id",
			fmt.Sprintf("inbox %s 需要 inbox ID", name),
			fmt.Sprintf("dalek inbox %s --id 1 --reply \"...\"", name),
		)
	}
	if strings.TrimSpace(*reply) == "" {
		exitUsageError(out,
			"缺少必填参数 --reply",
			"--reply 不能为空",
			fmt.Sprintf("dalek inbox %s --id 1 --reply \"资料已放到 /tmp/spec.md\"", name),
		)
	}
	if *timeout <= 0 {
		exitUsageError(out,
			"非法参数 --timeout",
			"--timeout 必须大于 0",
			fmt.Sprintf("dalek inbox %s --timeout 10s", name),
		)
	}

	p := mustOpenProjectWithOutput(out, *home, *proj)
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	result, err := p.ReplyInboxItem(ctx, uint(*id), string(action), *reply)
	if err != nil {
		exitRuntimeError(out,
			fmt.Sprintf("执行 inbox #%d 的 %s 回复失败", *id, name),
			err.Error(),
			"确认 inbox 为 open needs_user 项，且 wait_user 链未达到上限后重试",
		)
	}

	if strings.TrimSpace(result.Mode) == "focus_batch" {
		if out == outputJSON {
			printJSONOrExit(map[string]any{
				"schema":    "dalek.inbox.reply.accepted.v1",
				"accepted":  result.Accepted,
				"mode":      strings.TrimSpace(result.Mode),
				"inbox_id":  result.InboxID,
				"ticket_id": result.TicketID,
				"worker_id": result.WorkerID,
				"focus_id":  result.FocusID,
				"run_id":    result.RunID,
				"action":    string(result.Action),
			})
			return
		}
		fmt.Printf("reply accepted: inbox#%d action=%s mode=%s ticket=%d\n", result.InboxID, result.Action, result.Mode, result.TicketID)
		fmt.Println("focus controller 将串行恢复当前 blocked item")
		return
	}

	if out == outputJSON {
		printJSONOrExit(map[string]any{
			"schema":      "dalek.inbox.reply.accepted.v1",
			"accepted":    result.Accepted,
			"mode":        strings.TrimSpace(result.Mode),
			"inbox_id":    result.InboxID,
			"ticket_id":   result.TicketID,
			"worker_id":   result.WorkerID,
			"task_run_id": result.RunID,
			"next_action": strings.TrimSpace(result.NextAction),
			"action":      string(result.Action),
		})
		return
	}
	fmt.Printf("reply accepted: inbox#%d action=%s mode=%s ticket=%d worker=%d run=%d\n", result.InboxID, result.Action, result.Mode, result.TicketID, result.WorkerID, result.RunID)
	if result.RunID != 0 {
		fmt.Printf("ticket: dalek ticket show --ticket %d\n", result.TicketID)
		fmt.Printf("events: dalek ticket events --ticket %d\n", result.TicketID)
		fmt.Printf("task: dalek task show --id %d\n", result.RunID)
		fmt.Printf("task_events: dalek task events --id %d\n", result.RunID)
	}
}
