package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"dalek/internal/app"
)

func cmdManagerRunBatch(out cliOutputFormat, home, proj, ticketsFlag string, budget int) {
	p := mustOpenProjectWithOutput(out, home, proj)

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

	// 通过 daemon client start ticket，走 daemon 的 queue consumer + execution host
	_, daemonClient := mustOpenDaemonClient(out, home)
	projectName := p.Name()
	startTicket := func(ctx context.Context, ticketID uint) error {
		_, err := daemonClient.StartTicket(ctx, app.DaemonTicketStartRequest{
			Project:  projectName,
			TicketID: ticketID,
		})
		return err
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	softStop := make(chan struct{})
	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
		<-sig
		fmt.Fprintln(os.Stderr, "\n收到中断信号，完成当前 ticket 后停止...")
		close(softStop)
		<-sig
		fmt.Fprintln(os.Stderr, "\n强制停止...")
		cancel()
	}()

	focus, err := p.CreateFocusRun(ctx, "batch", ticketIDs, budget)
	if err != nil {
		exitRuntimeError(out, "创建 focus 失败", err.Error(), "检查是否已有 active focus（dalek manager show）")
	}
	fmt.Printf("focus batch 已创建: id=%d scope=%v budget=%d\n", focus.ID, ticketIDs, budget)

	if err := p.RunBatchFocus(ctx, focus, startTicket, softStop); err != nil {
		fmt.Fprintf(os.Stderr, "focus batch 结束: %v\n", err)
	}
	fmt.Printf("focus batch 完成: status=%s progress=%d/%d summary=%s\n",
		focus.Status, focus.CompletedCount, focus.TotalCount, focus.Summary)
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
	focus, err := p.ActiveFocusRun(context.Background())
	if err != nil {
		exitRuntimeError(out, "查询 focus 失败", err.Error(), "检查 DB 状态")
	}
	if focus == nil {
		fmt.Println("当前无 active focus (idle)")
		return
	}

	if out == outputJSON {
		printJSONOrExit(focus)
		return
	}
	fmt.Printf("focus_id=%d  mode=%s  status=%s  progress=%d/%d",
		focus.ID, focus.Mode, focus.Status, focus.CompletedCount, focus.TotalCount)
	if focus.ActiveTicketID != nil {
		fmt.Printf("  active_ticket=t%d", *focus.ActiveTicketID)
	}
	fmt.Printf("  budget=%d/%d", focus.AgentBudget, focus.AgentBudgetMax)
	fmt.Println()
	if focus.Summary != "" {
		fmt.Println("summary:", focus.Summary)
	}
}

func cmdManagerStop(args []string) {
	fs := flag.NewFlagSet("manager stop", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	home := fs.String("home", globalHome, "dalek Home 目录")
	proj := fs.String("project", globalProject, "项目名")
	reason := fs.String("reason", "", "停止原因")
	parseFlagSetOrExit(fs, args, globalOutput, "manager stop 参数解析失败", "运行 dalek manager stop --help")

	p := mustOpenProjectWithOutput(globalOutput, *home, *proj)
	if err := p.StopFocusRun(context.Background(), *reason); err != nil {
		exitRuntimeError(globalOutput, "停止 focus 失败", err.Error(), "检查是否有 active focus")
	}
	fmt.Println("focus 已停止")
}
