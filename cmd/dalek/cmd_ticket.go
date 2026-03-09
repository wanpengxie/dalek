package main

import (
	"dalek/internal/contracts"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"dalek/internal/app"
)

func cmdTicket(args []string) {
	if len(args) == 0 {
		printTicketUsage()
		os.Exit(2)
	}
	sub := strings.TrimSpace(args[0])
	switch sub {
	case "ls":
		cmdTicketList(args[1:])
	case "create":
		cmdTicketCreate(args[1:])
	case "edit":
		cmdTicketEdit(args[1:])
	case "set-priority":
		cmdTicketSetPriority(args[1:])
	case "show":
		cmdTicketShow(args[1:])
	case "start":
		cmdTicketStart(args[1:])
	case "dispatch":
		cmdTicketDispatch(args[1:])
	case "integration":
		cmdTicketIntegration(args[1:])
	case "interrupt":
		cmdTicketInterrupt(args[1:])
	case "stop":
		cmdTicketStop(args[1:])
	case "cleanup":
		cmdTicketCleanup(args[1:])
	case "archive":
		cmdTicketArchive(args[1:])
	case "events":
		cmdTicketEvents(args[1:])
	case "help", "-h", "--help":
		printTicketUsage()
		os.Exit(0)
	default:
		exitUsageError(globalOutput,
			fmt.Sprintf("未知 ticket 子命令: %s", sub),
			"ticket 命令组仅支持固定子命令",
			"运行 dalek ticket --help 查看可用子命令",
		)
	}
}

func printTicketUsage() {
	out := os.Stderr
	fmt.Fprintln(out, "Ticket 生命周期管理")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Usage:")
	fmt.Fprintln(out, "  dalek ticket <command> [flags]")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Commands:")
	fmt.Fprintln(out, "  ls          列出 tickets（默认不含归档）")
	fmt.Fprintln(out, "  create      创建新 ticket")
	fmt.Fprintln(out, "  edit        编辑 ticket 标题/描述")
	fmt.Fprintln(out, "  set-priority 设置 ticket 优先级（high/medium/low/none）")
	fmt.Fprintln(out, "  show        查看 ticket 详情")
	fmt.Fprintln(out, "  start       启动 ticket（创建 worktree + worker runtime）")
	fmt.Fprintln(out, "  dispatch    兼容派发入口（推荐使用 start）")
	fmt.Fprintln(out, "  integration 查看/标记 ticket integration 状态")
	fmt.Fprintln(out, "  interrupt   软中断 worker（发送进程 SIGINT）")
	fmt.Fprintln(out, "  stop        停止 worker")
	fmt.Fprintln(out, "  cleanup     清理 ticket worktree")
	fmt.Fprintln(out, "  archive     归档 ticket")
	fmt.Fprintln(out, "  events      查看事件时间线")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Use \"dalek ticket <command> --help\" for more information.")
}

func cmdTicketList(args []string) {
	fs := flag.NewFlagSet("ticket ls", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "列出 tickets")
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "Usage:")
		fmt.Fprintln(os.Stderr, "  dalek ticket ls [--all] [--timeout 2s] [--output text|json]")
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "Flags:")
		fs.PrintDefaults()
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "Examples:")
		fmt.Fprintln(os.Stderr, "  dalek ticket ls")
		fmt.Fprintln(os.Stderr, "  dalek ticket ls -o json")
	}
	home := fs.String("home", globalHome, "dalek Home 目录（默认 ~/.dalek）")
	proj := fs.String("project", globalProject, "项目名（可选）")
	projShort := fs.String("p", globalProject, "项目名（可选）")
	all := fs.Bool("all", false, "包含已归档 ticket")
	fs.BoolVar(all, "a", false, "包含已归档 ticket")
	timeout := fs.Duration("timeout", 2*time.Second, "超时（默认 2s）")
	output := addOutputFlag(fs, "输出格式: text|json（默认 text）")
	parseFlagSetOrExit(fs, args, globalOutput, "ticket ls 参数解析失败", "运行 dalek ticket ls --help 查看参数")
	if strings.TrimSpace(*projShort) != "" {
		*proj = strings.TrimSpace(*projShort)
	}
	out := parseOutputOrExit(*output, true)
	if *timeout <= 0 {
		exitUsageError(out,
			"非法参数 --timeout",
			"--timeout 必须大于 0",
			"例如: dalek ticket ls --timeout 2s",
		)
	}

	p := mustOpenProjectWithOutput(out, *home, *proj)
	ctx, cancel := projectCtx(*timeout)
	defer cancel()

	tickets, err := p.ListTickets(ctx, *all)
	if err != nil {
		exitRuntimeError(out,
			"查询 ticket 列表失败",
			err.Error(),
			"稍后重试，或检查项目数据库状态",
		)
	}
	views, err := p.ListTicketViews(ctx)
	if err != nil {
		exitRuntimeError(out,
			"查询 ticket 运行态失败",
			err.Error(),
			"稍后重试，或检查 worker 运行状态",
		)
	}
	viewByID := map[uint]app.TicketView{}
	for _, v := range views {
		viewByID[v.Ticket.ID] = v
	}

	type ticketItem struct {
		ID            uint   `json:"id"`
		Title         string `json:"title"`
		Label         string `json:"label"`
		Priority      int    `json:"priority"`
		PriorityLabel string `json:"priority_label"`
		Status        string `json:"status"`
		Runtime       string `json:"runtime"`
		NeedsUser     bool   `json:"needs_user"`
		OutputRef     string `json:"output_ref"`
		WorkerID      *uint  `json:"worker_id,omitempty"`
		Worktree      string `json:"worktree,omitempty"`
		Branch        string `json:"branch,omitempty"`
	}
	items := make([]ticketItem, 0, len(tickets))
	for _, tk := range tickets {
		if !*all && tk.WorkflowStatus == contracts.TicketArchived {
			continue
		}
		v, ok := viewByID[tk.ID]
		status := string(contracts.CanonicalTicketWorkflowStatus(tk.WorkflowStatus))
		runtime := string(contracts.TaskHealthUnknown)
		needsUser := false
		outputRef := "-"
		var workerID *uint
		worktree := ""
		branch := ""
		if ok {
			if strings.TrimSpace(string(v.DerivedStatus)) != "" {
				status = strings.TrimSpace(string(v.DerivedStatus))
			}
			if strings.TrimSpace(string(v.RuntimeHealthState)) != "" {
				runtime = strings.TrimSpace(string(v.RuntimeHealthState))
			}
			needsUser = v.RuntimeNeedsUser
			if v.LatestWorker != nil {
				outputRef = workerOutputRef(v.LatestWorker)
				wid := v.LatestWorker.ID
				workerID = &wid
				worktree = strings.TrimSpace(v.LatestWorker.WorktreePath)
				branch = strings.TrimSpace(v.LatestWorker.Branch)
			}
		}
		items = append(items, ticketItem{
			ID:            tk.ID,
			Title:         strings.TrimSpace(tk.Title),
			Label:         strings.TrimSpace(tk.Label),
			Priority:      tk.Priority,
			PriorityLabel: contracts.TicketPriorityLabel(tk.Priority),
			Status:        status,
			Runtime:       runtime,
			NeedsUser:     needsUser,
			OutputRef:     outputRef,
			WorkerID:      workerID,
			Worktree:      worktree,
			Branch:        branch,
		})
	}

	if out == outputJSON {
		printJSONOrExit(map[string]any{
			"schema":  "dalek.ticket.list.v1",
			"tickets": items,
		})
		return
	}
	if len(items) == 0 {
		fmt.Println("(empty)")
		return
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tLABEL\tPRIORITY\tSTATUS\tRUNTIME\tNEEDS_USER\tOUTPUT\tTITLE")
	for _, it := range items {
		label := "-"
		if strings.TrimSpace(it.Label) != "" {
			label = strings.TrimSpace(it.Label)
		}
		fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%s\t%v\t%s\t%s\n",
			it.ID, label, formatTicketPriority(it.Priority), it.Status, it.Runtime, it.NeedsUser, it.OutputRef, it.Title)
	}
	_ = tw.Flush()
}

