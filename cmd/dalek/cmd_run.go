package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"dalek/internal/app"
	nodeagentsvc "dalek/internal/services/nodeagent"
	runsvc "dalek/internal/services/run"
)

func cmdRun(args []string) {
	if len(args) == 0 {
		printRunUsage()
		os.Exit(2)
	}
	sub := strings.TrimSpace(args[0])
	switch sub {
	case "request":
		cmdRunRequest(args[1:])
	case "show":
		cmdRunShow(args[1:])
	case "logs":
		cmdRunLogs(args[1:])
	case "artifact", "artifacts":
		cmdRunArtifact(args[1:])
	case "cancel":
		cmdRunCancel(args[1:])
	case "help", "-h", "--help":
		printRunUsage()
		os.Exit(0)
	default:
		exitUsageError(globalOutput,
			fmt.Sprintf("未知 run 子命令: %s", sub),
			"run 命令组仅支持固定子命令",
			"运行 dalek run --help 查看可用命令",
		)
	}
}

func printRunUsage() {
	printGroupUsage("Run 运行与观测", "dalek run <command> [flags]", []string{
		"request    提交 run verify 请求",
		"show       查看 run 详情",
		"logs       查看 run 日志尾部",
		"artifact   查看 run 产物索引",
		"cancel     取消 run",
	})
	fmt.Fprintln(os.Stderr, "Use \"dalek run <command> --help\" for more information.")
}

func cmdRunRequest(args []string) {
	fs := flag.NewFlagSet("run request", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		printSubcommandUsage(
			fs,
			"提交 run verify 请求",
			"dalek run request --verify-target <target> [--request-id id] [--ticket N] [--snapshot-id snap] [--base-commit sha] [--workspace-generation gen] [--output text|json]",
			"dalek run request --verify-target test",
			"dalek run request --verify-target test --snapshot-id snap-1 -o json",
		)
	}
	home := fs.String("home", globalHome, "dalek Home 目录（默认 ~/.dalek）")
	proj := fs.String("project", globalProject, "项目名（可选）")
	projShort := fs.String("p", globalProject, "项目名（可选）")
	verifyTarget := fs.String("verify-target", "", "verify target（必填）")
	requestID := fs.String("request-id", "", "request id（可选）")
	ticketID := fs.Uint("ticket", 0, "ticket id（可选）")
	snapshotID := fs.String("snapshot-id", "", "snapshot id（可选）")
	baseCommit := fs.String("base-commit", "", "base commit（可选）")
	workspaceGeneration := fs.String("workspace-generation", "", "workspace generation（可选）")
	timeout := fs.Duration("timeout", 10*time.Second, "超时")
	output := addOutputFlag(fs, "输出格式: text|json（默认 text）")
	parseFlagSetOrExit(fs, args, globalOutput, "run request 参数解析失败", "运行 dalek run request --help 查看参数")
	if strings.TrimSpace(*projShort) != "" {
		*proj = strings.TrimSpace(*projShort)
	}
	out := parseOutputOrExit(*output, true)
	if strings.TrimSpace(*verifyTarget) == "" {
		exitUsageError(out, "缺少必填参数 --verify-target", "run request 需要 verify target", "dalek run request --verify-target test")
	}
	if err := requirePositiveDuration("timeout", *timeout); err != nil {
		exitUsageError(out, "参数错误", err.Error(), "调整 --timeout 为正时长")
	}

	p := mustOpenProjectWithOutput(out, *home, *proj)
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	res, err := p.SubmitRun(ctx, app.SubmitRunOptions{
		RequestID:                 strings.TrimSpace(*requestID),
		TicketID:                  uint(*ticketID),
		VerifyTarget:              strings.TrimSpace(*verifyTarget),
		SnapshotID:                strings.TrimSpace(*snapshotID),
		BaseCommit:                strings.TrimSpace(*baseCommit),
		SourceWorkspaceGeneration: strings.TrimSpace(*workspaceGeneration),
	})
	if err != nil {
		exitRuntimeError(out, "run request 失败", err.Error(), "检查 verify target 或 snapshot 参数后重试")
	}
	if out == outputJSON {
		printJSONOrExit(map[string]any{
			"schema": "dalek.run.request.v1",
			"run":    res,
		})
		return
	}
	fmt.Printf("run_id=%d request_id=%s status=%s\n", res.RunID, res.RequestID, res.RunStatus)
}

