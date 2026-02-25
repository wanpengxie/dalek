package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"dalek/internal/app"
)

func cmdNote(args []string) {
	if len(args) == 0 {
		cmdNoteHelp()
		os.Exit(2)
	}
	sub := strings.TrimSpace(args[0])
	switch sub {
	case "add":
		cmdNoteAdd(args[1:])
	case "ls":
		cmdNoteList(args[1:])
	case "show":
		cmdNoteShow(args[1:])
	case "approve":
		cmdNoteApprove(args[1:])
	case "reject":
		cmdNoteReject(args[1:])
	case "discard":
		cmdNoteDiscard(args[1:])
	case "help", "-h", "--help":
		cmdNoteHelp()
		os.Exit(0)
	default:
		exitUsageError(globalOutput,
			fmt.Sprintf("未知 note 子命令: %s", sub),
			"note 命令组仅支持固定子命令",
			"运行 dalek note --help 查看可用子命令",
		)
	}
}

func cmdNoteHelp() {
	printGroupUsage("Notebook 需求漏斗", "dalek note <command> [flags]", []string{
		"add      添加需求 note（自动 shaping）",
		"ls       列出 note",
		"show     查看 note 详情",
		"approve  审批 note 并创建 ticket",
		"reject   驳回 note",
		"discard  丢弃 note",
	})
	fmt.Fprintln(os.Stderr, "Use \"dalek note <command> --help\" for more information.")
}

func cmdNoteAdd(args []string) {
	fs := flag.NewFlagSet("note add", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		printSubcommandUsage(
			fs,
			"添加需求 note",
			"dalek note add \"...\" [--output text|json]",
			"dalek note add \"需要支持导出 CSV\"",
			"dalek note add --text \"支持自动重试\" -o json",
		)
	}
	home := fs.String("home", globalHome, "dalek Home 目录（默认 ~/.dalek）")
	proj := fs.String("project", globalProject, "项目名（可选）")
	projShort := fs.String("p", globalProject, "项目名（可选）")
	text := fs.String("text", "", "note 文本")
	output := addOutputFlag(fs, "输出格式: text|json（默认 text）")
	parseFlagSetOrExit(fs, args, globalOutput, "note add 参数解析失败", "运行 dalek note add --help 查看参数")
	if strings.TrimSpace(*projShort) != "" {
		*proj = strings.TrimSpace(*projShort)
	}
	out := parseOutputOrExit(*output, true)
	raw := strings.TrimSpace(*text)
	if raw == "" && fs.NArg() > 0 {
		raw = strings.TrimSpace(strings.Join(fs.Args(), " "))
	}
	if raw == "" {
		exitUsageError(out, "缺少 note 文本", "note add 需要文本内容", "dalek note add \"需要支持导出 CSV\"")
	}

	p := mustOpenProjectWithOutput(out, *home, *proj)
	_, daemonClient := mustOpenDaemonClient(out, *home)
	res, err := daemonClient.SubmitNote(context.Background(), app.DaemonNoteSubmitRequest{
		Project: strings.TrimSpace(p.Name()),
		Text:    raw,
	})
	if err != nil {
		if app.IsDaemonUnavailable(err) {
			exitRuntimeError(out, "note add 失败（daemon 不在线）", err.Error(), "请先执行 dalek daemon start 后重试")
		}
		exitRuntimeError(out, "note add 失败", err.Error(), "检查输入内容后重试")
	}

	if out == outputJSON {
		printJSONOrExit(map[string]any{
			"schema":         "dalek.note.add.v1",
			"accepted":       res.Accepted,
			"project":        strings.TrimSpace(res.Project),
			"note_id":        res.NoteID,
			"shaped_item_id": res.ShapedItemID,
			"deduped":        res.Deduped,
			"status": func() string {
				if res.ShapedItemID == 0 {
					return "open"
				}
				return "shaped"
			}(),
		})
		return
	}
	if res.Deduped {
		fmt.Printf("note deduped: note=%d shaped=%d\n", res.NoteID, res.ShapedItemID)
		return
	}
	if res.ShapedItemID == 0 {
		fmt.Printf("note added: note=%d status=open (daemon shaping queued)\n", res.NoteID)
		return
	}
	fmt.Printf("note added: note=%d shaped=%d status=shaped\n", res.NoteID, res.ShapedItemID)
}