func formatTicketPriority(priority int) string {
	label := contracts.TicketPriorityLabel(priority)
	if label == fmt.Sprintf("%d", priority) {
		return label
	}
	return fmt.Sprintf("%s(%d)", label, priority)
}

func outputRefFromRuntime(logPath string) string {
	logPath = strings.TrimSpace(logPath)
	switch {
	case logPath != "":
		return fmt.Sprintf("log=%s", logPath)
	default:
		return "-"
	}
}

func workerOutputRef(w *contracts.Worker) string {
	if w == nil {
		return "-"
	}
	return outputRefFromRuntime(w.LogPath)
}

func cmdTicketCreate(args []string) {
	fs := flag.NewFlagSet("ticket create", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "创建新 ticket")
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "Usage:")
		fmt.Fprintln(os.Stderr, "  dalek ticket create --title <title> --desc <description> [--label <label>|-l <label>] [--priority <high|medium|low|none>]")
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "Flags:")
		fs.PrintDefaults()
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "Examples:")
		fmt.Fprintln(os.Stderr, "  dalek ticket create --title \"实现功能X\" --desc \"需求文档在 /tmp/PRD.md\"")
		fmt.Fprintln(os.Stderr, "  dalek ticket create -t \"修复 Bug\" -d \"详情见 issue #42\" -l \"bugfix\" --priority high -o json")
	}
	home := fs.String("home", globalHome, "dalek Home 目录（默认 ~/.dalek）")
	proj := fs.String("project", globalProject, "项目名（可选）")
	projShort := fs.String("p", globalProject, "项目名（可选）")
	title := fs.String("title", "", "ticket 标题 (required)")
	fs.StringVar(title, "t", "", "ticket 标题 (required)")
	desc := fs.String("desc", "", "ticket 描述 (required)")
	fs.StringVar(desc, "d", "", "ticket 描述 (required)")
	label := fs.String("label", "", "ticket 标签（可选，单标签）")
	fs.StringVar(label, "l", "", "ticket 标签（可选，单标签）")
	priorityName := fs.String("priority", "none", "优先级（可选：high|medium|low|none，默认 none）")
	output := addOutputFlag(fs, "输出格式: text|json（默认 text）")
	parseFlagSetOrExit(fs, args, globalOutput, "ticket create 参数解析失败", "运行 dalek ticket create --help 查看参数")
	if strings.TrimSpace(*projShort) != "" {
		*proj = strings.TrimSpace(*projShort)
	}
	out := parseOutputOrExit(*output, true)
	if strings.TrimSpace(*title) == "" {
		exitUsageError(out,
			"缺少必填参数 --title",
			"ticket 创建需要标题和描述",
			"dalek ticket create --title \"标题\" --desc \"描述\"",
		)
	}
	if strings.TrimSpace(*desc) == "" {
		exitUsageError(out,
			"缺少必填参数 --desc",
			"ticket 创建需要标题和描述",
			"dalek ticket create --title \"标题\" --desc \"描述\"",
		)
	}
	priorityRaw := strings.TrimSpace(*priorityName)
	if priorityRaw == "" {
		priorityRaw = "none"
	}
	priority, ok := contracts.ParseTicketPriority(priorityRaw)
	if !ok {
		exitUsageError(out,
			fmt.Sprintf("非法参数 --priority: %s", priorityRaw),
			"只支持 high、medium、low、none",
			"例如: dalek ticket create --title \"标题\" --desc \"描述\" --priority high",
		)
	}

	p := mustOpenProjectWithOutput(out, *home, *proj)
	ctx, cancel := projectCtx(10 * time.Second)
	defer cancel()
	t, err := p.CreateTicketWithDescriptionAndLabelAndPriority(ctx, *title, *desc, *label, priority)
	if err != nil {
		exitRuntimeError(out,
			"创建 ticket 失败",
			err.Error(),
			"检查参数后重试，或先用 dalek ticket ls 查看现有票据",
		)
	}
	if out == outputJSON {
		printJSONOrExit(map[string]any{
			"schema": "dalek.ticket.create.v1",
			"id":     t.ID,
			"title":  strings.TrimSpace(t.Title),
			"label":  strings.TrimSpace(t.Label),
			"status": "created",
		})
		return
	}
	fmt.Printf("%d\n", t.ID)
}

func cmdTicketEdit(args []string) {
	fs := flag.NewFlagSet("ticket edit", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		printSubcommandUsage(
			fs,
			"编辑 ticket 标题/描述/标签",
			"dalek ticket edit --ticket <id> [--title <title>] [--desc <description>] [--label <label>|-l <label>] [--priority <high|medium|low|none>] [--timeout 2s] [--output text|json]",
			"dalek ticket edit --ticket 1 --title \"新标题\"",
			"dalek ticket edit --ticket 1 --desc \"补充描述\" -l \"backend\" --priority medium -o json",
		)
	}
	home := fs.String("home", globalHome, "dalek Home 目录（默认 ~/.dalek）")
	proj := fs.String("project", globalProject, "项目名（可选）")
	projShort := fs.String("p", globalProject, "项目名（可选）")
	ticketID := fs.Uint("ticket", 0, "ticket ID (required)")
	title := fs.String("title", "", "ticket 标题（可选）")
	desc := fs.String("desc", "", "ticket 描述（可选）")
	fs.StringVar(desc, "d", "", "ticket 描述（可选）")
	label := fs.String("label", "", "ticket 标签（可选，传空字符串可清空）")
	fs.StringVar(label, "l", "", "ticket 标签（可选，传空字符串可清空）")
	priorityName := fs.String("priority", "", "ticket 优先级（可选：high|medium|low|none）")
	timeout := fs.Duration("timeout", 2*time.Second, "超时（默认 2s）")
	output := addOutputFlag(fs, "输出格式: text|json（默认 text）")
	parseFlagSetOrExit(fs, args, globalOutput, "ticket edit 参数解析失败", "运行 dalek ticket edit --help 查看参数")
	if strings.TrimSpace(*projShort) != "" {
		*proj = strings.TrimSpace(*projShort)
	}
	out := parseOutputOrExit(*output, true)
	if *ticketID == 0 {
		exitUsageError(out,
			"缺少必填参数 --ticket",
			"ticket edit 需要 ticket ID",
			"dalek ticket edit --ticket 1 --title \"新标题\"",
		)
	}
	titleProvided := false
	descProvided := false
	labelProvided := false
	priorityProvided := false
	fs.Visit(func(f *flag.Flag) {
		switch strings.TrimSpace(f.Name) {
		case "title":
			titleProvided = true
		case "desc", "d":
			descProvided = true
		case "label", "l":
			labelProvided = true
		case "priority":
			priorityProvided = true
		}
	})
	if !titleProvided && !descProvided && !labelProvided && !priorityProvided {
		exitUsageError(out,
			"缺少可更新字段",
			"ticket edit 至少需要 --title、--desc、--label 或 --priority 之一",
			"dalek ticket edit --ticket 1 --title \"新标题\"",
		)
	}
	if titleProvided && trimOneLine(*title) == "" {
		exitUsageError(out,
			"非法参数 --title",
			"--title 不能为空",
			"请提供非空标题，例如 --title \"修正后的标题\"",
		)
	}
	if descProvided && strings.TrimSpace(*desc) == "" {
		exitUsageError(out,
			"非法参数 --desc",
			"--desc 不能为空",
			"请提供非空描述，例如 --desc \"补充需求背景\"",
		)
	}
	var parsedPriority int
	if priorityProvided {
		priorityRaw := strings.TrimSpace(*priorityName)
		if priorityRaw == "" {
			exitUsageError(out,
				"非法参数 --priority",
				"--priority 不能为空",
				"可选值：high、medium、low、none",
			)
		}
		priority, ok := contracts.ParseTicketPriority(priorityRaw)
		if !ok {
			exitUsageError(out,
				fmt.Sprintf("非法参数 --priority: %s", priorityRaw),
				"只支持 high、medium、low、none",
				"例如: dalek ticket edit --ticket 1 --priority high",
			)
		}
		parsedPriority = priority
	}
	if *timeout <= 0 {
		exitUsageError(out,
			"非法参数 --timeout",
			"--timeout 必须大于 0",
			"例如: dalek ticket edit --ticket 1 --timeout 2s",
		)
	}

	p := mustOpenProjectWithOutput(out, *home, *proj)
	ctx, cancel := projectCtx(*timeout)
	defer cancel()
	tickets, err := p.ListTickets(ctx, true)
	if err != nil {
		exitRuntimeError(out,
			"读取 ticket 失败",
			err.Error(),
			"稍后重试，或检查项目数据库状态",
		)
	}
	var tk *contracts.Ticket
	for i := range tickets {
		if tickets[i].ID == uint(*ticketID) {
			tk = &tickets[i]
			break
		}
	}
	if tk == nil {
		exitRuntimeError(out,
			fmt.Sprintf("ticket #%d 不存在", *ticketID),
			"指定的 ticket ID 在当前项目中未找到",
			"使用 dalek ticket ls 查看可用 tickets",
		)
	}
	if contracts.CanonicalTicketWorkflowStatus(tk.WorkflowStatus) == contracts.TicketArchived {
		exitRuntimeError(out,
			fmt.Sprintf("ticket #%d 已归档，禁止编辑", *ticketID),
			"归档 ticket 为只读状态",
			"如需继续修改，请先创建新 ticket",
		)
	}

	nextTitle := tk.Title
	nextDesc := tk.Description
	nextLabel := tk.Label
	nextPriority := tk.Priority
	if titleProvided {
		nextTitle = *title
	}
	if descProvided {
		nextDesc = *desc
	}
	if labelProvided {
		nextLabel = *label
	}
	if priorityProvided {
		nextPriority = parsedPriority
	}
	var errUpdate error
	if labelProvided {
		errUpdate = p.UpdateTicketTextAndLabelAndPriority(ctx, uint(*ticketID), nextTitle, nextDesc, nextLabel, nextPriority)
	} else {
		errUpdate = p.UpdateTicketTextAndPriority(ctx, uint(*ticketID), nextTitle, nextDesc, nextPriority)
	}
	if errUpdate != nil {
		exitRuntimeError(out,
			"更新 ticket 文本失败",
			errUpdate.Error(),
			"检查 --title/--desc/--label/--priority 参数后重试",
		)
	}

	finalTitle := trimOneLine(nextTitle)
	finalDesc := strings.TrimSpace(nextDesc)
	finalLabel := trimOneLine(nextLabel)
	status := string(contracts.CanonicalTicketWorkflowStatus(tk.WorkflowStatus))
	if out == outputJSON {
		printJSONOrExit(map[string]any{
			"schema":      "dalek.ticket.edit.v1",
			"ticket_id":   uint(*ticketID),
			"title":       finalTitle,
			"description": finalDesc,
			"label":       finalLabel,
			"status":      status,
		})
		return
	}
	fmt.Printf("t%d updated\n", *ticketID)
}