func cmdRunShow(args []string) {
	fs := flag.NewFlagSet("run show", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		printSubcommandUsage(
			fs,
			"查看 run 详情与聚合状态",
			"dalek run show --id <id> [--output text|json]",
			"dalek run show --id 1",
			"dalek run show --id 1 -o json",
		)
	}
	home := fs.String("home", globalHome, "dalek Home 目录（默认 ~/.dalek）")
	proj := fs.String("project", globalProject, "项目名（可选）")
	projShort := fs.String("p", globalProject, "项目名（可选）")
	runID := fs.Uint("id", 0, "run id（必填）")
	timeout := fs.Duration("timeout", 5*time.Second, "超时")
	output := addOutputFlag(fs, "输出格式: text|json（默认 text）")
	parseFlagSetOrExit(fs, normalizeTaskRunIDArgs(args), globalOutput, "run show 参数解析失败", "运行 dalek run show --help 查看参数")
	if strings.TrimSpace(*projShort) != "" {
		*proj = strings.TrimSpace(*projShort)
	}
	out := parseOutputOrExit(*output, true)
	if *runID == 0 {
		exitUsageError(out, "缺少必填参数 --id", "run show 需要 run id", "dalek run show --id 1")
	}
	if err := requirePositiveDuration("timeout", *timeout); err != nil {
		exitUsageError(out, "参数错误", err.Error(), "调整 --timeout 为正时长")
	}

	p := mustOpenProjectWithOutput(out, *home, *proj)
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	view, err := p.GetRun(ctx, uint(*runID))
	if err != nil {
		exitRuntimeError(out, "run show 失败", err.Error(), "确认 run_id 是否存在")
	}
	if view == nil {
		exitRuntimeError(out, "run 不存在", fmt.Sprintf("run_id=%d", *runID), "使用 dalek run request 或 dalek task ls 查看")
	}
	view, err = reconcileRunShowIfNeeded(ctx, *home, p, view)
	if err != nil {
		exitRuntimeError(out, "run reconcile 失败", err.Error(), "检查 daemon internal 配置或稍后重试")
	}
	taskStatus, err := p.GetTaskStatus(ctx, view.RunID)
	if err != nil {
		exitRuntimeError(out, "run show 失败", err.Error(), "确认 task status 视图可用后重试")
	}
	warnings := buildRunShowWarnings(view, taskStatus)
	if out == outputJSON {
		printJSONOrExit(map[string]any{
			"schema":      "dalek.run.show.v1",
			"run":         view,
			"task_status": taskStatus,
			"warnings":    warnings,
		})
		return
	}
	fmt.Printf("run_id: %d\n", view.RunID)
	fmt.Printf("status: %s\n", view.RunStatus)
	fmt.Printf("request_id: %s\n", view.RequestID)
	fmt.Printf("project: %s\n", view.ProjectKey)
	fmt.Printf("ticket_id: %d\n", view.TicketID)
	fmt.Printf("worker_id: %d\n", view.WorkerID)
	fmt.Printf("verify_target: %s\n", view.VerifyTarget)
	fmt.Printf("snapshot_id: %s\n", view.SnapshotID)
	fmt.Printf("base_commit: %s\n", view.BaseCommit)
	fmt.Printf("workspace_generation: %s\n", view.SourceWorkspaceGeneration)
	if taskStatus != nil {
		fmt.Printf("summary: %s\n", strings.TrimSpace(taskStatus.RuntimeSummary))
		fmt.Printf("milestone: %s\n", strings.TrimSpace(taskStatus.SemanticMilestone))
		fmt.Printf("last_event: %s\n", strings.TrimSpace(taskStatus.LastEventType))
		if note := strings.TrimSpace(taskStatus.LastEventNote); note != "" {
			fmt.Printf("last_note: %s\n", note)
		}
	}
	for _, warning := range warnings {
		if strings.TrimSpace(warning) != "" {
			fmt.Printf("hint: %s\n", strings.TrimSpace(warning))
		}
	}
}

