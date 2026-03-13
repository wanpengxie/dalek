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

func cmdManager(args []string) {
	if len(args) == 0 {
		printManagerUsage()
		os.Exit(2)
	}
	sub := strings.TrimSpace(args[0])
	switch sub {
	case "status":
		cmdManagerStatus(args[1:])
	case "tick":
		cmdManagerTick(args[1:])
	case "run":
		cmdManagerRun(args[1:])
	case "pause":
		cmdManagerPauseResume(args[1:], false)
	case "resume":
		cmdManagerPauseResume(args[1:], true)
	case "-h", "--help", "help":
		printManagerUsage()
		os.Exit(0)
	default:
		exitUsageError(globalOutput,
			fmt.Sprintf("未知 manager 子命令: %s", sub),
			"manager 命令组仅支持固定子命令",
			"运行 dalek manager --help 查看可用命令",
		)
	}
}

func printManagerUsage() {
	printGroupUsage("PM 调度器控制", "dalek manager <command> [flags]", []string{
		"status    查看 PM 状态",
		"tick      执行一次调度循环",
		"run       单次调试（--sync-worker-run 调试入口）",
		"pause     暂停自动派发",
		"resume    恢复自动派发",
	})
	fmt.Fprintln(os.Stderr, "Use \"dalek manager <command> --help\" for more information.")
}

func cmdManagerStatus(args []string) {
	fs := flag.NewFlagSet("manager status", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		printSubcommandUsage(
			fs,
			"查看 PM 状态",
			"dalek manager status [--output text|json]",
			"dalek manager status",
			"dalek manager status -o json",
		)
	}
	home := fs.String("home", globalHome, "dalek Home 目录（默认 ~/.dalek）")
	proj := fs.String("project", globalProject, "项目名（可选）")
	projShort := fs.String("p", globalProject, "项目名（可选）")
	output := addOutputFlag(fs, "输出格式: text|json（默认 text）")
	parseFlagSetOrExit(fs, args, globalOutput, "manager status 参数解析失败", "运行 dalek manager status --help 查看参数")
	if strings.TrimSpace(*projShort) != "" {
		*proj = strings.TrimSpace(*projShort)
	}
	out := parseOutputOrExit(*output, true)

	p := mustOpenProjectWithOutput(out, *home, *proj)
	st, err := p.GetPMState(context.Background())
	if err != nil {
		exitRuntimeError(out,
			"读取 manager 状态失败",
			err.Error(),
			"稍后重试，或检查数据库状态",
		)
	}
	gcPending, err := p.CountPendingWorktreeCleanup(context.Background())
	if err != nil {
		exitRuntimeError(out,
			"读取 worktree 清理状态失败",
			err.Error(),
			"稍后重试，或检查数据库状态",
		)
	}
	healthMetrics, err := p.GetPMHealthMetrics(context.Background(), app.PMHealthMetricsOptions{})
	if err != nil {
		exitRuntimeError(out,
			"读取健康指标失败",
			err.Error(),
			"稍后重试，或检查数据库状态",
		)
	}

	if out == outputJSON {
		lastTick := any(nil)
		if st.LastTickAt != nil {
			lastTick = st.LastTickAt.Local().Format(time.RFC3339)
		}
		lastRecovery := any(nil)
		if st.LastRecoveryAt != nil {
			lastRecovery = map[string]any{
				"recovered_at": st.LastRecoveryAt.Local().Format(time.RFC3339),
				"task_runs":    st.LastRecoveryTaskRuns,
				"notes":        st.LastRecoveryNotes,
				"workers":      st.LastRecoveryWorkers,
			}
		}
		printJSONOrExit(map[string]any{
			"schema":              "dalek.manager.status.v1",
			"max_running":         st.MaxRunningWorkers,
			"last_tick_at":        lastTick,
			"last_event_id":       st.LastEventID,
			"last_recovery":       lastRecovery,
			"worktree_gc_pending": gcPending,
			"health_metrics":      healthMetrics,
			"updated_at":          st.UpdatedAt.Local().Format(time.RFC3339),
		})
		return
	}

	lastTick := "-"
	if st.LastTickAt != nil {
		lastTick = st.LastTickAt.Local().Format("01-02 15:04:05")
	}
	fmt.Printf(
		"max_running=%d  gc_pending=%d  last_tick=%s  last_event_id=%d\n",
		st.MaxRunningWorkers,
		gcPending,
		lastTick,
		st.LastEventID,
	)
	if st.LastRecoveryAt != nil {
		fmt.Printf(
			"last_recovery=%s  task_runs=%d  notes=%d  workers=%d\n",
			st.LastRecoveryAt.Local().Format("01-02 15:04:05"),
			st.LastRecoveryTaskRuns,
			st.LastRecoveryNotes,
			st.LastRecoveryWorkers,
		)
	}
	fmt.Printf(
		"health window=%s..%s  worker_bootstrap_failure_rate=%.2f%%(%d/%d)  terminal_state_conflict_count=%d  duplicate_terminal_report_count=%d  merge_discard_count=%d  integration_ticket_count=%d  manual_intervention_count=%d  real_acceptance_pass_rate=%s\n",
		healthMetrics.WindowStart.Local().Format("01-02 15:04:05"),
		healthMetrics.WindowEnd.Local().Format("01-02 15:04:05"),
		healthMetrics.WorkerBootstrapFailureRate*100,
		healthMetrics.WorkerBootstrapFailureCount,
		healthMetrics.WorkerRunCount,
		healthMetrics.TerminalStateConflictCount,
		healthMetrics.DuplicateTerminalReportCount,
		healthMetrics.MergeDiscardCount,
		healthMetrics.IntegrationTicketCount,
		healthMetrics.ManualInterventionCount,
		formatNullablePercent(healthMetrics.RealAcceptancePassRate),
	)
}

