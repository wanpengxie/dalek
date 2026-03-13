package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"dalek/internal/app"
)

type taskStatusPublic struct {
	RunID      uint    `json:"run_id"`
	Owner      string  `json:"owner"`
	Type       string  `json:"type"`
	ProjectKey string  `json:"project_key"`
	TicketID   uint    `json:"ticket_id"`
	WorkerID   uint    `json:"worker_id"`
	Subject    string  `json:"subject"`
	RunStatus  string  `json:"run_status"`
	NextAction string  `json:"next_action,omitempty"`
	NeedsUser  bool    `json:"needs_user"`
	Summary    string  `json:"summary"`
	ErrorCode  string  `json:"error_code,omitempty"`
	ErrorMsg   string  `json:"error_message,omitempty"`
	StartedAt  *string `json:"started_at,omitempty"`
	FinishedAt *string `json:"finished_at,omitempty"`
	UpdatedAt  string  `json:"updated_at"`
}

func cmdTask(args []string) {
	if len(args) == 0 {
		printTaskUsage()
		os.Exit(2)
	}
	sub := strings.TrimSpace(args[0])
	switch sub {
	case "ls", "list":
		cmdTaskList(args[1:])
	case "show":
		cmdTaskShow(args[1:])
	case "events":
		cmdTaskEvents(args[1:])
	case "cancel":
		cmdTaskCancel(args[1:])
	case "help", "-h", "--help":
		printTaskUsage()
		os.Exit(0)
	default:
		exitUsageError(globalOutput,
			fmt.Sprintf("未知 task 子命令: %s", sub),
			"task 命令组仅支持固定子命令",
			"运行 dalek task --help 查看可用命令",
		)
	}
}