func cmdTicketSetPriority(args []string) {
	fs := flag.NewFlagSet("ticket set-priority", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		printSubcommandUsage(
			fs,
			"设置 ticket 优先级",
			"dalek ticket set-priority --ticket <id> --priority <high|medium|low|none> [--timeout 2s] [--output text|json]",
			"dalek ticket set-priority --ticket 1 --priority high",
			"dalek ticket set-priority --ticket 1 --priority low -o json",
		)
	}
	home := fs.String("home", globalHome, "dalek Home 目录（默认 ~/.dalek）")
	proj := fs.String("project", globalProject, "项目名（可选）")
	projShort := fs.String("p", globalProject, "项目名（可选）")
	ticketID := fs.Uint("ticket", 0, "ticket ID (required)")
	priorityName := fs.String("priority", "", "优先级（high|medium|low|none）")
	timeout := fs.Duration("timeout", 2*time.Second, "超时（默认 2s）")
	output := addOutputFlag(fs, "输出格式: text|json（默认 text）")
	parseFlagSetOrExit(fs, args, globalOutput, "ticket set-priority 参数解析失败", "运行 dalek ticket set-priority --help 查看参数")
	if strings.TrimSpace(*projShort) != "" {
		*proj = strings.TrimSpace(*projShort)
	}
	out := parseOutputOrExit(*output, true)
	if *ticketID == 0 {
		exitUsageError(out,
			"缺少必填参数 --ticket",
			"ticket set-priority 需要 ticket ID",
			"dalek ticket set-priority --ticket 1 --priority high",
		)
	}
	priorityRaw := strings.TrimSpace(*priorityName)
	if priorityRaw == "" {
		exitUsageError(out,
			"缺少必填参数 --priority",
			"ticket set-priority 需要优先级",
			"可选值：high、medium、low、none",
		)
	}
	priority, ok := contracts.ParseTicketPriority(priorityRaw)
	if !ok {
		exitUsageError(out,
			fmt.Sprintf("非法参数 --priority: %s", priorityRaw),
			"只支持 high、medium、low、none",
			"例如: dalek ticket set-priority --ticket 1 --priority high",
		)
	}
	if *timeout <= 0 {
		exitUsageError(out,
			"非法参数 --timeout",
			"--timeout 必须大于 0",
			"例如: dalek ticket set-priority --ticket 1 --timeout 2s",
		)
	}

	p := mustOpenProjectWithOutput(out, *home, *proj)
	ctx, cancel := projectCtx(*timeout)
	defer cancel()

	if err := p.SetTicketPriority(ctx, uint(*ticketID), priority); err != nil {
		exitRuntimeError(out,
			"设置 ticket 优先级失败",
			err.Error(),
			"检查 ticket 是否存在，以及 --priority 参数是否正确",
		)
	}

	label := contracts.TicketPriorityLabel(priority)
	if out == outputJSON {
		printJSONOrExit(map[string]any{
			"schema":         "dalek.ticket.set_priority.v1",
			"ticket_id":      uint(*ticketID),
			"priority":       priority,
			"priority_label": label,
			"status":         "updated",
		})
		return
	}
	fmt.Printf("t%d priority => %s(%d)\n", *ticketID, label, priority)
}

