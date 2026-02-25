package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"dalek/internal/app"
)

type agentRunPublic struct {
	RunID      uint    `json:"run_id"`
	RequestID  string  `json:"request_id,omitempty"`
	Provider   string  `json:"provider,omitempty"`
	Model      string  `json:"model,omitempty"`
	RuntimeDir string  `json:"runtime_dir,omitempty"`
	Prompt     string  `json:"prompt,omitempty"`
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

func cmdAgent(args []string) {
	if len(args) == 0 {
		cmdAgentHelp()
		os.Exit(2)
	}
	sub := strings.TrimSpace(args[0])
	switch sub {
	case "run":
		cmdAgentRun(args[1:])
	case "ls", "list":
		cmdAgentList(args[1:])
	case "show":
		cmdAgentShow(args[1:])
	case "cancel":
		cmdAgentCancel(args[1:])
	case "logs":
		cmdAgentLogs(args[1:])
	case "finish":
		cmdAgentFinish(args[1:])
	case "-h", "--help", "help":
		cmdAgentHelp()
		os.Exit(0)
	default:
		exitUsageError(globalOutput,
			fmt.Sprintf("未知 agent 子命令: %s", sub),
			"agent 命令组仅支持固定子命令",
			"运行 dalek agent --help 查看可用命令",
		)
	}
}

func cmdAgentHelp() {
	printGroupUsage("Agent 执行命令", "dalek agent <command> [flags]", []string{
		"run       提交 subagent 异步运行",
		"ls        列出 subagent 运行记录",
		"show      查看单个 subagent 运行详情",
		"cancel    取消运行中的 subagent",
		"logs      查看 subagent 执行日志",
		"finish    标记一次 agent run 完成（兼容旧流程）",
	})
	fmt.Fprintln(os.Stderr, "Use \"dalek agent <command> --help\" for more information.")
}

func cmdAgentRun(args []string) {
	fs := flag.NewFlagSet("agent run", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		printSubcommandUsage(
			fs,
			"提交 subagent 运行",
			"dalek agent run --prompt \"...\" [--provider codex|claude] [--model MODEL] [--sync] [--timeout 120m] [--output text|json]",
			"dalek agent run --prompt \"实现 ticket #58 的子任务\"",
			"dalek agent run --prompt \"修复测试\" --provider claude --model sonnet",
			"dalek agent run --prompt \"快速验证\" --sync --timeout 60m",
		)
	}
	home := fs.String("home", globalHome, "dalek Home 目录（默认 ~/.dalek）")
	proj := fs.String("project", globalProject, "项目名（可选）")
	projShort := fs.String("p", globalProject, "项目名（可选）")
	prompt := fs.String("prompt", "", "任务提示词（必填）")
	provider := fs.String("provider", "", "agent provider（可选，codex|claude）")
	model := fs.String("model", "", "agent model（可选）")
	requestID := fs.String("request-id", "", "幂等请求 ID（可选）")
	syncMode := fs.Bool("sync", false, "同步执行（阻塞直到完成）")
	timeout := fs.Duration("timeout", 0, "超时（可选；--sync 时必须 >0）")
	output := addOutputFlag(fs, "输出格式: text|json（默认 text）")
	parseFlagSetOrExit(fs, args, globalOutput, "agent run 参数解析失败", "运行 dalek agent run --help 查看参数")
	timeoutProvided := flagProvided(fs, "timeout")
	if strings.TrimSpace(*projShort) != "" {
		*proj = strings.TrimSpace(*projShort)
	}
	out := parseOutputOrExit(*output, true)
	if strings.TrimSpace(*prompt) == "" {
		exitUsageError(out,
			"缺少必填参数 --prompt",
			"agent run 需要任务提示词",
			"dalek agent run --prompt \"实现 xxx\"",
		)
	}
	if *syncMode && *timeout <= 0 {
		exitUsageError(out,
			"--sync 模式必须指定 --timeout > 0",
			"同步执行需要设置超时，避免终端无限阻塞",
			"dalek agent run --prompt \"...\" --sync --timeout 120m",
		)
	}
	if timeoutProvided && *timeout <= 0 {
		exitUsageError(out,
			"非法参数 --timeout",
			"--timeout 必须为正值",
			"例如: dalek agent run --prompt \"...\" --timeout 120m",
		)
	}
	p := mustOpenProjectWithOutput(out, *home, *proj)
	if *syncMode {
		ctx, cancel := projectCtx(*timeout)
		defer cancel()
		submission, err := p.SubmitSubagentRun(ctx, app.SubagentSubmitOptions{
			RequestID: strings.TrimSpace(*requestID),
			Provider:  strings.TrimSpace(*provider),
			Model:     strings.TrimSpace(*model),
			Prompt:    strings.TrimSpace(*prompt),
		})
		if err != nil {
			exitRuntimeError(out,
				"agent run 提交失败",
				err.Error(),
				"检查 --provider/--model 参数或提示词后重试",
			)
		}
		err = p.RunSubagentJob(ctx, submission.TaskRunID, app.SubagentRunOptions{RunnerID: "cli_sync"})
		if err != nil {
			exitRuntimeError(out,
				"agent run 同步执行失败",
				err.Error(),
				fmt.Sprintf("使用 dalek agent logs --run-id %d 查看日志", submission.TaskRunID),
			)
		}
		if out == outputJSON {
			printJSONOrExit(map[string]any{
				"schema":     "dalek.agent.run.sync.v1",
				"mode":       "sync",
				"accepted":   true,
				"run_id":     submission.TaskRunID,
				"request_id": strings.TrimSpace(submission.RequestID),
				"provider":   strings.TrimSpace(submission.Provider),
				"model":      strings.TrimSpace(submission.Model),
			})
			return
		}
		fmt.Printf("agent run 完成(sync): run=%d request=%s\n", submission.TaskRunID, strings.TrimSpace(submission.RequestID))
		fmt.Printf("show:  dalek agent show --run-id %d\n", submission.TaskRunID)
		fmt.Printf("logs:  dalek agent logs --run-id %d\n", submission.TaskRunID)
		return
	}

	_, daemonClient := mustOpenDaemonClient(out, *home)
	ctx, cancel := projectCtx(*timeout)
	defer cancel()
	receipt, err := daemonClient.SubmitSubagentRun(ctx, app.DaemonSubagentSubmitRequest{
		Project:   strings.TrimSpace(p.Name()),
		RequestID: strings.TrimSpace(*requestID),
		Provider:  strings.TrimSpace(*provider),
		Model:     strings.TrimSpace(*model),
		Prompt:    strings.TrimSpace(*prompt),
	})
	if err != nil {
		if app.IsDaemonUnavailable(err) {
			exitFixFirstError(out, 1,
				"daemon 不在线，无法异步执行 agent run",
				daemonUnavailableAgentRunFix(),
				daemonRuntimeErrorCause(err),
			)
		}
		exitRuntimeError(out,
			"异步 agent run 失败",
			daemonRuntimeErrorCause(err),
			"检查 daemon 日志（dalek daemon logs）后重试，或改用 --sync",
		)
	}

	if out == outputJSON {
		printJSONOrExit(map[string]any{
			"schema":      "dalek.agent.run.accepted.v1",
			"mode":        "async",
			"accepted":    receipt.Accepted,
			"project":     strings.TrimSpace(receipt.Project),
			"request_id":  strings.TrimSpace(receipt.RequestID),
			"run_id":      receipt.TaskRunID,
			"provider":    strings.TrimSpace(receipt.Provider),
			"model":       strings.TrimSpace(receipt.Model),
			"runtime_dir": strings.TrimSpace(receipt.RuntimeDir),
			"query": map[string]string{
				"show":   fmt.Sprintf("dalek agent show --run-id %d", receipt.TaskRunID),
				"logs":   fmt.Sprintf("dalek agent logs --run-id %d", receipt.TaskRunID),
				"cancel": fmt.Sprintf("dalek agent cancel --run-id %d", receipt.TaskRunID),
			},
		})
		return
	}
	fmt.Printf("agent run accepted: request=%s run=%d provider=%s model=%s\n",
		strings.TrimSpace(receipt.RequestID),
		receipt.TaskRunID,
		strings.TrimSpace(receipt.Provider),
		strings.TrimSpace(receipt.Model),
	)
	fmt.Printf("show:  dalek agent show --run-id %d\n", receipt.TaskRunID)
	fmt.Printf("logs:  dalek agent logs --run-id %d\n", receipt.TaskRunID)
	fmt.Printf("cancel: dalek agent cancel --run-id %d\n", receipt.TaskRunID)
}

func cmdAgentList(args []string) {
	fs := flag.NewFlagSet("agent ls", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		printSubcommandUsage(
			fs,
			"列出 subagent 运行记录",
			"dalek agent ls [--limit 50] [--output text|json]",
			"dalek agent ls",
			"dalek agent ls -o json",
		)
	}
	home := fs.String("home", globalHome, "dalek Home 目录")
	proj := fs.String("project", globalProject, "项目名（可选）")
	projShort := fs.String("p", globalProject, "项目名（可选）")
	limit := fs.Int("limit", 50, "最多返回条数")
	timeout := fs.Duration("timeout", 5*time.Second, "超时")
	output := addOutputFlag(fs, "输出格式: text|json（默认 text）")
	parseFlagSetOrExit(fs, args, globalOutput, "agent ls 参数解析失败", "运行 dalek agent ls --help 查看参数")
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
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	statuses, err := p.ListTaskStatus(ctx, app.ListTaskOptions{
		OwnerType:       app.TaskOwnerSubagent,
		TaskType:        "subagent_run",
		IncludeTerminal: true,
		Limit:           *limit,
	})
	if err != nil {
		exitRuntimeError(out, "agent ls 失败", err.Error(), "稍后重试，或检查数据库状态")
	}
	recs, err := p.ListSubagentRuns(ctx, *limit)
	if err != nil {
		exitRuntimeError(out, "agent ls 失败", err.Error(), "稍后重试，或检查数据库状态")
	}
	meta := map[uint]app.SubagentRun{}
	for _, rec := range recs {
		meta[rec.TaskRunID] = rec
	}

	items := make([]agentRunPublic, 0, len(statuses))
	for _, st := range statuses {
		if !isSubagentTask(st) {
			continue
		}
		var rec *app.SubagentRun
		if m, ok := meta[st.RunID]; ok {
			copyRec := m
			rec = &copyRec
		}
		items = append(items, mapAgentRunPublic(st, rec))
	}

	if out == outputJSON {
		printJSONOrExit(map[string]any{
			"schema": "dalek.agent.list.v1",
			"runs":   items,
		})
		return
	}
	if len(items) == 0 {
		fmt.Println("(no subagent runs)")
		return
	}
	fmt.Println("run\tstatus\tprovider\tmodel\tupdated\tsummary")
	for _, it := range items {
		fmt.Printf("%d\t%s\t%s\t%s\t%s\t%s\n",
			it.RunID,
			trimField(it.RunStatus, 12),
			trimField(it.Provider, 12),
			trimField(it.Model, 18),
			trimField(it.UpdatedAt, 20),
			trimField(it.Summary, 80),
		)
	}
}

func cmdAgentShow(args []string) {
	fs := flag.NewFlagSet("agent show", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		printSubcommandUsage(
			fs,
			"查看 subagent run 详情",
			"dalek agent show --run-id <id> [--output text|json]",
			"dalek agent show --run-id 1",
			"dalek agent show --run-id 1 -o json",
		)
	}
	home := fs.String("home", globalHome, "dalek Home 目录")
	proj := fs.String("project", globalProject, "项目名（可选）")
	projShort := fs.String("p", globalProject, "项目名（可选）")
	runID := fs.Uint("run-id", 0, "task run id（必填）")
	timeout := fs.Duration("timeout", 5*time.Second, "超时")
	output := addOutputFlag(fs, "输出格式: text|json（默认 text）")
	parseFlagSetOrExit(fs, args, globalOutput, "agent show 参数解析失败", "运行 dalek agent show --help 查看参数")
	if strings.TrimSpace(*projShort) != "" {
		*proj = strings.TrimSpace(*projShort)
	}
	out := parseOutputOrExit(*output, true)
	if err := requirePositiveDuration("timeout", *timeout); err != nil {
		exitUsageError(out, "参数错误", err.Error(), "调整 --timeout 为正时长")
	}
	if *runID == 0 {
		exitUsageError(out,
			"缺少必填参数 --run-id",
			"agent show 需要 run id",
			"dalek agent show --run-id 1",
		)
	}

	p := mustOpenProjectWithOutput(out, *home, *proj)
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	st, err := p.GetTaskStatus(ctx, uint(*runID))
	if err != nil {
		exitRuntimeError(out, "agent show 失败", err.Error(), "稍后重试，或检查数据库状态")
	}
	if st == nil || !isSubagentTask(*st) {
		exitRuntimeError(out,
			fmt.Sprintf("subagent run #%d 不存在", *runID),
			"指定 run id 在当前项目中未找到",
			"先运行 dalek agent ls 查看可用 run id",
		)
	}
	rec, err := p.GetSubagentRun(ctx, uint(*runID))
	if err != nil {
		exitRuntimeError(out, "agent show 失败", err.Error(), "稍后重试，或检查数据库状态")
	}
	item := mapAgentRunPublic(*st, rec)

	if out == outputJSON {
		printJSONOrExit(map[string]any{
			"schema": "dalek.agent.show.v1",
			"run":    item,
		})
		return
	}

	fmt.Printf("run_id: %d\n", item.RunID)
	fmt.Printf("status: %s\n", emptyAsDash(item.RunStatus))
	fmt.Printf("provider/model: %s / %s\n", emptyAsDash(item.Provider), emptyAsDash(item.Model))
	fmt.Printf("request_id: %s\n", emptyAsDash(item.RequestID))
	fmt.Printf("runtime_dir: %s\n", emptyAsDash(item.RuntimeDir))
	fmt.Printf("next_action: %s\n", emptyAsDash(item.NextAction))
	fmt.Printf("needs_user: %v\n", item.NeedsUser)
	fmt.Printf("error: code=%s msg=%s\n", emptyAsDash(item.ErrorCode), emptyAsDash(item.ErrorMsg))
	fmt.Printf("started=%s finished=%s updated=%s\n", formatTimePtr(st.StartedAt), formatTimePtr(st.FinishedAt), item.UpdatedAt)
	fmt.Printf("summary: %s\n", emptyAsDash(item.Summary))
	if strings.TrimSpace(item.Prompt) != "" {
		fmt.Printf("prompt: %s\n", strings.TrimSpace(item.Prompt))
	}
}

func cmdAgentCancel(args []string) {
	fs := flag.NewFlagSet("agent cancel", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		printSubcommandUsage(
			fs,
			"取消运行中的 subagent",
			"dalek agent cancel --run-id <id> [--output text|json]",
			"dalek agent cancel --run-id 1",
			"dalek agent cancel --run-id 1 -o json",
		)
	}
	home := fs.String("home", globalHome, "dalek Home 目录")
	proj := fs.String("project", globalProject, "项目名（可选）")
	projShort := fs.String("p", globalProject, "项目名（可选）")
	runID := fs.Uint("run-id", 0, "task run id（必填）")
	timeout := fs.Duration("timeout", 5*time.Second, "超时")
	output := addOutputFlag(fs, "输出格式: text|json（默认 text）")
	parseFlagSetOrExit(fs, args, globalOutput, "agent cancel 参数解析失败", "运行 dalek agent cancel --help 查看参数")
	if strings.TrimSpace(*projShort) != "" {
		*proj = strings.TrimSpace(*projShort)
	}
	out := parseOutputOrExit(*output, true)
	if err := requirePositiveDuration("timeout", *timeout); err != nil {
		exitUsageError(out, "参数错误", err.Error(), "调整 --timeout 为正时长")
	}
	if *runID == 0 {
		exitUsageError(out,
			"缺少必填参数 --run-id",
			"agent cancel 需要 run id",
			"dalek agent cancel --run-id 1",
		)
	}

	p := mustOpenProjectWithOutput(out, *home, *proj)
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	st, err := p.GetTaskStatus(ctx, uint(*runID))
	if err != nil {
		exitRuntimeError(out, "agent cancel 失败", err.Error(), "稍后重试，或检查数据库状态")
	}
	if st == nil || !isSubagentTask(*st) {
		exitRuntimeError(out,
			fmt.Sprintf("subagent run #%d 不存在", *runID),
			"指定 run id 在当前项目中未找到",
			"先运行 dalek agent ls 查看可用 run id",
		)
	}

	daemonWarning := ""
	if _, daemonClient, derr := openDaemonClient(*home); derr != nil {
		daemonWarning = fmt.Sprintf("daemon cancel 调用不可用，已降级为仅标记数据库（%s）", strings.TrimSpace(derr.Error()))
	} else {
		cancelRes, cerr := daemonClient.CancelRun(ctx, uint(*runID))
		if cerr != nil {
			daemonWarning = fmt.Sprintf("daemon cancel 调用失败，已降级为仅标记数据库（%s）", strings.TrimSpace(cerr.Error()))
		} else if !cancelRes.Canceled {
			reason := strings.TrimSpace(cancelRes.Reason)
			if reason == "" {
				reason = "run 不在当前 daemon 执行上下文中"
			}
			daemonWarning = fmt.Sprintf("daemon 未确认取消信号（%s），已降级为仅标记数据库", reason)
		}
	}

	result, err := p.CancelTaskRun(ctx, uint(*runID))
	if err != nil {
		cause := strings.TrimSpace(err.Error())
		if daemonWarning != "" {
			cause = fmt.Sprintf("%s；%s", cause, daemonWarning)
		}
		exitRuntimeError(out, "agent cancel 失败", cause, "稍后重试，或检查数据库状态")
	}
	if !result.Canceled {
		exitRuntimeError(out,
			fmt.Sprintf("subagent run #%d 无法取消", *runID),
			emptyAsDash(strings.TrimSpace(result.Reason)),
			fmt.Sprintf("使用 dalek agent show --run-id %d 查看最新状态", *runID),
		)
	}
	if out == outputJSON {
		payload := map[string]any{
			"schema":     "dalek.agent.cancel.v1",
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
	fmt.Printf("subagent run #%d 已取消\n", result.RunID)
	fmt.Printf("show: dalek agent show --run-id %d\n", result.RunID)
	fmt.Printf("logs: dalek agent logs --run-id %d\n", result.RunID)
}

func cmdAgentLogs(args []string) {
	fs := flag.NewFlagSet("agent logs", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		printSubcommandUsage(
			fs,
			"查看 subagent 执行日志",
			"dalek agent logs --run-id <id> [--follow] [--timeout 30m] [--output text|json]",
			"dalek agent logs --run-id 1",
			"dalek agent logs --run-id 1 --follow",
		)
	}
	home := fs.String("home", globalHome, "dalek Home 目录")
	proj := fs.String("project", globalProject, "项目名（可选）")
	projShort := fs.String("p", globalProject, "项目名（可选）")
	runID := fs.Uint("run-id", 0, "task run id（必填）")
	follow := fs.Bool("follow", false, "持续跟随日志输出")
	timeout := fs.Duration("timeout", 0, "follow 模式超时（可选）")
	output := addOutputFlag(fs, "输出格式: text|json（默认 text）")
	parseFlagSetOrExit(fs, args, globalOutput, "agent logs 参数解析失败", "运行 dalek agent logs --help 查看参数")
	if strings.TrimSpace(*projShort) != "" {
		*proj = strings.TrimSpace(*projShort)
	}
	out := parseOutputOrExit(*output, true)
	if *runID == 0 {
		exitUsageError(out,
			"缺少必填参数 --run-id",
			"agent logs 需要 run id",
			"dalek agent logs --run-id 1",
		)
	}
	if *follow && out == outputJSON {
		exitUsageError(out,
			"--follow 不支持 JSON 输出",
			"跟随模式是持续流式输出，无法稳定编码为单个 JSON",
			"移除 --follow 或将 --output 设为 text",
		)
	}
	if flagProvided(fs, "timeout") && *timeout <= 0 {
		exitUsageError(out,
			"非法参数 --timeout",
			"--timeout 必须为正值",
			"例如: dalek agent logs --run-id 1 --follow --timeout 30m",
		)
	}

	p := mustOpenProjectWithOutput(out, *home, *proj)
	ctx, cancel := projectCtx(5 * time.Second)
	defer cancel()
	rec, err := p.GetSubagentRun(ctx, uint(*runID))
	if err != nil {
		exitRuntimeError(out, "agent logs 失败", err.Error(), "稍后重试，或检查数据库状态")
	}
	if rec == nil || strings.TrimSpace(rec.RuntimeDir) == "" {
		exitRuntimeError(out,
			fmt.Sprintf("subagent run #%d 无日志目录", *runID),
			"当前 run 尚未生成 runtime 记录",
			"先运行 dalek agent show --run-id <id> 确认状态",
		)
	}
	streamPath := filepath.Join(strings.TrimSpace(rec.RuntimeDir), "stream.log")
	sdkPath := filepath.Join(strings.TrimSpace(rec.RuntimeDir), "sdk-stream.log")
	logPath := streamPath
	if _, statErr := os.Stat(logPath); statErr != nil {
		if _, sdkErr := os.Stat(sdkPath); sdkErr == nil {
			logPath = sdkPath
		}
	}

	if !*follow {
		raw, rerr := os.ReadFile(logPath)
		if rerr != nil {
			if os.IsNotExist(rerr) {
				if out == outputJSON {
					printJSONOrExit(map[string]any{
						"schema":    "dalek.agent.logs.v1",
						"run_id":    *runID,
						"path":      logPath,
						"content":   "",
						"available": false,
					})
					return
				}
				fmt.Println("(log file not created yet)")
				return
			}
			exitRuntimeError(out, "读取日志失败", rerr.Error(), "确认 runtime 目录权限后重试")
		}
		if out == outputJSON {
			printJSONOrExit(map[string]any{
				"schema":    "dalek.agent.logs.v1",
				"run_id":    *runID,
				"path":      logPath,
				"content":   string(raw),
				"available": true,
			})
			return
		}
		if len(raw) == 0 {
			fmt.Println("(empty log)")
			return
		}
		fmt.Print(string(raw))
		return
	}

	followCtx, followCancel := projectCtx(*timeout)
	defer followCancel()
	offset := int64(0)
	for {
		grew := false
		raw, rerr := os.ReadFile(logPath)
		if rerr == nil {
			if int64(len(raw)) < offset {
				offset = 0
			}
			if int64(len(raw)) > offset {
				chunk := raw[offset:]
				if len(chunk) > 0 {
					fmt.Print(string(chunk))
				}
				offset = int64(len(raw))
				grew = true
			}
		}
		status, _ := p.GetTaskStatus(context.Background(), uint(*runID))
		if status != nil && isTaskTerminal(status.OrchestrationState) && !grew {
			return
		}
		select {
		case <-followCtx.Done():
			return
		case <-time.After(500 * time.Millisecond):
		}
	}
}

func cmdAgentFinish(args []string) {
	fs := flag.NewFlagSet("agent finish", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		printSubcommandUsage(
			fs,
			"标记 agent run 完成",
			"dalek agent finish --run-id <id> [--exit-code 0] [--timeout 10s]",
			"dalek agent finish --run-id 1 --exit-code 0",
			"dalek agent finish --run-id 9 --exit-code 1 --timeout 20s",
		)
	}
	home := fs.String("home", globalHome, "dalek Home 目录（默认 ~/.dalek）")
	proj := fs.String("project", globalProject, "项目名（可选）")
	projShort := fs.String("p", globalProject, "项目名（可选）")
	runID := fs.Uint("run-id", 0, "task run id（必填）")
	exitCode := fs.Int("exit-code", 0, "agent 退出码")
	timeout := fs.Duration("timeout", 10*time.Second, "超时（例如 10s）")
	parseFlagSetOrExit(fs, args, globalOutput, "agent finish 参数解析失败", "运行 dalek agent finish --help 查看参数")
	if strings.TrimSpace(*projShort) != "" {
		*proj = strings.TrimSpace(*projShort)
	}
	if *runID == 0 {
		exitUsageError(globalOutput,
			"缺少必填参数 --run-id",
			"agent finish 需要 task run id",
			"dalek agent finish --run-id 1 --exit-code 0",
		)
	}
	if *timeout <= 0 {
		exitUsageError(globalOutput,
			"非法参数 --timeout",
			"--timeout 必须大于 0",
			"例如: dalek agent finish --run-id 1 --timeout 10s",
		)
	}

	p := mustOpenProjectWithOutput(globalOutput, *home, *proj)
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	if err := p.FinishAgentRun(ctx, uint(*runID), *exitCode); err != nil {
		exitRuntimeError(globalOutput,
			"agent finish 失败",
			err.Error(),
			"确认 run-id 存在并重试",
		)
	}
	fmt.Println("ok")
}

func isSubagentTask(st app.TaskStatus) bool {
	return strings.TrimSpace(st.OwnerType) == string(app.TaskOwnerSubagent) && strings.TrimSpace(st.TaskType) == "subagent_run"
}

func mapAgentRunPublic(st app.TaskStatus, rec *app.SubagentRun) agentRunPublic {
	mapped := mapTaskStatusPublic(st)
	out := agentRunPublic{
		RunID:      st.RunID,
		RunStatus:  strings.TrimSpace(mapped.RunStatus),
		NextAction: strings.TrimSpace(mapped.NextAction),
		NeedsUser:  mapped.NeedsUser,
		Summary:    strings.TrimSpace(mapped.Summary),
		ErrorCode:  strings.TrimSpace(mapped.ErrorCode),
		ErrorMsg:   strings.TrimSpace(mapped.ErrorMsg),
		StartedAt:  mapped.StartedAt,
		FinishedAt: mapped.FinishedAt,
		UpdatedAt:  strings.TrimSpace(mapped.UpdatedAt),
	}
	if rec != nil {
		out.RequestID = strings.TrimSpace(rec.RequestID)
		out.Provider = strings.TrimSpace(rec.Provider)
		out.Model = strings.TrimSpace(rec.Model)
		out.RuntimeDir = strings.TrimSpace(rec.RuntimeDir)
		out.Prompt = strings.TrimSpace(rec.Prompt)
	}
	return out
}

func isTaskTerminal(state string) bool {
	switch strings.TrimSpace(strings.ToLower(state)) {
	case "succeeded", "failed", "canceled":
		return true
	default:
		return false
	}
}
