package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"dalek/internal/app"

	"gorm.io/gorm"
)

const focusTailPollInterval = 1500 * time.Millisecond

func cmdManagerRunBatch(out cliOutputFormat, home, proj, ticketsFlag string, budget int) {
	p := mustOpenProjectWithOutput(out, home, proj)
	_, daemonClient := mustOpenDaemonClient(out, home)
	ticketIDs := parseManagerFocusTicketIDs(out, ticketsFlag)

	result, err := daemonClient.FocusStart(context.Background(), app.DaemonFocusStartRequest{
		Project: p.Name(),
		FocusStartInput: app.FocusStartInput{
			Mode:           "batch",
			ScopeTicketIDs: ticketIDs,
			AgentBudget:    budget,
		},
	})
	if err != nil {
		exitManagerFocusDaemonError(out, "启动 focus", err)
	}
	if out == outputJSON {
		printJSONOrExit(result)
		return
	}
	fmt.Printf("focus batch 已提交到 daemon: id=%d scope=%v budget=%d\n", result.FocusID, ticketIDs, budget)
	tailFocusRun(out, daemonClient, p.Name(), result.FocusID)
}

func cmdManagerAdd(args []string) {
	fs := flag.NewFlagSet("manager add", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		printSubcommandUsage(
			fs,
			"向当前 active focus 热插入 tickets",
			"dalek manager add --tickets 29,30 [--output text|json]",
			"dalek manager add --tickets 29,30",
			"dalek manager add --tickets 29,30 -o json",
		)
	}
	home := fs.String("home", globalHome, "dalek Home 目录（默认 ~/.dalek）")
	proj := fs.String("project", globalProject, "项目名（可选）")
	projShort := fs.String("p", globalProject, "项目名（可选）")
	tickets := fs.String("tickets", "", "要追加的 ticket IDs，逗号分隔: 29,30")
	output := addOutputFlag(fs, "输出格式: text|json（默认 text）")
	parseFlagSetOrExit(fs, args, globalOutput, "manager add 参数解析失败", "运行 dalek manager add --help 查看参数")
	if strings.TrimSpace(*projShort) != "" {
		*proj = strings.TrimSpace(*projShort)
	}
	out := parseOutputOrExit(*output, true)

	p := mustOpenProjectWithOutput(out, *home, *proj)
	_, daemonClient := mustOpenDaemonClient(out, *home)
	ticketIDs := parseManagerFocusTicketIDs(out, *tickets)

	result, err := daemonClient.FocusAddTickets(context.Background(), app.DaemonFocusAddTicketsRequest{
		Project:   p.Name(),
		TicketIDs: ticketIDs,
	})
	if err != nil {
		exitManagerFocusDaemonError(out, "添加 tickets 到 focus", err)
	}
	if out == outputJSON {
		printJSONOrExit(result)
		return
	}
	fmt.Printf("focus add 完成: focus_id=%d added=%d skipped=%d added_ids=%v skipped_ids=%v\n",
		result.FocusID, result.AddedCount, result.SkippedCount, result.AddedIDs, result.SkippedIDs)
}

func cmdManagerShow(args []string) {
	fs := flag.NewFlagSet("manager show", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	home := fs.String("home", globalHome, "dalek Home 目录")
	proj := fs.String("project", globalProject, "项目名")
	output := addOutputFlag(fs, "输出格式: text|json")
	parseFlagSetOrExit(fs, args, globalOutput, "manager show 参数解析失败", "运行 dalek manager show --help")
	out := parseOutputOrExit(*output, true)

	p := mustOpenProjectWithOutput(out, *home, *proj)
	if _, daemonClient, err := openDaemonClient(*home); err == nil {
		view, err := daemonClient.FocusGetCurrent(context.Background(), p.Name())
		switch {
		case err == nil:
			printFocusView(out, view, false)
			return
		case errors.Is(err, gorm.ErrRecordNotFound):
			fmt.Println("当前无 active focus (idle)")
			return
		case !app.IsDaemonUnavailable(err):
			exitRuntimeError(out, "查询 focus 失败", err.Error(), "检查 daemon 与 focus 状态")
		}
	}

	view, err := p.FocusGet(context.Background(), 0)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			fmt.Println("当前无 active focus (idle)")
			return
		}
		exitRuntimeError(out, "查询 focus 失败", err.Error(), "检查 DB 状态")
	}
	printFocusView(out, view, true)
}