func buildRunShowWarnings(view *app.RunView, taskStatus *app.TaskStatus) []string {
	out := make([]string, 0, 2)
	if view != nil {
		switch strings.TrimSpace(string(view.RunStatus)) {
		case "node_offline", "reconciling":
			out = append(out, "run 正在恢复中，优先检查 node 连通性与 daemon internal 配置")
		}
	}
	if taskStatus != nil && strings.TrimSpace(taskStatus.LastEventType) == "run_artifact_upload_failed" {
		out = append(out, "执行终态已确定，artifact 上传存在部分失败，请单独检查产物链路")
	}
	return out
}

func reconcileRunShowIfNeeded(ctx context.Context, homeFlag string, p *app.Project, view *app.RunView) (*app.RunView, error) {
	if view == nil {
		return nil, nil
	}
	status := strings.TrimSpace(string(view.RunStatus))
	if status != "node_offline" && status != "reconciling" {
		return view, nil
	}
	homeDir, err := app.ResolveHomeDir(strings.TrimSpace(homeFlag))
	if err != nil {
		return view, nil
	}
	h, err := app.OpenHome(homeDir)
	if err != nil {
		return view, nil
	}
	cfg := h.Config.WithDefaults()
	client, err := nodeagentsvc.NewClient(nodeagentsvc.ClientConfig{
		BaseURL:   "http://" + strings.TrimSpace(cfg.Daemon.Internal.Listen),
		AuthToken: strings.TrimSpace(cfg.Daemon.Internal.NodeAgentToken),
		Timeout:   5 * time.Second,
	})
	if err != nil {
		return view, nil
	}
	fetcher := runsvc.NewNodeAgentRunFetcher(client, strings.TrimSpace(p.Key()), nodeagentsvc.ProtocolVersionV1)
	if strings.TrimSpace(view.RequestID) != "" {
		if _, err := p.ReconcileRunByRequestID(ctx, fetcher, strings.TrimSpace(view.RequestID)); err == nil {
			return p.GetRun(ctx, view.RunID)
		}
	}
	if _, err := p.ReconcileRun(ctx, fetcher, view.RunID); err != nil {
		return view, err
	}
	return p.GetRun(ctx, view.RunID)
}

func cmdRunLogs(args []string) {
	fs := flag.NewFlagSet("run logs", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		printSubcommandUsage(
			fs,
			"查看 run 日志尾部",
			"dalek run logs --id <id> [--lines 20] [--output text|json]",
			"dalek run logs --id 1",
			"dalek run logs --id 1 --lines 50 -o json",
		)
	}
	home := fs.String("home", globalHome, "dalek Home 目录（默认 ~/.dalek）")
	proj := fs.String("project", globalProject, "项目名（可选）")
	projShort := fs.String("p", globalProject, "项目名（可选）")
	runID := fs.Uint("id", 0, "run id（必填）")
	lines := fs.Int("lines", 20, "返回行数上限（默认 20）")
	timeout := fs.Duration("timeout", 5*time.Second, "超时")
	output := addOutputFlag(fs, "输出格式: text|json（默认 text）")
	parseFlagSetOrExit(fs, normalizeTaskRunIDArgs(args), globalOutput, "run logs 参数解析失败", "运行 dalek run logs --help 查看参数")
	if strings.TrimSpace(*projShort) != "" {
		*proj = strings.TrimSpace(*projShort)
	}
	out := parseOutputOrExit(*output, true)
	if *runID == 0 {
		exitUsageError(out, "缺少必填参数 --id", "run logs 需要 run id", "dalek run logs --id 1")
	}
	if err := requirePositiveDuration("timeout", *timeout); err != nil {
		exitUsageError(out, "参数错误", err.Error(), "调整 --timeout 为正时长")
	}
	if *lines < 0 {
		exitUsageError(out, "参数错误", "--lines 不能为负数", "调整 --lines 为正整数")
	}

	p := mustOpenProjectWithOutput(out, *home, *proj)
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	logs, err := p.GetRunLogs(ctx, uint(*runID), *lines)
	if err != nil {
		exitRuntimeError(out, "run logs 失败", err.Error(), "确认 run_id 是否存在")
	}
	if !logs.Found {
		exitRuntimeError(out, "run 不存在", fmt.Sprintf("run_id=%d", *runID), "使用 dalek run request 或 dalek task ls 查看")
	}
	if out == outputJSON {
		printJSONOrExit(map[string]any{
			"schema": "dalek.run.logs.v1",
			"logs":   logs,
		})
		return
	}
	fmt.Println(logs.Tail)
}