func cmdTicketShow(args []string) {
	fs := flag.NewFlagSet("ticket show", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "查看 ticket 详情")
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "Usage:")
		fmt.Fprintln(os.Stderr, "  dalek ticket show --ticket <id> [--output text|json]")
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "Flags:")
		fs.PrintDefaults()
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "Examples:")
		fmt.Fprintln(os.Stderr, "  dalek ticket show --ticket 1")
		fmt.Fprintln(os.Stderr, "  dalek ticket show -t 1 -o json")
	}
	home := fs.String("home", globalHome, "dalek Home 目录（默认 ~/.dalek）")
	proj := fs.String("project", globalProject, "项目名（可选）")
	projShort := fs.String("p", globalProject, "项目名（可选）")
	ticketID := fs.Uint("ticket", 0, "ticket ID (required)")
	fs.UintVar(ticketID, "t", 0, "ticket ID (required)")
	timeout := fs.Duration("timeout", 2*time.Second, "超时（默认 2s）")
	output := addOutputFlag(fs, "输出格式: text|json（默认 text）")
	parseFlagSetOrExit(fs, args, globalOutput, "ticket show 参数解析失败", "运行 dalek ticket show --help 查看参数")
	if strings.TrimSpace(*projShort) != "" {
		*proj = strings.TrimSpace(*projShort)
	}
	out := parseOutputOrExit(*output, true)
	if *ticketID == 0 {
		exitUsageError(out,
			"缺少必填参数 --ticket",
			"ticket show 需要 ticket ID",
			"dalek ticket show --ticket 1",
		)
	}
	if *timeout <= 0 {
		exitUsageError(out,
			"非法参数 --timeout",
			"--timeout 必须大于 0",
			"例如: dalek ticket show --ticket 1 --timeout 2s",
		)
	}

	p := mustOpenProjectWithOutput(out, *home, *proj)
	ctx, cancel := projectCtx(*timeout)
	defer cancel()

	tickets, err := p.ListTickets(ctx, true)
	if err != nil {
		exitRuntimeError(out,
			"读取 ticket 详情失败",
			err.Error(),
			"稍后重试，或检查数据库状态",
		)
	}
	var tk *contracts.Ticket
	for i := range tickets {
		if tickets[i].ID == uint(*ticketID) {
			tk = &tickets[i]
			break
		}
	}
	if tk == nil {
		exitRuntimeError(out,
			fmt.Sprintf("ticket #%d 不存在", *ticketID),
			"指定的 ticket ID 在当前项目中未找到",
			"使用 dalek ticket ls 查看可用 tickets",
		)
	}

	views, err := p.ListTicketViews(ctx)
	if err != nil {
		exitRuntimeError(out,
			"读取 ticket 运行态失败",
			err.Error(),
			"稍后重试，或检查 worker 状态",
		)
	}
	var view *app.TicketView
	for i := range views {
		if views[i].Ticket.ID == tk.ID {
			view = &views[i]
			break
		}
	}
	worker := (*contracts.Worker)(nil)
	if view != nil && view.LatestWorker != nil {
		worker = view.LatestWorker
	} else {
		w, werr := p.LatestWorker(ctx, tk.ID)
		if werr == nil {
			worker = w
		}
	}

	status := string(tk.WorkflowStatus)
	runtime := string(contracts.TaskHealthUnknown)
	needsUser := false
	if view != nil {
		if strings.TrimSpace(string(view.DerivedStatus)) != "" {
			status = strings.TrimSpace(string(view.DerivedStatus))
		}
		if strings.TrimSpace(string(view.RuntimeHealthState)) != "" {
			runtime = strings.TrimSpace(string(view.RuntimeHealthState))
		}
		needsUser = view.RuntimeNeedsUser
	}

	type workerJSON struct {
		ID        uint   `json:"id"`
		OutputRef string `json:"output_ref"`
		LogPath   string `json:"log_path"`
		Worktree  string `json:"worktree"`
		Branch    string `json:"branch"`
		Runtime   string `json:"runtime"`
		NeedsUser bool   `json:"needs_user"`
	}
	var workerPayload *workerJSON
	if worker != nil {
		workerPayload = &workerJSON{
			ID:        worker.ID,
			OutputRef: workerOutputRef(worker),
			LogPath:   strings.TrimSpace(worker.LogPath),
			Worktree:  strings.TrimSpace(worker.WorktreePath),
			Branch:    strings.TrimSpace(worker.Branch),
			Runtime:   runtime,
			NeedsUser: needsUser,
		}
	}

	if out == outputJSON {
		printJSONOrExit(map[string]any{
			"schema":         "dalek.ticket.show.v1",
			"id":             tk.ID,
			"title":          strings.TrimSpace(tk.Title),
			"description":    strings.TrimSpace(tk.Description),
			"label":          strings.TrimSpace(tk.Label),
			"priority":       tk.Priority,
			"priority_label": contracts.TicketPriorityLabel(tk.Priority),
			"status":         status,
			"created_at":     tk.CreatedAt.Local().Format(time.RFC3339),
			"worker":         workerPayload,
		})
		return
	}

	fmt.Printf("ticket:\t#%d\n", tk.ID)
	fmt.Printf("title:\t%s\n", strings.TrimSpace(tk.Title))
	fmt.Printf("desc:\t%s\n", strings.TrimSpace(tk.Description))
	label := strings.TrimSpace(tk.Label)
	if label == "" {
		label = "-"
	}
	fmt.Printf("label:\t%s\n", label)
	fmt.Printf("priority:\t%s\n", formatTicketPriority(tk.Priority))
	fmt.Printf("status:\t%s\n", status)
	fmt.Printf("created_at:\t%s\n", tk.CreatedAt.Local().Format("2006-01-02 15:04:05"))
	fmt.Println()
	if worker == nil {
		fmt.Println("worker:\t-")
		fmt.Println("output:\t-")
		fmt.Println("worktree:\t-")
		fmt.Println("branch:\t-")
		fmt.Printf("runtime:\t%s\n", runtime)
		fmt.Printf("needs_user:\t%v\n", needsUser)
		return
	}
	fmt.Printf("worker:\tw%d\n", worker.ID)
	fmt.Printf("output:\t%s\n", workerOutputRef(worker))
	fmt.Printf("worktree:\t%s\n", strings.TrimSpace(worker.WorktreePath))
	fmt.Printf("branch:\t%s\n", strings.TrimSpace(worker.Branch))
	fmt.Printf("runtime:\t%s\n", runtime)
	fmt.Printf("needs_user:\t%v\n", needsUser)
}

func cmdTicketStart(args []string) {
	fs := flag.NewFlagSet("ticket start", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		printSubcommandUsage(
			fs,
			"启动 ticket",
			"dalek ticket start --ticket <id> [--base BRANCH] [--timeout 60s] [--output text|json]",
			"dalek ticket start --ticket 1",
			"dalek ticket start --ticket 1 --base main -o json",
		)
	}
	home := fs.String("home", globalHome, "dalek Home 目录（默认 ~/.dalek）")
	proj := fs.String("project", globalProject, "项目名（可选）")
	projShort := fs.String("p", globalProject, "项目名（可选）")
	ticketID := fs.Uint("ticket", 0, "ticket ID (required)")
	fs.UintVar(ticketID, "t", 0, "ticket ID (required)")
	base := fs.String("base", "", "基准分支（可选，默认当前 HEAD）")
	fs.StringVar(base, "b", "", "基准分支（可选，默认当前 HEAD）")
	timeout := fs.Duration("timeout", 60*time.Second, "超时（默认 60s）")
	output := addOutputFlag(fs, "输出格式: text|json（默认 text）")
	parseFlagSetOrExit(fs, args, globalOutput, "ticket start 参数解析失败", "运行 dalek ticket start --help 查看参数")
	if strings.TrimSpace(*projShort) != "" {
		*proj = strings.TrimSpace(*projShort)
	}
	out := parseOutputOrExit(*output, true)
	if *ticketID == 0 {
		exitUsageError(out,
			"缺少必填参数 --ticket",
			"ticket start 需要 ticket ID",
			"dalek ticket start --ticket 1",
		)
	}
	if *timeout <= 0 {
		exitUsageError(out,
			"非法参数 --timeout",
			"--timeout 必须大于 0",
			"例如: dalek ticket start --ticket 1 --timeout 60s",
		)
	}

	p := mustOpenProjectWithOutput(out, *home, *proj)
	ctx, cancel := projectCtx(*timeout)
	defer cancel()
	a, err := p.StartTicketWithOptions(ctx, uint(*ticketID), app.StartOptions{BaseBranch: strings.TrimSpace(*base)})
	if err != nil {
		exitRuntimeError(out,
			"启动 ticket 失败",
			err.Error(),
			"确认 ticket 存在且当前仓库状态正常，然后重试",
		)
	}

	if out == outputJSON {
		payload := map[string]any{
			"schema":     "dalek.ticket.start.v1",
			"ticket_id":  uint(*ticketID),
			"worker_id":  a.ID,
			"output_ref": outputRefFromRuntime(a.LogPath),
			"log_path":   strings.TrimSpace(a.LogPath),
			"worktree":   strings.TrimSpace(a.WorktreePath),
			"branch":     strings.TrimSpace(a.Branch),
		}
		printJSONOrExit(payload)
		return
	}
	fmt.Printf("t%d started: worker=w%d output=%s worktree=%s branch=%s\n",
		*ticketID, a.ID, outputRefFromRuntime(a.LogPath), strings.TrimSpace(a.WorktreePath), strings.TrimSpace(a.Branch))
}

