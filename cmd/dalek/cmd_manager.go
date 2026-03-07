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
		"run       单次调度（--sync-dispatch 调试入口）",
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

	if out == outputJSON {
		lastTick := any(nil)
		if st.LastTickAt != nil {
			lastTick = st.LastTickAt.Local().Format(time.RFC3339)
		}
		plannerActiveTaskRunID := any(nil)
		if st.PlannerActiveTaskRunID != nil {
			plannerActiveTaskRunID = *st.PlannerActiveTaskRunID
		}
		plannerCooldownUntil := any(nil)
		if st.PlannerCooldownUntil != nil {
			plannerCooldownUntil = st.PlannerCooldownUntil.Local().Format(time.RFC3339)
		}
		plannerLastRunAt := any(nil)
		if st.PlannerLastRunAt != nil {
			plannerLastRunAt = st.PlannerLastRunAt.Local().Format(time.RFC3339)
		}
		lastRecovery := any(nil)
		if st.LastRecoveryAt != nil {
			lastRecovery = map[string]any{
				"recovered_at":  st.LastRecoveryAt.Local().Format(time.RFC3339),
				"dispatch_jobs": st.LastRecoveryDispatchJobs,
				"task_runs":     st.LastRecoveryTaskRuns,
				"notes":         st.LastRecoveryNotes,
				"workers":       st.LastRecoveryWorkers,
			}
		}
		printJSONOrExit(map[string]any{
			"schema":                     "dalek.manager.status.v1",
			"autopilot":                  st.AutopilotEnabled,
			"max_running":                st.MaxRunningWorkers,
			"planner_dirty":              st.PlannerDirty,
			"planner_wake_version":       st.PlannerWakeVersion,
			"planner_active_task_run_id": plannerActiveTaskRunID,
			"planner_cooldown_until":     plannerCooldownUntil,
			"planner_last_error":         strings.TrimSpace(st.PlannerLastError),
			"planner_last_run_at":        plannerLastRunAt,
			"last_tick_at":               lastTick,
			"last_event_id":              st.LastEventID,
			"last_recovery":              lastRecovery,
			"worktree_gc_pending":        gcPending,
			"updated_at":                 st.UpdatedAt.Local().Format(time.RFC3339),
		})
		return
	}

	lastTick := "-"
	if st.LastTickAt != nil {
		lastTick = st.LastTickAt.Local().Format("01-02 15:04:05")
	}
	plannerActiveTaskRunID := "-"
	if st.PlannerActiveTaskRunID != nil {
		plannerActiveTaskRunID = fmt.Sprintf("%d", *st.PlannerActiveTaskRunID)
	}
	fmt.Printf(
		"autopilot=%v  max_running=%d  planner_dirty=%v  planner_active_task_run_id=%s  gc_pending=%d  last_tick=%s  last_event_id=%d\n",
		st.AutopilotEnabled,
		st.MaxRunningWorkers,
		st.PlannerDirty,
		plannerActiveTaskRunID,
		gcPending,
		lastTick,
		st.LastEventID,
	)
	fmt.Printf(
		"planner_wake_version=%d  planner_cooldown_until=%s  planner_last_run_at=%s  planner_last_error=%s\n",
		st.PlannerWakeVersion,
		formatTimePtr(st.PlannerCooldownUntil),
		formatTimePtr(st.PlannerLastRunAt),
		emptyAsDash(strings.TrimSpace(st.PlannerLastError)),
	)
	if st.LastRecoveryAt != nil {
		fmt.Printf(
			"last_recovery=%s  dispatch_jobs=%d  task_runs=%d  notes=%d  workers=%d\n",
			st.LastRecoveryAt.Local().Format("01-02 15:04:05"),
			st.LastRecoveryDispatchJobs,
			st.LastRecoveryTaskRuns,
			st.LastRecoveryNotes,
			st.LastRecoveryWorkers,
		)
	}
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

	p := mustOpenProjectWithOutput(globalOutput, *home, *proj)
	st, err := p.SetAutopilotEnabled(context.Background(), enabled)
	if err != nil {
		exitRuntimeError(globalOutput,
			"更新 autopilot 失败",
			err.Error(),
			"稍后重试，或检查数据库状态",
		)
	}
	fmt.Printf("autopilot=%v  max_running=%d\n", st.AutopilotEnabled, st.MaxRunningWorkers)
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
	dryRun := fs.Bool("dry-run", false, "只观测/生成 inbox/merge 提案，不做 start/dispatch")
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
			"schema":             "dalek.manager.tick.v1",
			"at":                 res.At.Local().Format(time.RFC3339),
			"autopilot":          res.AutopilotEnabled,
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
			"dispatched_tickets": res.DispatchedTickets,
			"merge_proposed":     res.MergeProposed,
			"errors":             res.Errors,
		})
		return
	}

	fmt.Printf("%s  autopilot=%v  running=%d(blocked=%d)  zombie(rec=%d blocked=%d illegal=%d undefined=%d)  cap=%d  started=%d  dispatched=%d  inbox=%d  merge_proposed=%d\n",
		res.At.Local().Format("01-02 15:04:05"),
		res.AutopilotEnabled,
		res.Running, res.RunningBlocked,
		res.ZombieRecovered, res.ZombieBlocked, res.ZombieIllegal, res.ZombieUndefined,
		res.Capacity,
		len(res.StartedTickets),
		len(res.DispatchedTickets),
		res.InboxUpserts,
		len(res.MergeProposed),
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
			"dalek manager run --once --sync-dispatch --dispatch-timeout 120m [--max N] [--dry-run] [--output text|json]",
			"dalek manager run --once --sync-dispatch --dispatch-timeout 120m",
			"dalek manager run --once --sync-dispatch --dry-run -o json",
			"dalek manager run",
			"dalek daemon start",
		)
	}
	home := fs.String("home", globalHome, "dalek Home 目录（默认 ~/.dalek）")
	proj := fs.String("project", globalProject, "项目名（可选）")
	projShort := fs.String("p", globalProject, "项目名（可选）")
	_ = fs.Duration("interval", 15*time.Second, "已废弃（manager run 前台循环已移除）")
	maxRunning := fs.Int("max", 0, "最大并发 running workers（可选；0 表示用 DB 默认）")
	dryRun := fs.Bool("dry-run", false, "只观测/生成 inbox/merge 提案，不做 start/dispatch")
	once := fs.Bool("once", false, "仅执行一次调度循环（sync-dispatch 模式必填）")
	syncDispatch := fs.Bool("sync-dispatch", false, "本地同步派发（阻塞等待 dispatch 完成，不走 daemon）")
	dispatchTimeout := fs.Duration("dispatch-timeout", 0, "同步派发超时（仅 --sync-dispatch 时生效，必须 > 0）")
	output := addOutputFlag(fs, "输出格式: text|json（默认 text）")
	parseFlagSetOrExit(fs, args, globalOutput, "manager run 参数解析失败", "运行 dalek manager run --help 查看参数")
	if strings.TrimSpace(*projShort) != "" {
		*proj = strings.TrimSpace(*projShort)
	}
	out := parseOutputOrExit(*output, true)
	if !*syncDispatch {
		exitRuntimeError(out,
			"manager run 已迁移到 daemon，前台循环已移除",
			"请使用 dalek daemon start 启动常驻 manager loop（30s tick + 事件驱动）",
			"如需非 daemon 单次调试，请使用 dalek manager run --once --sync-dispatch",
		)
	}
	if !*once {
		exitUsageError(out,
			"缺少必填参数 --once",
			"--sync-dispatch 仅支持配合 --once 使用",
			"例如: dalek manager run --once --sync-dispatch --dispatch-timeout 120m",
		)
	}
	if *dispatchTimeout <= 0 {
		exitUsageError(out,
			"--sync-dispatch 模式必须指定 --dispatch-timeout > 0",
			"同步派发需要设置超时，避免终端无限阻塞",
			"例如: dalek manager run --once --sync-dispatch --dispatch-timeout 120m",
		)
	}

	p := mustOpenProjectWithOutput(out, *home, *proj)
	res, err := p.ManagerTick(context.Background(), app.ManagerTickOptions{
		MaxRunningWorkers: *maxRunning,
		DryRun:            *dryRun,
		SyncDispatch:      true,
		DispatchTimeout:   *dispatchTimeout,
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
			"mode":               "sync_dispatch",
			"dispatch_timeout":   dispatchTimeout.String(),
			"at":                 res.At.Local().Format(time.RFC3339),
			"autopilot":          res.AutopilotEnabled,
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
			"dispatched_tickets": res.DispatchedTickets,
			"merge_proposed":     res.MergeProposed,
			"errors":             res.Errors,
		})
		return
	}

	timeoutText := "none"
	if *dispatchTimeout > 0 {
		timeoutText = dispatchTimeout.String()
	}
	fmt.Printf("%s  mode=sync_dispatch  dispatch_timeout=%s  autopilot=%v  running=%d(blocked=%d)  zombie(rec=%d blocked=%d illegal=%d undefined=%d)  cap=%d  started=%d  dispatched=%d  inbox=%d  merge_proposed=%d\n",
		res.At.Local().Format("01-02 15:04:05"),
		timeoutText,
		res.AutopilotEnabled,
		res.Running, res.RunningBlocked,
		res.ZombieRecovered, res.ZombieBlocked, res.ZombieIllegal, res.ZombieUndefined,
		res.Capacity,
		len(res.StartedTickets),
		len(res.DispatchedTickets),
		res.InboxUpserts,
		len(res.MergeProposed),
	)
	if len(res.Errors) > 0 {
		for _, e := range res.Errors {
			fmt.Fprintln(os.Stderr, "error:", e)
		}
	}
}