func cmdManagerPauseResume(args []string, enabled bool) {
	fs := flag.NewFlagSet("manager pause/resume", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	if enabled {
		fs.Usage = func() {
			printSubcommandUsage(
				fs,
				"恢复自动派发",
				"dalek manager resume",
				"dalek manager resume",
				"dalek manager resume --project demo",
			)
		}
	} else {
		fs.Usage = func() {
			printSubcommandUsage(
				fs,
				"暂停自动派发",
				"dalek manager pause",
				"dalek manager pause",
				"dalek manager pause --project demo",
			)
		}
	}
	home := fs.String("home", globalHome, "dalek Home 目录（默认 ~/.dalek）")
	proj := fs.String("project", globalProject, "项目名（可选）")
	projShort := fs.String("p", globalProject, "项目名（可选）")
	parseFlagSetOrExit(fs, args, globalOutput, "manager pause/resume 参数解析失败", "运行 dalek manager --help 查看参数")
	if strings.TrimSpace(*projShort) != "" {
		*proj = strings.TrimSpace(*projShort)
	}

	_ = mustOpenProjectWithOutput(globalOutput, *home, *proj)
	if enabled {
		fmt.Println("autopilot 已移除（planner loop 已清理）。队列调度由 queue consumer 自动完成。")
	} else {
		fmt.Println("autopilot 已移除（planner loop 已清理）。队列调度由 queue consumer 自动完成。")
	}
}

func cmdManagerTick(args []string) {
	fs := flag.NewFlagSet("manager tick", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		printSubcommandUsage(
			fs,
			"执行一次调度循环",
			"dalek manager tick [--max N] [--dry-run] [--timeout 0] [--output text|json]",
			"dalek manager tick",
			"dalek manager tick --dry-run -o json",
		)
	}
	home := fs.String("home", globalHome, "dalek Home 目录（默认 ~/.dalek）")
	proj := fs.String("project", globalProject, "项目名（可选）")
	projShort := fs.String("p", globalProject, "项目名（可选）")
	maxRunning := fs.Int("max", 0, "最大并发 running workers（可选；0 表示用 DB 默认）")
	dryRun := fs.Bool("dry-run", false, "只观测/生成 inbox/merge 提案，不做 start/worker-run")
	timeout := fs.Duration("timeout", 0, "本次 tick 超时（默认 0，不超时）")
	output := addOutputFlag(fs, "输出格式: text|json（默认 text）")
	parseFlagSetOrExit(fs, args, globalOutput, "manager tick 参数解析失败", "运行 dalek manager tick --help 查看参数")
	if strings.TrimSpace(*projShort) != "" {
		*proj = strings.TrimSpace(*projShort)
	}
	out := parseOutputOrExit(*output, true)
	if *timeout < 0 {
		exitUsageError(out,
			"非法参数 --timeout",
			"--timeout 不能小于 0（0 表示不超时）",
			"例如: dalek manager tick --timeout 0",
		)
	}

	p := mustOpenProjectWithOutput(out, *home, *proj)
	ctx, cancel := projectCtx(*timeout)
	defer cancel()
	res, err := p.ManagerTick(ctx, app.ManagerTickOptions{MaxRunningWorkers: *maxRunning, DryRun: *dryRun})
	if err != nil {
		exitRuntimeError(out,
			"manager tick 失败",
			err.Error(),
			"稍后重试，或检查项目运行态",
		)
	}

	if out == outputJSON {
		printJSONOrExit(map[string]any{
			"schema":            "dalek.manager.tick.v1",
			"at":                res.At.Local().Format(time.RFC3339),
			"max_running":       res.MaxRunning,
			"running":           res.Running,
			"running_blocked":   res.RunningBlocked,
			"zombie_recovered":  res.ZombieRecovered,
			"zombie_blocked":    res.ZombieBlocked,
			"zombie_illegal":    res.ZombieIllegal,
			"zombie_undefined":  res.ZombieUndefined,
			"capacity":          res.Capacity,
			"events_consumed":   res.EventsConsumed,
			"inbox_upserts":     res.InboxUpserts,
			"started_tickets":   res.StartedTickets,
			"activated_tickets": res.ActivatedTickets,
			"serial_deferred":   res.SerialDeferred,
			"merge_frozen":      res.MergeFrozen,
			"surface_conflicts": res.SurfaceConflicts,
			"errors":            res.Errors,
		})
		return
	}

	fmt.Printf("%s  running=%d(blocked=%d)  zombie(rec=%d blocked=%d illegal=%d undefined=%d)  cap=%d  started=%d  activated=%d  serial_deferred=%d  inbox=%d  merge_frozen=%d  surface_conflicts=%d\n",
		res.At.Local().Format("01-02 15:04:05"),
		res.Running, res.RunningBlocked,
		res.ZombieRecovered, res.ZombieBlocked, res.ZombieIllegal, res.ZombieUndefined,
		res.Capacity,
		len(res.StartedTickets),
		len(res.ActivatedTickets),
		len(res.SerialDeferred),
		res.InboxUpserts,
		len(res.MergeFrozen),
		len(res.SurfaceConflicts),
	)
	if len(res.Errors) > 0 {
		for _, e := range res.Errors {
			fmt.Fprintln(os.Stderr, "error:", e)
		}
	}
}

func cmdManagerRun(args []string) {
	fs := flag.NewFlagSet("manager run", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		printSubcommandUsage(
			fs,
			"执行一次调度（非 daemon 调试入口）",
			"dalek manager run --once --sync-worker-run --worker-run-timeout 120m [--max N] [--dry-run] [--output text|json]",
			"dalek manager run --once --sync-worker-run --worker-run-timeout 120m",
			"dalek manager run --once --sync-worker-run --dry-run -o json",
			"dalek manager run",
			"dalek daemon start",
		)
	}
	home := fs.String("home", globalHome, "dalek Home 目录（默认 ~/.dalek）")
	proj := fs.String("project", globalProject, "项目名（可选）")
	projShort := fs.String("p", globalProject, "项目名（可选）")
	_ = fs.Duration("interval", 15*time.Second, "已废弃（manager run 前台循环已移除）")
	maxRunning := fs.Int("max", 0, "最大并发 running workers（可选；0 表示用 DB 默认）")
	dryRun := fs.Bool("dry-run", false, "只观测/生成 inbox/merge 提案，不做 start/worker-run")
	once := fs.Bool("once", false, "仅执行一次调度循环（sync-worker-run 模式必填）")
	syncWorkerRun := fs.Bool("sync-worker-run", false, "本地同步执行 worker run（阻塞等待 worker run 完成，不走 daemon）")
	workerRunTimeout := fs.Duration("worker-run-timeout", 0, "同步 worker run 超时（仅 --sync-worker-run 时生效，必须 > 0）")
	output := addOutputFlag(fs, "输出格式: text|json（默认 text）")
	parseFlagSetOrExit(fs, args, globalOutput, "manager run 参数解析失败", "运行 dalek manager run --help 查看参数")
	if strings.TrimSpace(*projShort) != "" {
		*proj = strings.TrimSpace(*projShort)
	}
	out := parseOutputOrExit(*output, true)
	if !*syncWorkerRun {
		exitRuntimeError(out,
			"manager run 已迁移到 daemon，前台循环已移除",
			"请使用 dalek daemon start 启动常驻 manager loop（30s tick + 事件驱动）",
			"如需非 daemon 单次调试，请使用 dalek manager run --once --sync-worker-run",
		)
	}
	if !*once {
		exitUsageError(out,
			"缺少必填参数 --once",
			"--sync-worker-run 仅支持配合 --once 使用",
			"例如: dalek manager run --once --sync-worker-run --worker-run-timeout 120m",
		)
	}
	if *workerRunTimeout <= 0 {
		exitUsageError(out,
			"--sync-worker-run 模式必须指定 --worker-run-timeout > 0",
			"同步 worker run 需要设置超时，避免终端无限阻塞",
			"例如: dalek manager run --once --sync-worker-run --worker-run-timeout 120m",
		)
	}

	p := mustOpenProjectWithOutput(out, *home, *proj)
	res, err := p.ManagerTick(context.Background(), app.ManagerTickOptions{
		MaxRunningWorkers: *maxRunning,
		DryRun:            *dryRun,
		SyncWorkerRun:     true,
		WorkerRunTimeout:  *workerRunTimeout,
	})
	if err != nil {
		exitRuntimeError(out,
			"manager run 失败",
			err.Error(),
			"稍后重试，或检查项目运行态",
		)
	}

	if out == outputJSON {
		printJSONOrExit(map[string]any{
			"schema":             "dalek.manager.run.v1",
			"mode":               "sync_worker_run",
			"worker_run_timeout": workerRunTimeout.String(),
			"at":                 res.At.Local().Format(time.RFC3339),
			"max_running":        res.MaxRunning,
			"running":            res.Running,
			"running_blocked":    res.RunningBlocked,
			"zombie_recovered":   res.ZombieRecovered,
			"zombie_blocked":     res.ZombieBlocked,
			"zombie_illegal":     res.ZombieIllegal,
			"zombie_undefined":   res.ZombieUndefined,
			"capacity":           res.Capacity,
			"events_consumed":    res.EventsConsumed,
			"inbox_upserts":      res.InboxUpserts,
			"started_tickets":    res.StartedTickets,
			"activated_tickets":  res.ActivatedTickets,
			"serial_deferred":    res.SerialDeferred,
			"merge_frozen":       res.MergeFrozen,
			"surface_conflicts":  res.SurfaceConflicts,
			"errors":             res.Errors,
		})
		return
	}

	timeoutText := "none"
	if *workerRunTimeout > 0 {
		timeoutText = workerRunTimeout.String()
	}
	fmt.Printf("%s  mode=sync_worker_run  worker_run_timeout=%s  running=%d(blocked=%d)  zombie(rec=%d blocked=%d illegal=%d undefined=%d)  cap=%d  started=%d  activated=%d  serial_deferred=%d  inbox=%d  merge_frozen=%d  surface_conflicts=%d\n",
		res.At.Local().Format("01-02 15:04:05"),
		timeoutText,
		res.Running, res.RunningBlocked,
		res.ZombieRecovered, res.ZombieBlocked, res.ZombieIllegal, res.ZombieUndefined,
		res.Capacity,
		len(res.StartedTickets),
		len(res.ActivatedTickets),
		len(res.SerialDeferred),
		res.InboxUpserts,
		len(res.MergeFrozen),
		len(res.SurfaceConflicts),
	)
	if len(res.Errors) > 0 {
		for _, e := range res.Errors {
			fmt.Fprintln(os.Stderr, "error:", e)
		}
	}
}

func formatNullablePercent(v *float64) string {
	if v == nil {
		return "-"
	}
	return fmt.Sprintf("%.2f%%", (*v)*100)
}