func cmdTicketDispatch(args []string) {
	fs := flag.NewFlagSet("ticket dispatch", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		printSubcommandUsage(
			fs,
			"兼容派发入口（推荐使用 ticket start）",
			"dalek ticket dispatch --ticket <id> [--prompt \"...\"] [--auto-start=true|false] [--timeout 30s] [--output text|json]",
			"dalek ticket dispatch --ticket 1",
			"dalek ticket dispatch --ticket 1 --auto-start=false",
			"dalek ticket dispatch --ticket 1 --timeout 30s",
			"dalek ticket dispatch --ticket 1 --prompt \"补充说明\" -o json",
		)
	}
	home := fs.String("home", globalHome, "dalek Home 目录（默认 ~/.dalek）")
	proj := fs.String("project", globalProject, "项目名（可选）")
	projShort := fs.String("p", globalProject, "项目名（可选）")
	ticketID := fs.Uint("ticket", 0, "ticket ID (required)")
	fs.UintVar(ticketID, "t", 0, "ticket ID (required)")
	entryPrompt := fs.String("prompt", "", "补充本轮指令（可选）")
	autoStart := fs.Bool("auto-start", true, "ticket 未启动时是否自动 start（默认 true）")
	requestID := fs.String("request-id", "", "幂等请求 ID（可选）")
	timeout := fs.Duration("timeout", 0, "超时（可选，必须 >0）")
	output := addOutputFlag(fs, "输出格式: text|json（默认 text）")
	parseFlagSetOrExit(fs, args, globalOutput, "ticket dispatch 参数解析失败", "运行 dalek ticket dispatch --help 查看参数")
	timeoutProvided := flagProvided(fs, "timeout")
	if strings.TrimSpace(*projShort) != "" {
		*proj = strings.TrimSpace(*projShort)
	}
	out := parseOutputOrExit(*output, true)
	if *ticketID == 0 {
		exitUsageError(out,
			"缺少必填参数 --ticket",
			"ticket dispatch 需要 ticket ID",
			"dalek ticket dispatch --ticket 1",
		)
	}
	if timeoutProvided && *timeout <= 0 {
		exitUsageError(out,
			"非法参数 --timeout",
			"--timeout 必须为正值",
			"例如: dalek ticket dispatch --ticket 1 --timeout 120m",
		)
	}
	enforceDispatchDepthGuardOrExit(out, "dalek ticket dispatch")

	p := mustOpenProjectWithOutput(out, *home, *proj)
	_, daemonClient := mustOpenDaemonClient(out, *home)
	ctx, cancel := projectCtx(*timeout)
	defer cancel()
	receipt, err := daemonClient.SubmitDispatch(ctx, app.DaemonDispatchSubmitRequest{
		Project:   strings.TrimSpace(p.Name()),
		TicketID:  uint(*ticketID),
		RequestID: strings.TrimSpace(*requestID),
		Prompt:    strings.TrimSpace(*entryPrompt),
		AutoStart: autoStart,
	})
	if err != nil {
		if app.IsDaemonUnavailable(err) {
			exitFixFirstError(out, 1,
				"daemon 不在线，无法异步派发",
				daemonUnavailableDispatchFix(uint(*ticketID)),
				daemonRuntimeErrorCause(err),
			)
		}
		exitRuntimeError(out,
			"异步派发失败",
			daemonRuntimeErrorCause(err),
			"检查 daemon 日志（dalek daemon logs）后重试",
		)
	}

	if out == outputJSON {
		printJSONOrExit(map[string]any{
			"schema":      "dalek.ticket.dispatch.accepted.v1",
			"mode":        "async",
			"accepted":    receipt.Accepted,
			"project":     strings.TrimSpace(receipt.Project),
			"request_id":  strings.TrimSpace(receipt.RequestID),
			"ticket_id":   receipt.TicketID,
			"worker_id":   receipt.WorkerID,
			"task_run_id": receipt.TaskRunID,
			"query": map[string]string{
				"show":   fmt.Sprintf("dalek task show --id %d", receipt.TaskRunID),
				"events": fmt.Sprintf("dalek task events --id %d", receipt.TaskRunID),
				"cancel": fmt.Sprintf("dalek task cancel --id %d", receipt.TaskRunID),
			},
		})
		return
	}

	fmt.Printf("dispatch accepted: ticket=%d worker=%d request=%s run=%d\n",
		receipt.TicketID, receipt.WorkerID, strings.TrimSpace(receipt.RequestID), receipt.TaskRunID)
	fmt.Printf("query: dalek task show --id %d\n", receipt.TaskRunID)
	fmt.Printf("events: dalek task events --id %d\n", receipt.TaskRunID)
	fmt.Printf("cancel: dalek task cancel --id %d\n", receipt.TaskRunID)
}

func cmdTicketIntegration(args []string) {
	if len(args) == 0 {
		printTicketIntegrationUsage()
		os.Exit(2)
	}
	switch strings.TrimSpace(args[0]) {
	case "status":
		cmdTicketIntegrationStatus(args[1:])
	case "abandon":
		cmdTicketIntegrationAbandon(args[1:])
	case "help", "-h", "--help":
		printTicketIntegrationUsage()
		os.Exit(0)
	default:
		exitUsageError(globalOutput,
			fmt.Sprintf("未知 ticket integration 子命令: %s", strings.TrimSpace(args[0])),
			"ticket integration 仅支持 status|abandon",
			"运行 dalek ticket integration --help 查看可用子命令",
		)
	}
}

func printTicketIntegrationUsage() {
	out := os.Stderr
	fmt.Fprintln(out, "Ticket integration 状态管理")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Usage:")
	fmt.Fprintln(out, "  dalek ticket integration <command> [flags]")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Commands:")
	fmt.Fprintln(out, "  status     查看 ticket integration 状态")
	fmt.Fprintln(out, "  abandon    手动放弃 ticket integration")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Use \"dalek ticket integration <command> --help\" for more information.")
}

func cmdTicketIntegrationStatus(args []string) {
	fs := flag.NewFlagSet("ticket integration status", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		printSubcommandUsage(
			fs,
			"查看 ticket integration 状态",
			"dalek ticket integration status --ticket <id> [--timeout 5s] [--output text|json]",
			"dalek ticket integration status --ticket 1",
			"dalek ticket integration status --ticket 1 -o json",
		)
	}
	home := fs.String("home", globalHome, "dalek Home 目录（默认 ~/.dalek）")
	proj := fs.String("project", globalProject, "项目名（可选）")
	projShort := fs.String("p", globalProject, "项目名（可选）")
	ticketID := fs.Uint("ticket", 0, "ticket ID (required)")
	fs.UintVar(ticketID, "t", 0, "ticket ID (required)")
	timeout := fs.Duration("timeout", 5*time.Second, "超时（默认 5s）")
	output := addOutputFlag(fs, "输出格式: text|json（默认 text）")
	parseFlagSetOrExit(fs, args, globalOutput, "ticket integration status 参数解析失败", "运行 dalek ticket integration status --help 查看参数")
	if strings.TrimSpace(*projShort) != "" {
		*proj = strings.TrimSpace(*projShort)
	}
	out := parseOutputOrExit(*output, true)
	if *ticketID == 0 {
		exitUsageError(out,
			"缺少必填参数 --ticket",
			"ticket integration status 需要 ticket ID",
			"dalek ticket integration status --ticket 1",
		)
	}
	if *timeout <= 0 {
		exitUsageError(out,
			"非法参数 --timeout",
			"--timeout 必须大于 0",
			"例如: dalek ticket integration status --ticket 1 --timeout 5s",
		)
	}

	p := mustOpenProjectWithOutput(out, *home, *proj)
	ctx, cancel := projectCtx(*timeout)
	defer cancel()
	view, err := p.GetTicketViewByID(ctx, uint(*ticketID))
	if err != nil {
		exitRuntimeError(out,
			"读取 ticket integration 状态失败",
			err.Error(),
			"确认 ticket 存在后重试",
		)
	}
	if view == nil {
		exitRuntimeError(out,
			"读取 ticket integration 状态失败",
			"ticket 不存在",
			"确认 ticket ID 后重试",
		)
	}
	tk := view.Ticket
	status := contracts.CanonicalIntegrationStatus(tk.IntegrationStatus)
	statusText := string(status)
	if statusText == "" {
		statusText = "-"
	}
	anchor := strings.TrimSpace(tk.MergeAnchorSHA)
	if anchor == "" {
		anchor = "-"
	}
	target := strings.TrimSpace(tk.TargetBranch)
	if target == "" {
		target = "-"
	}
	mergedAt := ""
	if tk.MergedAt != nil && !tk.MergedAt.IsZero() {
		mergedAt = tk.MergedAt.Local().Format("2006-01-02 15:04:05")
	}
	reason := strings.TrimSpace(tk.AbandonedReason)

	if out == outputJSON {
		printJSONOrExit(map[string]any{
			"schema":             "dalek.ticket.integration.status.v1",
			"ticket_id":          tk.ID,
			"workflow_status":    string(contracts.CanonicalTicketWorkflowStatus(tk.WorkflowStatus)),
			"integration_status": string(status),
			"merge_anchor_sha":   strings.TrimSpace(tk.MergeAnchorSHA),
			"target_branch":      strings.TrimSpace(tk.TargetBranch),
			"merged_at":          mergedAt,
			"abandoned_reason":   reason,
		})
		return
	}
	fmt.Printf("ticket:\tt%d\n", tk.ID)
	fmt.Printf("workflow:\t%s\n", contracts.CanonicalTicketWorkflowStatus(tk.WorkflowStatus))
	fmt.Printf("integration:\t%s\n", statusText)
	fmt.Printf("anchor:\t%s\n", anchor)
	fmt.Printf("target:\t%s\n", target)
	if mergedAt == "" {
		fmt.Println("merged_at:\t-")
	} else {
		fmt.Printf("merged_at:\t%s\n", mergedAt)
	}
	if reason == "" {
		fmt.Println("reason:\t-")
	} else {
		fmt.Printf("reason:\t%s\n", reason)
	}
}