func cmdRunArtifact(args []string) {
	if len(args) == 0 {
		printRunArtifactUsage()
		os.Exit(2)
	}
	sub := strings.TrimSpace(args[0])
	switch sub {
	case "ls", "list":
		cmdRunArtifactList(args[1:])
	case "help", "-h", "--help":
		printRunArtifactUsage()
		os.Exit(0)
	default:
		exitUsageError(globalOutput,
			fmt.Sprintf("未知 run artifact 子命令: %s", sub),
			"run artifact 命令组仅支持 ls",
			"运行 dalek run artifact --help 查看可用命令",
		)
	}
}

func printRunArtifactUsage() {
	printGroupUsage("Run 产物索引", "dalek run artifact <command> [flags]", []string{
		"ls   查看 run 产物索引与 artifact issues",
	})
	fmt.Fprintln(os.Stderr, "Use \"dalek run artifact <command> --help\" for more information.")
}

func cmdRunArtifactList(args []string) {
	fs := flag.NewFlagSet("run artifact ls", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		printSubcommandUsage(
			fs,
			"查看 run 产物索引与 artifact issues",
			"dalek run artifact ls --id <id> [--output text|json]",
			"dalek run artifact ls --id 1",
			"dalek run artifact ls --id 1 -o json",
		)
	}
	home := fs.String("home", globalHome, "dalek Home 目录（默认 ~/.dalek）")
	proj := fs.String("project", globalProject, "项目名（可选）")
	projShort := fs.String("p", globalProject, "项目名（可选）")
	runID := fs.Uint("id", 0, "run id（必填）")
	timeout := fs.Duration("timeout", 5*time.Second, "超时")
	output := addOutputFlag(fs, "输出格式: text|json（默认 text）")
	parseFlagSetOrExit(fs, normalizeTaskRunIDArgs(args), globalOutput, "run artifact ls 参数解析失败", "运行 dalek run artifact ls --help 查看参数")
	if strings.TrimSpace(*projShort) != "" {
		*proj = strings.TrimSpace(*projShort)
	}
	out := parseOutputOrExit(*output, true)
	if *runID == 0 {
		exitUsageError(out, "缺少必填参数 --id", "run artifact ls 需要 run id", "dalek run artifact ls --id 1")
	}
	if err := requirePositiveDuration("timeout", *timeout); err != nil {
		exitUsageError(out, "参数错误", err.Error(), "调整 --timeout 为正时长")
	}

	p := mustOpenProjectWithOutput(out, *home, *proj)
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	artifacts, err := p.ListRunArtifacts(ctx, uint(*runID))
	if err != nil {
		exitRuntimeError(out, "run artifact ls 失败", err.Error(), "确认 run_id 是否存在")
	}
	if !artifacts.Found {
		exitRuntimeError(out, "run 不存在", fmt.Sprintf("run_id=%d", *runID), "使用 dalek run request 或 dalek task ls 查看")
	}
	if out == outputJSON {
		warnings := buildRunArtifactWarnings(artifacts)
		printJSONOrExit(map[string]any{
			"schema":    "dalek.run.artifacts.v1",
			"artifacts": artifacts,
			"warnings":  warnings,
		})
		return
	}
	if len(artifacts.Artifacts) == 0 {
		if len(artifacts.Issues) == 0 {
			fmt.Println("(empty)")
			return
		}
		fmt.Println("(no indexed artifacts)")
	} else {
		for _, art := range artifacts.Artifacts {
			fmt.Printf("%s\t%s\t%s\n", strings.TrimSpace(art.Name), strings.TrimSpace(art.Kind), strings.TrimSpace(art.Ref))
		}
	}
	for _, issue := range artifacts.Issues {
		fmt.Printf("WARN\t%s\t%s\t%s\n",
			emptyAsDash(strings.TrimSpace(issue.Name)),
			emptyAsDash(strings.TrimSpace(issue.Status)),
			emptyAsDash(strings.TrimSpace(issue.Reason)),
		)
	}
}