func cmdNoteList(args []string) {
	fs := flag.NewFlagSet("note ls", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		printSubcommandUsage(
			fs,
			"列出 note",
			"dalek note ls [--status shaped] [--shaped] [--limit 50] [--output text|json]",
			"dalek note ls",
			"dalek note ls --shaped -o json",
		)
	}
	home := fs.String("home", globalHome, "dalek Home 目录（默认 ~/.dalek）")
	proj := fs.String("project", globalProject, "项目名（可选）")
	projShort := fs.String("p", globalProject, "项目名（可选）")
	status := fs.String("status", "", "按状态过滤（可选）")
	shaped := fs.Bool("shaped", false, "仅展示已进入 shaped 的 note")
	limit := fs.Int("limit", 50, "最多返回条数")
	output := addOutputFlag(fs, "输出格式: text|json（默认 text）")
	parseFlagSetOrExit(fs, args, globalOutput, "note ls 参数解析失败", "运行 dalek note ls --help 查看参数")
	if strings.TrimSpace(*projShort) != "" {
		*proj = strings.TrimSpace(*projShort)
	}
	out := parseOutputOrExit(*output, true)
	p := mustOpenProjectWithOutput(out, *home, *proj)
	items, err := p.ListNotes(context.Background(), app.ListNoteOptions{
		StatusOnly: strings.TrimSpace(*status),
		ShapedOnly: *shaped,
		Limit:      *limit,
	})
	if err != nil {
		exitRuntimeError(out, "note ls 失败", err.Error(), "检查数据库状态后重试")
	}

	if out == outputJSON {
		printJSONOrExit(map[string]any{
			"schema": "dalek.note.list.v1",
			"items":  items,
		})
		return
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tSTATUS\tSHAPED\tTICKET\tTITLE")
	for _, it := range items {
		ticketID := uint(0)
		if it.Shaped != nil {
			ticketID = it.Shaped.TicketID
		}
		title := ""
		if it.Shaped != nil {
			title = strings.TrimSpace(it.Shaped.Title)
		}
		if title == "" {
			title = strings.TrimSpace(it.Text)
			runes := []rune(title)
			if len(runes) > 40 {
				title = string(runes[:40]) + "..."
			}
		}
		fmt.Fprintf(w, "%d\t%s\t%d\t%d\t%s\n", it.ID, it.Status, it.ShapedItemID, ticketID, title)
	}
	_ = w.Flush()
}

func cmdNoteShow(args []string) {
	fs := flag.NewFlagSet("note show", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		printSubcommandUsage(
			fs,
			"查看 note 详情",
			"dalek note show --id <id> [--output text|json]",
			"dalek note show --id 1",
			"dalek note show --id 1 -o json",
		)
	}
	home := fs.String("home", globalHome, "dalek Home 目录（默认 ~/.dalek）")
	proj := fs.String("project", globalProject, "项目名（可选）")
	projShort := fs.String("p", globalProject, "项目名（可选）")
	id := fs.Uint("id", 0, "note id（必填）")
	output := addOutputFlag(fs, "输出格式: text|json（默认 text）")
	parseFlagSetOrExit(fs, args, globalOutput, "note show 参数解析失败", "运行 dalek note show --help 查看参数")
	if strings.TrimSpace(*projShort) != "" {
		*proj = strings.TrimSpace(*projShort)
	}
	out := parseOutputOrExit(*output, true)
	if *id == 0 {
		exitUsageError(out, "缺少 --id", "note show 需要 id", "dalek note show --id 1")
	}
	p := mustOpenProjectWithOutput(out, *home, *proj)
	item, err := p.GetNote(context.Background(), uint(*id))
	if err != nil {
		exitRuntimeError(out, "note show 失败", err.Error(), "检查 note id 后重试")
	}
	if item == nil {
		exitRuntimeError(out, "note 不存在", fmt.Sprintf("id=%d", *id), "运行 dalek note ls 查看可用 id")
	}
	if out == outputJSON {
		printJSONOrExit(map[string]any{
			"schema": "dalek.note.show.v1",
			"item":   item,
		})
		return
	}
	ticketID := uint(0)
	if item.Shaped != nil {
		ticketID = item.Shaped.TicketID
	}
	fmt.Printf("note=%d status=%s shaped=%d ticket=%d\n", item.ID, item.Status, item.ShapedItemID, ticketID)
	fmt.Printf("text: %s\n", strings.TrimSpace(item.Text))
	if item.Shaped != nil {
		fmt.Printf("title: %s\n", strings.TrimSpace(item.Shaped.Title))
		fmt.Printf("description: %s\n", strings.TrimSpace(item.Shaped.Description))
	}
}

func cmdNoteApprove(args []string) {
	fs := flag.NewFlagSet("note approve", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		printSubcommandUsage(
			fs,
			"审批 note 并创建 ticket",
			"dalek note approve --id <id> [--by name] [--output text|json]",
			"dalek note approve --id 1",
			"dalek note approve --id 1 --by pm -o json",
		)
	}
	home := fs.String("home", globalHome, "dalek Home 目录（默认 ~/.dalek）")
	proj := fs.String("project", globalProject, "项目名（可选）")
	projShort := fs.String("p", globalProject, "项目名（可选）")
	id := fs.Uint("id", 0, "note id（必填）")
	by := fs.String("by", "cli", "审批人标识")
	output := addOutputFlag(fs, "输出格式: text|json（默认 text）")
	parseFlagSetOrExit(fs, args, globalOutput, "note approve 参数解析失败", "运行 dalek note approve --help 查看参数")
	if strings.TrimSpace(*projShort) != "" {
		*proj = strings.TrimSpace(*projShort)
	}
	out := parseOutputOrExit(*output, true)
	if *id == 0 {
		exitUsageError(out, "缺少 --id", "note approve 需要 id", "dalek note approve --id 1")
	}
	p := mustOpenProjectWithOutput(out, *home, *proj)
	tk, err := p.ApproveNote(context.Background(), uint(*id), strings.TrimSpace(*by))
	if err != nil {
		exitRuntimeError(out, "note approve 失败", err.Error(), "检查 note 状态后重试")
	}
	if out == outputJSON {
		printJSONOrExit(map[string]any{
			"schema":    "dalek.note.approve.v1",
			"note_id":   uint(*id),
			"ticket_id": tk.ID,
			"title":     strings.TrimSpace(tk.Title),
		})
		return
	}
	fmt.Printf("note approved: note=%d -> ticket=t%d\n", *id, tk.ID)
}

func cmdNoteReject(args []string) {
	fs := flag.NewFlagSet("note reject", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		printSubcommandUsage(
			fs,
			"驳回 note",
			"dalek note reject --id <id> --reason \"...\" [--output text|json]",
			"dalek note reject --id 1 --reason \"信息不完整\"",
			"dalek note reject --id 1 --reason \"与目标不符\" -o json",
		)
	}
	home := fs.String("home", globalHome, "dalek Home 目录（默认 ~/.dalek）")
	proj := fs.String("project", globalProject, "项目名（可选）")
	projShort := fs.String("p", globalProject, "项目名（可选）")
	id := fs.Uint("id", 0, "note id（必填）")
	reason := fs.String("reason", "", "驳回原因（必填）")
	output := addOutputFlag(fs, "输出格式: text|json（默认 text）")
	parseFlagSetOrExit(fs, args, globalOutput, "note reject 参数解析失败", "运行 dalek note reject --help 查看参数")
	if strings.TrimSpace(*projShort) != "" {
		*proj = strings.TrimSpace(*projShort)
	}
	out := parseOutputOrExit(*output, true)
	if *id == 0 {
		exitUsageError(out, "缺少 --id", "note reject 需要 id", "dalek note reject --id 1 --reason \"...\"")
	}
	if strings.TrimSpace(*reason) == "" {
		exitUsageError(out, "缺少 --reason", "note reject 需要驳回原因", "dalek note reject --id 1 --reason \"信息不完整\"")
	}
	p := mustOpenProjectWithOutput(out, *home, *proj)
	if err := p.RejectNote(context.Background(), uint(*id), strings.TrimSpace(*reason)); err != nil {
		exitRuntimeError(out, "note reject 失败", err.Error(), "检查 note 状态后重试")
	}
	if out == outputJSON {
		printJSONOrExit(map[string]any{
			"schema":  "dalek.note.reject.v1",
			"note_id": uint(*id),
			"status":  "rejected",
		})
		return
	}
	fmt.Printf("note rejected: note=%d\n", *id)
}

func cmdNoteDiscard(args []string) {
	fs := flag.NewFlagSet("note discard", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		printSubcommandUsage(
			fs,
			"丢弃 note",
			"dalek note discard --id <id> [--output text|json]",
			"dalek note discard --id 1",
			"dalek note discard --id 1 -o json",
		)
	}
	home := fs.String("home", globalHome, "dalek Home 目录（默认 ~/.dalek）")
	proj := fs.String("project", globalProject, "项目名（可选）")
	projShort := fs.String("p", globalProject, "项目名（可选）")
	id := fs.Uint("id", 0, "note id（必填）")
	output := addOutputFlag(fs, "输出格式: text|json（默认 text）")
	parseFlagSetOrExit(fs, args, globalOutput, "note discard 参数解析失败", "运行 dalek note discard --help 查看参数")
	if strings.TrimSpace(*projShort) != "" {
		*proj = strings.TrimSpace(*projShort)
	}
	out := parseOutputOrExit(*output, true)
	if *id == 0 {
		exitUsageError(out, "缺少 --id", "note discard 需要 id", "dalek note discard --id 1")
	}
	p := mustOpenProjectWithOutput(out, *home, *proj)
	if err := p.DiscardNote(context.Background(), uint(*id)); err != nil {
		exitRuntimeError(out, "note discard 失败", err.Error(), "检查 note 状态后重试")
	}
	if out == outputJSON {
		printJSONOrExit(map[string]any{
			"schema":  "dalek.note.discard.v1",
			"note_id": uint(*id),
			"status":  "discarded",
		})
		return
	}
	fmt.Printf("note discarded: note=%d\n", *id)
}