func cmdTicketIntegrationAbandon(args []string) {
	fs := flag.NewFlagSet("ticket integration abandon", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		printSubcommandUsage(
			fs,
			"手动放弃 ticket integration",
			"dalek ticket integration abandon --ticket <id> --reason \"...\" [--timeout 5s] [--output text|json]",
			"dalek ticket integration abandon --ticket 1 --reason \"不再需要合并\"",
			"dalek ticket integration abandon --ticket 1 --reason \"需求已变更\" -o json",
		)
	}
	home := fs.String("home", globalHome, "dalek Home 目录（默认 ~/.dalek）")
	proj := fs.String("project", globalProject, "项目名（可选）")
	projShort := fs.String("p", globalProject, "项目名（可选）")
	ticketID := fs.Uint("ticket", 0, "ticket ID (required)")
	fs.UintVar(ticketID, "t", 0, "ticket ID (required)")
	reason := fs.String("reason", "", "abandon 理由 (required)")
	timeout := fs.Duration("timeout", 5*time.Second, "超时（默认 5s）")
	output := addOutputFlag(fs, "输出格式: text|json（默认 text）")
	parseFlagSetOrExit(fs, args, globalOutput, "ticket integration abandon 参数解析失败", "运行 dalek ticket integration abandon --help 查看参数")
	if strings.TrimSpace(*projShort) != "" {
		*proj = strings.TrimSpace(*projShort)
	}
	out := parseOutputOrExit(*output, true)
	if *ticketID == 0 {
		exitUsageError(out,
			"缺少必填参数 --ticket",
			"ticket integration abandon 需要 ticket ID",
			"dalek ticket integration abandon --ticket 1 --reason \"需求变更\"",
		)
	}
	if strings.TrimSpace(*reason) == "" {
		exitUsageError(out,
			"缺少必填参数 --reason",
			"ticket integration abandon 需要说明原因",
			"dalek ticket integration abandon --ticket 1 --reason \"需求变更\"",
		)
	}
	if *timeout <= 0 {
		exitUsageError(out,
			"非法参数 --timeout",
			"--timeout 必须大于 0",
			"例如: dalek ticket integration abandon --ticket 1 --reason \"需求变更\" --timeout 5s",
		)
	}

	p := mustOpenProjectWithOutput(out, *home, *proj)
	ctx, cancel := projectCtx(*timeout)
	defer cancel()
	if err := p.AbandonTicketIntegration(ctx, uint(*ticketID), strings.TrimSpace(*reason)); err != nil {
		exitRuntimeError(out,
			"abandon ticket integration 失败",
			err.Error(),
			"确认 ticket 已 done 且 integration_status 为 needs_merge/merged 后重试",
		)
	}

	if out == outputJSON {
		printJSONOrExit(map[string]any{
			"schema":             "dalek.ticket.integration.abandon.v1",
			"ticket_id":          uint(*ticketID),
			"integration_status": string(contracts.IntegrationAbandoned),
			"abandoned_reason":   strings.TrimSpace(*reason),
		})
		return
	}
	fmt.Printf("ticket t%d integration abandoned: %s\n", *ticketID, strings.TrimSpace(*reason))
}

func cmdTicketInterrupt(args []string) {
	fs := flag.NewFlagSet("ticket interrupt", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		printSubcommandUsage(
			fs,
			"软中断 worker/ticket",
			"dalek ticket interrupt (--ticket <id> | --worker <id>) [--timeout 10s] [--output text|json]",
			"dalek ticket interrupt --ticket 1",
			"dalek ticket interrupt --worker 3 -o json",
		)
	}
	home := fs.String("home", globalHome, "dalek Home 目录（默认 ~/.dalek）")
	proj := fs.String("project", globalProject, "项目名（可选）")
	projShort := fs.String("p", globalProject, "项目名（可选）")
	ticketID := fs.Uint("ticket", 0, "ticket ID（与 --worker 二选一）")
	fs.UintVar(ticketID, "t", 0, "ticket ID（与 --worker 二选一）")
	workerID := fs.Uint("worker", 0, "worker ID（优先）")
	fs.UintVar(workerID, "w", 0, "worker ID（优先）")
	timeout := fs.Duration("timeout", 10*time.Second, "超时（默认 10s）")
	output := addOutputFlag(fs, "输出格式: text|json（默认 text）")
	parseFlagSetOrExit(fs, args, globalOutput, "ticket interrupt 参数解析失败", "运行 dalek ticket interrupt --help 查看参数")
	if strings.TrimSpace(*projShort) != "" {
		*proj = strings.TrimSpace(*projShort)
	}
	out := parseOutputOrExit(*output, true)
	if *ticketID == 0 && *workerID == 0 {
		exitUsageError(out,
			"缺少目标参数",
			"ticket interrupt 需要 --ticket 或 --worker",
			"dalek ticket interrupt --ticket 1",
		)
	}
	if *timeout <= 0 {
		exitUsageError(out,
			"非法参数 --timeout",
			"--timeout 必须大于 0",
			"例如: dalek ticket interrupt --ticket 1 --timeout 10s",
		)
	}

	p := mustOpenProjectWithOutput(out, *home, *proj)
	ctx, cancel := projectCtx(*timeout)
	defer cancel()

	var r app.InterruptResult
	if *workerID != 0 {
		rr, err := p.InterruptWorker(ctx, uint(*workerID))
		if err != nil {
			exitRuntimeError(out,
				"中断 worker 失败",
				err.Error(),
				"确认 worker 正在运行并重试",
			)
		}
		r = rr
	} else {
		rr, err := p.InterruptTicket(ctx, uint(*ticketID))
		if err != nil {
			exitRuntimeError(out,
				"中断 ticket 失败",
				err.Error(),
				"确认 ticket 已启动并重试",
			)
		}
		r = rr
	}

	if out == outputJSON {
		payload := map[string]any{
			"schema":      "dalek.ticket.interrupt.v1",
			"ticket_id":   r.TicketID,
			"worker_id":   r.WorkerID,
			"mode":        strings.TrimSpace(r.Mode),
			"task_run_id": r.TaskRunID,
			"output_ref":  outputRefFromRuntime(r.LogPath),
			"log_path":    strings.TrimSpace(r.LogPath),
		}
		printJSONOrExit(payload)
		return
	}
	fmt.Printf("interrupted: t%d worker=w%d mode=%s output=%s",
		r.TicketID, r.WorkerID, strings.TrimSpace(r.Mode), outputRefFromRuntime(r.LogPath))
	fmt.Println()
}