func cmdTaskList(args []string) {
	fs := flag.NewFlagSet("task ls", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		printSubcommandUsage(
			fs,
			"列出任务运行态",
			"dalek task ls [--owner worker|pm|subagent] [--type TYPE] [--ticket N] [--worker N] [--all] [--limit 50] [--output text|json]",
			"dalek task ls",
			"dalek task ls --ticket 1 -o json",
		)
	}
	home := fs.String("home", globalHome, "dalek Home 目录")
	proj := fs.String("project", globalProject, "项目名（可选）")
	projShort := fs.String("p", globalProject, "项目名（可选）")
	owner := fs.String("owner", "", "owner 过滤：worker|pm|subagent")
	taskType := fs.String("type", "", "task_type 过滤")
	ticketID := fs.Uint("ticket", 0, "ticket id 过滤")
	workerID := fs.Uint("worker", 0, "worker id 过滤")
	all := fs.Bool("all", false, "包含终态（succeeded/failed/canceled）")
	limit := fs.Int("limit", 50, "最多返回条数（按时间升序展示）")
	timeout := fs.Duration("timeout", 5*time.Second, "超时")
	output := addOutputFlag(fs, "输出格式: text|json（默认 text）")
	parseFlagSetOrExit(fs, args, globalOutput, "task ls 参数解析失败", "运行 dalek task ls --help 查看参数")
	if strings.TrimSpace(*projShort) != "" {
		*proj = strings.TrimSpace(*projShort)
	}
	out := parseOutputOrExit(*output, true)
	if err := requirePositiveInt("limit", *limit); err != nil {
		exitUsageError(out, "参数错误", err.Error(), "调整 --limit 为正整数")
	}
	if err := requirePositiveDuration("timeout", *timeout); err != nil {
		exitUsageError(out, "参数错误", err.Error(), "调整 --timeout 为正时长")
	}

	p := mustOpenProjectWithOutput(out, *home, *proj)
	ownerType, err := app.ParseTaskOwnerType(*owner)
	if err != nil {
		exitUsageError(out, "参数错误", err.Error(), "将 --owner 设为 worker、pm 或 subagent")
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	items, err := p.ListTaskStatus(ctx, app.ListTaskOptions{
		OwnerType:       ownerType,
		TaskType:        strings.TrimSpace(*taskType),
		TicketID:        uint(*ticketID),
		WorkerID:        uint(*workerID),
		IncludeTerminal: *all,
		Limit:           *limit,
	})
	if err != nil {
		exitRuntimeError(out,
			"task ls 失败",
			err.Error(),
			"稍后重试，或检查数据库状态",
		)
	}
	publicItems := make([]taskStatusPublic, 0, len(items))
	for _, it := range items {
		publicItems = append(publicItems, mapTaskStatusPublic(it))
	}

	if out == outputJSON {
		printJSONOrExit(map[string]any{
			"schema": "dalek.task.list.v2",
			"tasks":  publicItems,
		})
		return
	}

	if len(publicItems) == 0 {
		fmt.Println("(no task runs)")
		return
	}
	fmt.Println("run\towner\ttype\tstatus\tnext\tticket\tworker\tupdated\tsummary")
	for _, it := range publicItems {
		fmt.Printf("%d\t%s\t%s\t%s\t%s\tt%d\tw%d\t%s\t%s\n",
			it.RunID,
			trimField(it.Owner, 8),
			trimField(it.Type, 20),
			trimField(it.RunStatus, 12),
			trimField(it.NextAction, 10),
			it.TicketID,
			it.WorkerID,
			trimField(it.UpdatedAt, 20),
			trimField(it.Summary, 80),
		)
	}
}

func cmdTaskShow(args []string) {
	fs := flag.NewFlagSet("task show", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		printSubcommandUsage(
			fs,
			"查看单个 task run 详情",
			"dalek task show --id <id> [--output text|json]",
			"dalek task show --id 1",
			"dalek task show --id 1 -o json",
		)
	}
	home := fs.String("home", globalHome, "dalek Home 目录")
	proj := fs.String("project", globalProject, "项目名（可选）")
	projShort := fs.String("p", globalProject, "项目名（可选）")
	runID := fs.Uint("id", 0, "task run id（必填）")
	timeout := fs.Duration("timeout", 5*time.Second, "超时")
	output := addOutputFlag(fs, "输出格式: text|json（默认 text）")
	parseFlagSetOrExit(fs, normalizeTaskRunIDArgs(args), globalOutput, "task show 参数解析失败", "运行 dalek task show --help 查看参数")
	if strings.TrimSpace(*projShort) != "" {
		*proj = strings.TrimSpace(*projShort)
	}
	out := parseOutputOrExit(*output, true)
	if err := requirePositiveDuration("timeout", *timeout); err != nil {
		exitUsageError(out, "参数错误", err.Error(), "调整 --timeout 为正时长")
	}
	if *runID == 0 {
		exitUsageError(out,
			"缺少必填参数 --id",
			"task show 需要 task run id",
			"dalek task show --id 1",
		)
	}

	p := mustOpenProjectWithOutput(out, *home, *proj)
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	it, err := p.GetTaskStatus(ctx, uint(*runID))
	if err != nil {
		exitRuntimeError(out, "task show 失败", err.Error(), "稍后重试，或检查数据库状态")
	}
	if it == nil {
		exitRuntimeError(out,
			fmt.Sprintf("task run #%d 不存在", *runID),
			"指定 run id 在当前项目中未找到",
			"先运行 dalek task ls 查看可用 run id",
		)
	}
	publicItem := mapTaskStatusPublic(*it)

	if out == outputJSON {
		printJSONOrExit(map[string]any{
			"schema": "dalek.task.show.v2",
			"task":   publicItem,
		})
		return
	}

	fmt.Printf("run_id: %d\n", publicItem.RunID)
	fmt.Printf("owner/type: %s / %s\n", publicItem.Owner, publicItem.Type)
	fmt.Printf("project: %s\n", publicItem.ProjectKey)
	fmt.Printf("ticket/worker: t%d / w%d\n", publicItem.TicketID, publicItem.WorkerID)
	fmt.Printf("subject: %s\n", publicItem.Subject)
	fmt.Printf("run_status: %s\n", emptyAsDash(publicItem.RunStatus))
	fmt.Printf("next_action: %s\n", emptyAsDash(publicItem.NextAction))
	fmt.Printf("needs_user: %v\n", publicItem.NeedsUser)
	fmt.Printf("last_event: %s at=%s\n", emptyAsDash(it.LastEventType), formatTimePtr(it.LastEventAt))
	fmt.Printf("error: code=%s msg=%s\n", emptyAsDash(publicItem.ErrorCode), emptyAsDash(publicItem.ErrorMsg))
	fmt.Printf("started=%s finished=%s updated=%s\n", formatTimePtr(it.StartedAt), formatTimePtr(it.FinishedAt), publicItem.UpdatedAt)
	fmt.Printf("summary: %s\n", emptyAsDash(strings.TrimSpace(publicItem.Summary)))
	fmt.Printf("last_event_note: %s\n", emptyAsDash(strings.TrimSpace(it.LastEventNote)))
}

func cmdTaskEvents(args []string) {
	fs := flag.NewFlagSet("task events", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		printSubcommandUsage(
			fs,
			"查看 task run 事件",
			"dalek task events --id <id> [--limit 50] [--output text|json]",
			"dalek task events --id 1",
			"dalek task events --id 1 --limit 100 -o json",
		)
	}
	home := fs.String("home", globalHome, "dalek Home 目录")
	proj := fs.String("project", globalProject, "项目名（可选）")
	projShort := fs.String("p", globalProject, "项目名（可选）")
	runID := fs.Uint("id", 0, "task run id（必填）")
	limit := fs.Int("limit", 50, "条数")
	timeout := fs.Duration("timeout", 5*time.Second, "超时")
	output := addOutputFlag(fs, "输出格式: text|json（默认 text）")
	parseFlagSetOrExit(fs, normalizeTaskRunIDArgs(args), globalOutput, "task events 参数解析失败", "运行 dalek task events --help 查看参数")
	if strings.TrimSpace(*projShort) != "" {
		*proj = strings.TrimSpace(*projShort)
	}
	out := parseOutputOrExit(*output, true)
	if err := requirePositiveInt("limit", *limit); err != nil {
		exitUsageError(out, "参数错误", err.Error(), "调整 --limit 为正整数")
	}
	if err := requirePositiveDuration("timeout", *timeout); err != nil {
		exitUsageError(out, "参数错误", err.Error(), "调整 --timeout 为正时长")
	}
	if *runID == 0 {
		exitUsageError(out,
			"缺少必填参数 --id",
			"task events 需要 task run id",
			"dalek task events --id 1",
		)
	}

	p := mustOpenProjectWithOutput(out, *home, *proj)
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	events, err := p.ListTaskEvents(ctx, uint(*runID), *limit)
	if err != nil {
		exitRuntimeError(out, "task events 失败", err.Error(), "稍后重试，或检查数据库状态")
	}

	if out == outputJSON {
		type evJSON struct {
			ID        uint   `json:"id"`
			RunID     uint   `json:"run_id"`
			CreatedAt string `json:"created_at"`
			Type      string `json:"type"`
			Note      string `json:"note"`
			FromState any    `json:"from_state"`
			ToState   any    `json:"to_state"`
			Payload   any    `json:"payload"`
		}
		arr := make([]evJSON, 0, len(events))
		for _, ev := range events {
			arr = append(arr, evJSON{
				ID:        ev.ID,
				RunID:     ev.TaskRunID,
				CreatedAt: ev.CreatedAt.Local().Format(time.RFC3339),
				Type:      strings.TrimSpace(ev.EventType),
				Note:      strings.TrimSpace(ev.Note),
				FromState: parseRawJSONOrMap(strings.TrimSpace(ev.FromStateJSON)),
				ToState:   parseRawJSONOrMap(strings.TrimSpace(ev.ToStateJSON)),
				Payload:   parseRawJSONOrMap(strings.TrimSpace(ev.PayloadJSON)),
			})
		}
		printJSONOrExit(map[string]any{
			"schema": "dalek.task.events.v1",
			"events": arr,
		})
		return
	}

	if len(events) == 0 {
		fmt.Println("(no events)")
		return
	}
	for _, ev := range events {
		fmt.Printf("%s  #%d  %s\n", ev.CreatedAt.Local().Format("2006-01-02 15:04:05"), ev.ID, emptyAsDash(strings.TrimSpace(ev.EventType)))
		if strings.TrimSpace(ev.Note) != "" {
			fmt.Printf("  note: %s\n", strings.TrimSpace(ev.Note))
		}
		if strings.TrimSpace(ev.FromStateJSON) != "" {
			fmt.Printf("  from: %s\n", strings.TrimSpace(ev.FromStateJSON))
		}
		if strings.TrimSpace(ev.ToStateJSON) != "" {
			fmt.Printf("  to:   %s\n", strings.TrimSpace(ev.ToStateJSON))
		}
		if strings.TrimSpace(ev.PayloadJSON) != "" {
			fmt.Printf("  payload: %s\n", strings.TrimSpace(ev.PayloadJSON))
		}
	}
}

func cmdTaskCancel(args []string) {
	fs := flag.NewFlagSet("task cancel", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		printSubcommandUsage(
			fs,
			"取消运行中的 task run",
			"dalek task cancel --id <id> [--output text|json]",
			"dalek task cancel --id 1",
			"dalek task cancel --id 1 -o json",
		)
	}
	home := fs.String("home", globalHome, "dalek Home 目录")
	proj := fs.String("project", globalProject, "项目名（可选）")
	projShort := fs.String("p", globalProject, "项目名（可选）")
	runID := fs.Uint("id", 0, "task run id（必填）")
	timeout := fs.Duration("timeout", 5*time.Second, "超时")
	output := addOutputFlag(fs, "输出格式: text|json（默认 text）")
	parseFlagSetOrExit(fs, normalizeTaskRunIDArgs(args), globalOutput, "task cancel 参数解析失败", "运行 dalek task cancel --help 查看参数")
	if strings.TrimSpace(*projShort) != "" {
		*proj = strings.TrimSpace(*projShort)
	}
	out := parseOutputOrExit(*output, true)
	if err := requirePositiveDuration("timeout", *timeout); err != nil {
		exitUsageError(out, "参数错误", err.Error(), "调整 --timeout 为正时长")
	}
	if *runID == 0 {
		exitUsageError(out,
			"缺少必填参数 --id",
			"task cancel 需要 task run id",
			"dalek task cancel --id 1",
		)
	}

	p := mustOpenProjectWithOutput(out, *home, *proj)
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	taskStatus, statusErr := p.GetTaskStatus(ctx, uint(*runID))

	daemonWarning := ""
	if _, daemonClient, derr := openDaemonClient(*home); derr != nil {
		daemonWarning = fmt.Sprintf(
			"daemon cancel 调用不可用，已降级为仅标记数据库；若 daemon 仍在执行，任务可能继续运行（%s）",
			strings.TrimSpace(derr.Error()),
		)
	} else if statusErr != nil {
		daemonWarning = fmt.Sprintf("读取 task 状态失败，已降级为仅标记数据库（%s）", strings.TrimSpace(statusErr.Error()))
	} else if taskStatus != nil && strings.TrimSpace(strings.ToLower(taskStatus.OwnerType)) == "worker" && taskStatus.TicketID != 0 {
		cancelRes, cerr := daemonClient.CancelTicketLoop(ctx, strings.TrimSpace(p.Name()), taskStatus.TicketID)
		if cerr != nil {
			daemonWarning = fmt.Sprintf(
				"daemon ticket-loop cancel 调用失败，已降级为仅标记数据库；若 daemon 仍在执行，任务可能继续运行（%s）",
				strings.TrimSpace(cerr.Error()),
			)
		} else if !cancelRes.Canceled {
			reason := strings.TrimSpace(cancelRes.Reason)
			if reason == "" {
				reason = "ticket loop 不在当前 daemon 执行上下文中"
			}
			daemonWarning = fmt.Sprintf("daemon 未确认 ticket loop 取消信号（%s），已降级为仅标记数据库", reason)
		}
	} else {
		cancelRes, cerr := daemonClient.CancelTaskRun(ctx, uint(*runID))
		if cerr != nil {
			daemonWarning = fmt.Sprintf("daemon task-run cancel 调用失败，已降级为仅标记数据库；若 daemon 仍在执行，任务可能继续运行（%s）", strings.TrimSpace(cerr.Error()))
		} else if !cancelRes.Canceled {
			reason := strings.TrimSpace(cancelRes.Reason)
			if reason == "" {
				reason = "task run 不在当前 daemon 执行上下文中"
			}
			daemonWarning = fmt.Sprintf("daemon 未确认 task run 取消信号（%s），已降级为仅标记数据库", reason)
		}
	}

	result, err := p.CancelTaskRun(ctx, uint(*runID))
	if err != nil {
		cause := strings.TrimSpace(err.Error())
		if daemonWarning != "" {
			cause = fmt.Sprintf("%s；%s", cause, daemonWarning)
		}
		exitRuntimeError(out, "task cancel 失败", cause, "稍后重试，或检查数据库状态")
	}
	if !result.Found {
		exitRuntimeError(out,
			fmt.Sprintf("task run #%d 不存在", *runID),
			"指定 run id 在当前项目中未找到",
			"先运行 dalek task ls 查看可用 run id",
		)
	}
	if !result.Canceled {
		exitRuntimeError(out,
			fmt.Sprintf("task run #%d 无法取消", *runID),
			emptyAsDash(strings.TrimSpace(result.Reason)),
			fmt.Sprintf("使用 dalek task show --id %d 查看最新状态", *runID),
		)
	}

	if out == outputJSON {
		payload := map[string]any{
			"schema":     "dalek.task.cancel.v1",
			"run_id":     result.RunID,
			"found":      result.Found,
			"canceled":   result.Canceled,
			"reason":     strings.TrimSpace(result.Reason),
			"from_state": strings.TrimSpace(result.FromState),
			"to_state":   strings.TrimSpace(result.ToState),
		}
		if daemonWarning != "" {
			payload["warning"] = daemonWarning
		}
		printJSONOrExit(payload)
		return
	}

	if daemonWarning != "" {
		fmt.Printf("warning: %s\n", daemonWarning)
	}
	fmt.Printf("task run #%d 已取消\n", result.RunID)
	fmt.Printf("show:   dalek task show --id %d\n", result.RunID)
	fmt.Printf("events: dalek task events --id %d\n", result.RunID)
}

func printTaskUsage() {
	printGroupUsage("任务运行时观测", "dalek task <command> [flags]", []string{
		"ls        列出 task runs",
		"show      查看 task run 详情",
		"events    查看 task run 事件",
		"cancel    取消 task run",
	})
	fmt.Fprintln(os.Stderr, "Use \"dalek task <command> --help\" for more information.")
}

func formatTimePtr(t *time.Time) string {
	if t == nil || t.IsZero() {
		return "-"
	}
	return t.Local().Format(time.RFC3339)
}

func emptyAsDash(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "-"
	}
	return s
}