func buildRunArtifactWarnings(artifacts app.RunArtifacts) []string {
	out := make([]string, 0, 2)
	if len(artifacts.Artifacts) == 0 && len(artifacts.Issues) > 0 {
		out = append(out, "当前没有可用产物索引，但存在 artifact 上传失败记录")
	}
	if len(artifacts.Issues) > 0 {
		out = append(out, fmt.Sprintf("发现 %d 条 artifact issue", len(artifacts.Issues)))
	}
	return out
}

func cmdRunCancel(args []string) {
	fs := flag.NewFlagSet("run cancel", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		printSubcommandUsage(
			fs,
			"取消 run",
			"dalek run cancel --id <id> [--output text|json]",
			"dalek run cancel --id 1",
			"dalek run cancel --id 1 -o json",
		)
	}
	home := fs.String("home", globalHome, "dalek Home 目录（默认 ~/.dalek）")
	proj := fs.String("project", globalProject, "项目名（可选）")
	projShort := fs.String("p", globalProject, "项目名（可选）")
	runID := fs.Uint("id", 0, "run id（必填）")
	timeout := fs.Duration("timeout", 5*time.Second, "超时")
	output := addOutputFlag(fs, "输出格式: text|json（默认 text）")
	parseFlagSetOrExit(fs, normalizeTaskRunIDArgs(args), globalOutput, "run cancel 参数解析失败", "运行 dalek run cancel --help 查看参数")
	if strings.TrimSpace(*projShort) != "" {
		*proj = strings.TrimSpace(*projShort)
	}
	out := parseOutputOrExit(*output, true)
	if *runID == 0 {
		exitUsageError(out, "缺少必填参数 --id", "run cancel 需要 run id", "dalek run cancel --id 1")
	}
	if err := requirePositiveDuration("timeout", *timeout); err != nil {
		exitUsageError(out, "参数错误", err.Error(), "调整 --timeout 为正时长")
	}

	p := mustOpenProjectWithOutput(out, *home, *proj)
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	res, err := p.CancelRun(ctx, uint(*runID))
	if err != nil {
		exitRuntimeError(out, "run cancel 失败", err.Error(), "确认 run_id 是否存在")
	}
	if !res.Found {
		exitRuntimeError(out, "run 不存在", fmt.Sprintf("run_id=%d", *runID), "使用 dalek run request 或 dalek task ls 查看")
	}
	if out == outputJSON {
		printJSONOrExit(map[string]any{
			"schema": "dalek.run.cancel.v1",
			"result": res,
		})
		return
	}
	fmt.Printf("run_id=%d canceled=%t reason=%s\n", res.RunID, res.Canceled, strings.TrimSpace(res.Reason))
}