func cmdManagerStop(args []string) {
	fs := flag.NewFlagSet("manager stop", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	home := fs.String("home", globalHome, "dalek Home 目录")
	proj := fs.String("project", globalProject, "项目名")
	focusID := fs.Uint("focus-id", 0, "focus ID（可选；默认查询当前 active focus）")
	force := fs.Bool("force", false, "强制取消当前 focus（写入 desired_state=canceling）")
	reason := fs.String("reason", "", "兼容保留，不进入 focus 控制面")
	parseFlagSetOrExit(fs, args, globalOutput, "manager stop 参数解析失败", "运行 dalek manager stop --help")

	p := mustOpenProjectWithOutput(globalOutput, *home, *proj)
	_, daemonClient := mustOpenDaemonClient(globalOutput, *home)
	resolvedFocusID := *focusID
	if resolvedFocusID == 0 {
		view, err := daemonClient.FocusGetCurrent(context.Background(), p.Name())
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				exitRuntimeError(globalOutput, "停止 focus 失败", "当前无 active focus", "先执行 dalek manager run --mode batch")
			}
			exitManagerFocusDaemonError(globalOutput, "查询 active focus", err)
		}
		resolvedFocusID = view.Run.ID
	}
	requestID := fmt.Sprintf("cli_focus_%d", time.Now().UnixNano())
	if strings.TrimSpace(*reason) != "" {
		requestID = fmt.Sprintf("%s_%d", sanitizeRequestToken(*reason), time.Now().UnixNano())
	}
	if *force {
		if err := daemonClient.FocusCancel(context.Background(), app.DaemonFocusCancelRequest{
			Project:   p.Name(),
			FocusID:   resolvedFocusID,
			RequestID: requestID,
		}); err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				exitRuntimeError(globalOutput, "强制取消 focus 失败", "focus 不存在", "检查 focus-id 是否正确")
			}
			exitManagerFocusDaemonError(globalOutput, "强制取消 focus", err)
		}
		fmt.Printf("focus 已请求强制取消: id=%d request_id=%s\n", resolvedFocusID, requestID)
		return
	}
	if err := daemonClient.FocusStop(context.Background(), app.DaemonFocusStopRequest{
		Project:   p.Name(),
		FocusID:   resolvedFocusID,
		RequestID: requestID,
	}); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			exitRuntimeError(globalOutput, "停止 focus 失败", "focus 不存在", "检查 focus-id 是否正确")
		}
		exitManagerFocusDaemonError(globalOutput, "停止 focus", err)
	}
	fmt.Printf("focus 已请求停止: id=%d request_id=%s\n", resolvedFocusID, requestID)
}

func cmdManagerTail(args []string) {
	fs := flag.NewFlagSet("manager tail", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	home := fs.String("home", globalHome, "dalek Home 目录")
	proj := fs.String("project", globalProject, "项目名")
	focusID := fs.Uint("focus-id", 0, "focus ID（可选；默认跟随当前 active focus）")
	parseFlagSetOrExit(fs, args, globalOutput, "manager tail 参数解析失败", "运行 dalek manager tail --help")

	p := mustOpenProjectWithOutput(globalOutput, *home, *proj)
	_, daemonClient := mustOpenDaemonClient(globalOutput, *home)
	resolvedFocusID := *focusID
	if resolvedFocusID == 0 {
		view, err := daemonClient.FocusGetCurrent(context.Background(), p.Name())
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				exitRuntimeError(globalOutput, "tail focus 失败", "当前无 active focus", "先执行 dalek manager run --mode batch")
			}
			exitManagerFocusDaemonError(globalOutput, "查询 active focus", err)
		}
		resolvedFocusID = view.Run.ID
	}
	tailFocusRun(globalOutput, daemonClient, p.Name(), resolvedFocusID)
}