func cmdTicketStop(args []string) {
	fs := flag.NewFlagSet("ticket stop", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		printSubcommandUsage(
			fs,
			"停止 worker/ticket",
			"dalek ticket stop (--ticket <id> | --worker <id> | --all) [--timeout 15s] [--output text|json]",
			"dalek ticket stop --ticket 1",
			"dalek ticket stop --all -o json",
		)
	}
	home := fs.String("home", globalHome, "dalek Home 目录（默认 ~/.dalek）")
	proj := fs.String("project", globalProject, "项目名（可选）")
	projShort := fs.String("p", globalProject, "项目名（可选）")
	all := fs.Bool("all", false, "停止所有 workers")
	fs.BoolVar(all, "a", false, "停止所有 workers")
	ticketID := fs.Uint("ticket", 0, "ticket ID")
	fs.UintVar(ticketID, "t", 0, "ticket ID")
	workerID := fs.Uint("worker", 0, "worker ID")
	fs.UintVar(workerID, "w", 0, "worker ID")
	timeout := fs.Duration("timeout", 15*time.Second, "超时（默认 15s）")
	output := addOutputFlag(fs, "输出格式: text|json（默认 text）")
	parseFlagSetOrExit(fs, args, globalOutput, "ticket stop 参数解析失败", "运行 dalek ticket stop --help 查看参数")
	if strings.TrimSpace(*projShort) != "" {
		*proj = strings.TrimSpace(*projShort)
	}
	out := parseOutputOrExit(*output, true)

	choiceCount := 0
	if *all {
		choiceCount++
	}
	if *ticketID != 0 {
		choiceCount++
	}
	if *workerID != 0 {
		choiceCount++
	}
	if choiceCount != 1 {
		exitUsageError(out,
			"缺少或冲突的目标参数",
			"ticket stop 需要且仅允许一个目标：--ticket、--worker、或 --all",
			"dalek ticket stop --ticket 1",
		)
	}
	if *timeout <= 0 {
		exitUsageError(out,
			"非法参数 --timeout",
			"--timeout 必须大于 0",
			"例如: dalek ticket stop --ticket 1 --timeout 15s",
		)
	}

	p := mustOpenProjectWithOutput(out, *home, *proj)
	type stopItem struct {
		WorkerID  uint   `json:"worker_id"`
		OutputRef string `json:"output_ref"`
	}
	stopped := make([]stopItem, 0, 4)

	if *workerID != 0 {
		ctx1, cancel1 := projectCtx(*timeout)
		w, werr := p.WorkerByID(ctx1, uint(*workerID))
		cancel1()
		if werr != nil {
			exitRuntimeError(out,
				fmt.Sprintf("worker #%d 不存在", *workerID),
				werr.Error(),
				"使用 dalek ticket ls 查看当前 worker 关联状态",
			)
		}
		ctx2, cancel2 := projectCtx(*timeout)
		err := p.StopWorker(ctx2, uint(*workerID))
		cancel2()
		if err != nil {
			exitRuntimeError(out,
				fmt.Sprintf("停止 worker #%d 失败", *workerID),
				err.Error(),
				"确认 worker 正在运行并重试",
			)
		}
		outputRef := "-"
		if w != nil {
			outputRef = outputRefFromRuntime(w.LogPath)
		}
		stopped = append(stopped, stopItem{WorkerID: uint(*workerID), OutputRef: outputRef})
	} else if *ticketID != 0 {
		ctx1, cancel1 := projectCtx(*timeout)
		w, werr := p.LatestWorker(ctx1, uint(*ticketID))
		cancel1()
		if werr != nil {
			exitRuntimeError(out,
				fmt.Sprintf("ticket #%d 没有运行中的 worker", *ticketID),
				werr.Error(),
				fmt.Sprintf("先使用 dalek ticket start --ticket %d 启动", *ticketID),
			)
		}
		if w == nil {
			exitRuntimeError(out,
				fmt.Sprintf("ticket #%d 没有运行中的 worker", *ticketID),
				"ticket 尚未启动或 worker 已停止",
				fmt.Sprintf("先使用 dalek ticket start --ticket %d 启动", *ticketID),
			)
		}
		ctx2, cancel2 := projectCtx(*timeout)
		err := p.StopTicket(ctx2, uint(*ticketID))
		cancel2()
		if err != nil {
			exitRuntimeError(out,
				fmt.Sprintf("停止 ticket #%d 失败", *ticketID),
				err.Error(),
				"确认 ticket 正在运行并重试",
			)
		}
		stopped = append(stopped, stopItem{
			WorkerID:  w.ID,
			OutputRef: outputRefFromRuntime(w.LogPath),
		})
	} else {
		ctx1, cancel1 := projectCtx(*timeout)
		running, err := p.ListRunningWorkers(ctx1)
		cancel1()
		if err != nil {
			exitRuntimeError(out,
				"读取运行中 workers 失败",
				err.Error(),
				"稍后重试，或检查数据库状态",
			)
		}
		for _, w := range running {
			ctx2, cancel2 := projectCtx(*timeout)
			err := p.StopWorker(ctx2, w.ID)
			cancel2()
			if err != nil {
				exitRuntimeError(out,
					fmt.Sprintf("停止 worker #%d 失败", w.ID),
					err.Error(),
					"确认 worker 正在运行并重试",
				)
			}
			stopped = append(stopped, stopItem{
				WorkerID:  w.ID,
				OutputRef: outputRefFromRuntime(w.LogPath),
			})
		}
	}

	if out == outputJSON {
		printJSONOrExit(map[string]any{
			"schema":  "dalek.ticket.stop.v1",
			"stopped": stopped,
			"count":   len(stopped),
		})
		return
	}
	if len(stopped) == 0 {
		fmt.Println("(empty)")
		return
	}
	for _, it := range stopped {
		fmt.Printf("stopped: worker=w%d output=%s\n", it.WorkerID, it.OutputRef)
	}
}

func cmdTicketArchive(args []string) {
	fs := flag.NewFlagSet("ticket archive", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		printSubcommandUsage(
			fs,
			"归档 ticket",
			"dalek ticket archive --ticket <id> [--timeout 5s] [--output text|json]",
			"dalek ticket archive --ticket 1",
			"dalek ticket archive --ticket 1 -o json",
		)
	}
	home := fs.String("home", globalHome, "dalek Home 目录（默认 ~/.dalek）")
	proj := fs.String("project", globalProject, "项目名（可选）")
	projShort := fs.String("p", globalProject, "项目名（可选）")
	ticketID := fs.Uint("ticket", 0, "ticket ID (required)")
	fs.UintVar(ticketID, "t", 0, "ticket ID (required)")
	timeout := fs.Duration("timeout", 5*time.Second, "超时（默认 5s）")
	output := addOutputFlag(fs, "输出格式: text|json（默认 text）")
	parseFlagSetOrExit(fs, args, globalOutput, "ticket archive 参数解析失败", "运行 dalek ticket archive --help 查看参数")
	if strings.TrimSpace(*projShort) != "" {
		*proj = strings.TrimSpace(*projShort)
	}
	out := parseOutputOrExit(*output, true)
	if *ticketID == 0 {
		exitUsageError(out,
			"缺少必填参数 --ticket",
			"ticket archive 需要 ticket ID",
			"dalek ticket archive --ticket 1",
		)
	}
	if *timeout <= 0 {
		exitUsageError(out,
			"非法参数 --timeout",
			"--timeout 必须大于 0",
			"例如: dalek ticket archive --ticket 1 --timeout 5s",
		)
	}

	p := mustOpenProjectWithOutput(out, *home, *proj)
	ctx, cancel := projectCtx(*timeout)
	defer cancel()
	if err := p.ArchiveTicket(ctx, uint(*ticketID)); err != nil {
		exitRuntimeError(out,
			"归档 ticket 失败",
			err.Error(),
			"确认 ticket 存在且当前状态允许归档后重试",
		)
	}
	if out == outputJSON {
		printJSONOrExit(map[string]any{
			"schema":    "dalek.ticket.archive.v1",
			"ticket_id": uint(*ticketID),
			"status":    "archived",
		})
		return
	}
	fmt.Printf("t%d archived\n", *ticketID)
}