func trimField(s string, max int) string {
	s = strings.TrimSpace(s)
	runes := []rune(s)
	if max <= 0 || len(runes) <= max {
		return s
	}
	if max <= 3 {
		return string(runes[:max])
	}
	return string(runes[:max-3]) + "..."
}

func mapTaskStatusPublic(it app.TaskStatus) taskStatusPublic {
	subject := strings.TrimSpace(it.SubjectType)
	if sid := strings.TrimSpace(it.SubjectID); sid != "" {
		if subject != "" {
			subject = subject + ":" + sid
		} else {
			subject = sid
		}
	}
	if subject == "" {
		subject = "-"
	}
	summary := strings.TrimSpace(it.SemanticSummary)
	if summary == "" {
		summary = strings.TrimSpace(it.RuntimeSummary)
	}
	runStatus := app.DeriveRunStatus(it.OrchestrationState, it.RuntimeHealthState, it.RuntimeNeedsUser)
	startedAt := formatPtrRFC3339(it.StartedAt)
	finishedAt := formatPtrRFC3339(it.FinishedAt)
	return taskStatusPublic{
		RunID:      it.RunID,
		Owner:      strings.TrimSpace(it.OwnerType),
		Type:       strings.TrimSpace(it.TaskType),
		ProjectKey: strings.TrimSpace(it.ProjectKey),
		TicketID:   it.TicketID,
		WorkerID:   it.WorkerID,
		Subject:    subject,
		RunStatus:  runStatus,
		NextAction: strings.TrimSpace(it.SemanticNextAction),
		NeedsUser:  it.RuntimeNeedsUser,
		Summary:    summary,
		ErrorCode:  strings.TrimSpace(it.ErrorCode),
		ErrorMsg:   strings.TrimSpace(it.ErrorMessage),
		StartedAt:  startedAt,
		FinishedAt: finishedAt,
		UpdatedAt:  app.TaskStatusUpdatedAt(it).Local().Format(time.RFC3339),
	}
}

func formatPtrRFC3339(v *time.Time) *string {
	if v == nil || v.IsZero() {
		return nil
	}
	s := v.Local().Format(time.RFC3339)
	return &s
}

func normalizeTaskRunIDArgs(args []string) []string {
	if len(args) == 0 {
		return nil
	}
	out := make([]string, len(args))
	copy(out, args)
	for i, arg := range out {
		if arg == "--run" {
			out[i] = "--id"
			continue
		}
		if strings.HasPrefix(arg, "--run=") {
			out[i] = "--id=" + strings.TrimPrefix(arg, "--run=")
		}
	}
	return out
}

func requirePositiveDuration(name string, v time.Duration) error {
	if v <= 0 {
		return fmt.Errorf("%s 必须 > 0", strings.TrimSpace(name))
	}
	return nil
}

func requirePositiveInt(name string, v int) error {
	if v <= 0 {
		return fmt.Errorf("%s 必须 > 0", strings.TrimSpace(name))
	}
	return nil
}