func tailFocusRun(out cliOutputFormat, daemonClient *app.DaemonAPIClient, project string, focusID uint) {
	project = strings.TrimSpace(project)
	if daemonClient == nil || project == "" || focusID == 0 {
		return
	}
	sinceEventID := uint(0)
	lastStatusLine := ""
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sig)
	ticker := time.NewTicker(focusTailPollInterval)
	defer ticker.Stop()

	for {
		outcome, err := daemonClient.FocusPoll(context.Background(), project, focusID, sinceEventID)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				exitRuntimeError(out, "tail focus 失败", "focus 不存在", "检查 focus-id 是否正确")
			}
			exitManagerFocusDaemonError(out, "轮询 focus", err)
		}
		for _, event := range outcome.Events {
			sinceEventID = event.ID
			fmt.Printf("[%d] %s  %s\n", event.ID, strings.TrimSpace(event.Kind), strings.TrimSpace(event.Summary))
		}
		statusLine := renderFocusStatusLine(outcome.View)
		if statusLine != "" && statusLine != lastStatusLine {
			fmt.Println(statusLine)
			lastStatusLine = statusLine
		}
		if outcome.View.Run.IsTerminal() {
			return
		}
		select {
		case <-ticker.C:
		case <-sig:
			fmt.Fprintln(os.Stderr, "\n退出 tail，focus 将继续由 daemon 推进。")
			return
		}
	}
}

func parseManagerFocusTicketIDs(out cliOutputFormat, ticketsFlag string) []uint {
	var ticketIDs []uint
	if strings.TrimSpace(ticketsFlag) != "" {
		for _, s := range strings.Split(ticketsFlag, ",") {
			s = strings.TrimSpace(s)
			if s == "" {
				continue
			}
			var id uint
			if _, err := fmt.Sscanf(s, "%d", &id); err != nil {
				exitUsageError(out, fmt.Sprintf("无效 ticket ID: %s", s), "ticket ID 必须是正整数", "示例: --tickets 1,2,3")
			}
			ticketIDs = append(ticketIDs, id)
		}
	}
	if len(ticketIDs) == 0 {
		exitUsageError(out, "请通过 --tickets 指定 ticket IDs", "示例: --tickets 1,2,3", "dalek manager run --mode batch --tickets 1,2,3")
	}
	return ticketIDs
}

func renderFocusStatusLine(view app.FocusRunView) string {
	completed := 0
	for _, item := range view.Items {
		switch item.Status {
		case "completed":
			completed++
		}
	}
	line := fmt.Sprintf(
		"focus_id=%d  mode=%s  status=%s  desired=%s  progress=%d/%d  budget=%d/%d",
		view.Run.ID,
		strings.TrimSpace(view.Run.Mode),
		strings.TrimSpace(view.Run.Status),
		strings.TrimSpace(view.Run.DesiredState),
		completed,
		len(view.Items),
		view.Run.AgentBudget,
		view.Run.AgentBudgetMax,
	)
	if strings.TrimSpace(view.Run.Summary) != "" {
		line += "  summary=" + strings.TrimSpace(view.Run.Summary)
	}
	return line
}

func printFocusView(out cliOutputFormat, view app.FocusRunView, readonlyStale bool) {
	if readonlyStale {
		view.ReadonlyStale = true
	}
	if out == outputJSON {
		printJSONOrExit(map[string]any{
			"readonly_stale": readonlyStale,
			"focus":          view,
		})
		return
	}
	prefix := ""
	if readonlyStale {
		prefix = "[readonly-stale] "
	}
	fmt.Print(prefix)
	fmt.Println(renderFocusStatusLine(view))
}

func exitManagerFocusDaemonError(out cliOutputFormat, action string, err error) {
	if app.IsDaemonUnavailable(err) {
		exitRuntimeError(out, action+"失败（daemon 不在线）", daemonRuntimeErrorCause(err), "请先执行 dalek daemon start 后重试")
	}
	exitRuntimeError(out, action+"失败", err.Error(), "检查 daemon 与 focus 状态")
}

func sanitizeRequestToken(raw string) string {
	raw = strings.TrimSpace(strings.ToLower(raw))
	if raw == "" {
		return "cli_focus"
	}
	var b strings.Builder
	for _, ch := range raw {
		switch {
		case ch >= 'a' && ch <= 'z':
			b.WriteRune(ch)
		case ch >= '0' && ch <= '9':
			b.WriteRune(ch)
		case ch == '-' || ch == '_':
			b.WriteRune(ch)
		case ch == ' ':
			b.WriteRune('_')
		}
	}
	if b.Len() == 0 {
		return "cli_focus"
	}
	return b.String()
}