func cmdTicketCleanup(args []string) {
	fs := flag.NewFlagSet("ticket cleanup", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		printSubcommandUsage(
			fs,
			"清理 ticket 的 worktree",
			"dalek ticket cleanup --ticket <id> [--dry-run] [--force] [--timeout 15s] [--output text|json]",
			"dalek ticket cleanup --ticket 1 --dry-run",
			"dalek ticket cleanup --ticket 1 --force -o json",
		)
	}
	home := fs.String("home", globalHome, "dalek Home 目录（默认 ~/.dalek）")
	proj := fs.String("project", globalProject, "项目名（可选）")
	projShort := fs.String("p", globalProject, "项目名（可选）")
	ticketID := fs.Uint("ticket", 0, "ticket ID (required)")
	fs.UintVar(ticketID, "t", 0, "ticket ID (required)")
	dryRun := fs.Bool("dry-run", false, "只检查并标记 pending，不实际删除")
	force := fs.Bool("force", false, "允许强制清理（例如 dirty worktree）")
	timeout := fs.Duration("timeout", 15*time.Second, "超时（默认 15s）")
	output := addOutputFlag(fs, "输出格式: text|json（默认 text）")
	parseFlagSetOrExit(fs, args, globalOutput, "ticket cleanup 参数解析失败", "运行 dalek ticket cleanup --help 查看参数")
	if strings.TrimSpace(*projShort) != "" {
		*proj = strings.TrimSpace(*projShort)
	}
	out := parseOutputOrExit(*output, true)
	if *ticketID == 0 {
		exitUsageError(out,
			"缺少必填参数 --ticket",
			"ticket cleanup 需要 ticket ID",
			"dalek ticket cleanup --ticket 1",
		)
	}
	if *timeout <= 0 {
		exitUsageError(out,
			"非法参数 --timeout",
			"--timeout 必须大于 0",
			"例如: dalek ticket cleanup --ticket 1 --timeout 15s",
		)
	}

	p := mustOpenProjectWithOutput(out, *home, *proj)
	ctx, cancel := projectCtx(*timeout)
	defer cancel()
	res, err := p.CleanupTicketWorktree(ctx, uint(*ticketID), app.WorktreeCleanupOptions{
		Force:  *force,
		DryRun: *dryRun,
	})
	if err != nil {
		exitRuntimeError(out,
			"清理 worktree 失败",
			err.Error(),
			"确认 ticket 已归档且无运行中任务后重试；必要时使用 --force",
		)
	}

	if out == outputJSON {
		requestedAt := any(nil)
		cleanedAt := any(nil)
		if res.RequestedAt != nil {
			requestedAt = res.RequestedAt.Local().Format(time.RFC3339)
		}
		if res.CleanedAt != nil {
			cleanedAt = res.CleanedAt.Local().Format(time.RFC3339)
		}
		printJSONOrExit(map[string]any{
			"schema":       "dalek.ticket.cleanup.v1",
			"ticket_id":    res.TicketID,
			"worker_id":    res.WorkerID,
			"worktree":     strings.TrimSpace(res.Worktree),
			"branch":       strings.TrimSpace(res.Branch),
			"dry_run":      res.DryRun,
			"pending":      res.Pending,
			"cleaned":      res.Cleaned,
			"dirty":        res.Dirty,
			"session_live": res.SessionLive,
			"requested_at": requestedAt,
			"cleaned_at":   cleanedAt,
			"message":      strings.TrimSpace(res.Message),
		})
		return
	}

	if res.Cleaned {
		fmt.Printf("cleaned: t%d worker=w%d worktree=%s\n", res.TicketID, res.WorkerID, strings.TrimSpace(res.Worktree))
	} else if res.DryRun {
		fmt.Printf("dry-run: t%d worker=w%d pending=true worktree=%s\n", res.TicketID, res.WorkerID, strings.TrimSpace(res.Worktree))
	} else {
		fmt.Printf("cleanup: t%d worker=w%d pending=%v cleaned=%v\n", res.TicketID, res.WorkerID, res.Pending, res.Cleaned)
	}
	if strings.TrimSpace(res.Message) != "" {
		fmt.Printf("note: %s\n", strings.TrimSpace(res.Message))
	}
}

func cmdTicketEvents(args []string) {
	fs := flag.NewFlagSet("ticket events", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		printSubcommandUsage(
			fs,
			"查看 ticket/worker 事件时间线",
			"dalek ticket events (--ticket <id> | --worker <id>) [--limit 50] [--timeout 3s] [--output text|json]",
			"dalek ticket events --ticket 1",
			"dalek ticket events --worker 2 -n 100 -o json",
		)
	}
	home := fs.String("home", globalHome, "dalek Home 目录（默认 ~/.dalek）")
	proj := fs.String("project", globalProject, "项目名（可选）")
	projShort := fs.String("p", globalProject, "项目名（可选）")
	ticketID := fs.Uint("ticket", 0, "ticket ID")
	fs.UintVar(ticketID, "t", 0, "ticket ID")
	workerID := fs.Uint("worker", 0, "worker ID")
	fs.UintVar(workerID, "w", 0, "worker ID")
	limit := fs.Int("limit", 50, "最多返回条数（默认 50）")
	fs.IntVar(limit, "n", 50, "最多返回条数（默认 50）")
	timeout := fs.Duration("timeout", 3*time.Second, "超时（默认 3s）")
	output := addOutputFlag(fs, "输出格式: text|json（默认 text）")
	parseFlagSetOrExit(fs, args, globalOutput, "ticket events 参数解析失败", "运行 dalek ticket events --help 查看参数")
	if strings.TrimSpace(*projShort) != "" {
		*proj = strings.TrimSpace(*projShort)
	}
	out := parseOutputOrExit(*output, true)
	if *ticketID == 0 && *workerID == 0 {
		exitUsageError(out,
			"缺少目标参数",
			"ticket events 至少需要 --ticket 或 --worker 之一",
			"dalek ticket events --ticket 1",
		)
	}
	if *limit <= 0 {
		exitUsageError(out,
			"非法参数 --limit",
			"--limit 必须大于 0",
			"例如: dalek ticket events --ticket 1 --limit 50",
		)
	}
	if *timeout <= 0 {
		exitUsageError(out,
			"非法参数 --timeout",
			"--timeout 必须大于 0",
			"例如: dalek ticket events --ticket 1 --timeout 3s",
		)
	}

	p := mustOpenProjectWithOutput(out, *home, *proj)
	ctx, cancel := projectCtx(*timeout)
	defer cancel()
	events, err := p.ListTaskEventsByScope(ctx, uint(*ticketID), uint(*workerID), *limit)
	if err != nil {
		exitRuntimeError(out,
			"查询事件时间线失败",
			err.Error(),
			"稍后重试，或检查项目数据库状态",
		)
	}
	sort.Slice(events, func(i, j int) bool { return events[i].CreatedAt.Before(events[j].CreatedAt) })

	if out == outputJSON {
		type eventJSON struct {
			ID        uint   `json:"id"`
			CreatedAt string `json:"created_at"`
			RunID     uint   `json:"run_id"`
			Type      string `json:"type"`
			Note      string `json:"note"`
			FromState any    `json:"from_state"`
			ToState   any    `json:"to_state"`
			Payload   any    `json:"payload"`
		}
		arr := make([]eventJSON, 0, len(events))
		for _, ev := range events {
			arr = append(arr, eventJSON{
				ID:        ev.ID,
				CreatedAt: ev.CreatedAt.Local().Format(time.RFC3339),
				RunID:     ev.TaskRunID,
				Type:      strings.TrimSpace(ev.EventType),
				Note:      strings.TrimSpace(ev.Note),
				FromState: parseRawJSONOrMap(strings.TrimSpace(ev.FromStateJSON)),
				ToState:   parseRawJSONOrMap(strings.TrimSpace(ev.ToStateJSON)),
				Payload:   parseRawJSONOrMap(strings.TrimSpace(ev.PayloadJSON)),
			})
		}
		printJSONOrExit(map[string]any{
			"schema": "dalek.ticket.events.v1",
			"events": arr,
		})
		return
	}

	if len(events) == 0 {
		fmt.Println("(empty)")
		return
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "TIME\tRUN\tTYPE\tNOTE")
	for _, ev := range events {
		note := strings.TrimSpace(ev.Note)
		if note == "" {
			note = "-"
		}
		fmt.Fprintf(tw, "%s\t%d\t%s\t%s\n",
			ev.CreatedAt.Local().Format("2006-01-02 15:04:05"),
			ev.TaskRunID,
			strings.TrimSpace(ev.EventType),
			trimOneLine(note),
		)
	}
	_ = tw.Flush()
}

func parseRawJSONOrMap(raw string) any {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return map[string]any{}
	}
	var out any
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return map[string]any{}
	}
	if out == nil {
		return map[string]any{}
	}
	return out
}
