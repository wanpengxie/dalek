package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"dalek/internal/app"
	"dalek/internal/contracts"
)

const workerReportStatusHookWaitTimeout = 12 * time.Second

func cmdWorker(args []string) {
	if len(args) == 0 {
		cmdWorkerHelp()
		os.Exit(2)
	}
	sub := strings.TrimSpace(args[0])
	switch sub {
	case "report":
		cmdWorkerReport(args[1:])
	case "run":
		cmdWorkerRun(args[1:])
	case "-h", "--help", "help":
		cmdWorkerHelp()
		os.Exit(0)
	default:
		exitUsageError(globalOutput,
			fmt.Sprintf("未知 worker 子命令: %s", sub),
			"worker 命令组仅支持固定子命令",
			"运行 dalek worker --help 查看可用命令",
		)
	}
}

func cmdWorkerHelp() {
	printGroupUsage("Worker 内部命令", "dalek worker <command> [flags]", []string{
		"report    上报 worker 运行状态",
		"run       直接派发 worker（跳过 PM）",
	})
	fmt.Fprintln(os.Stderr, "Use \"dalek worker <command> --help\" for more information.")
}

func envUint(key string) uint {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return 0
	}
	var out uint
	_, _ = fmt.Sscanf(v, "%d", &out)
	return out
}

func parseBoolLoose(s string) (bool, error) {
	s = strings.TrimSpace(strings.ToLower(s))
	switch s {
	case "1", "true", "yes", "y", "on":
		return true, nil
	case "0", "false", "no", "n", "off", "":
		return false, nil
	default:
		return false, fmt.Errorf("非法 bool: %q", s)
	}
}

func cmdWorkerRun(args []string) {
	fs := flag.NewFlagSet("worker run", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		printSubcommandUsage(
			fs,
			"通过 daemon 提交 worker run",
			"dalek worker run --ticket <id> [--prompt \"...\"] [--timeout 30s] [--output text|json]",
			"dalek worker run --ticket 1",
			"dalek worker run --ticket 1 --timeout 30s",
			"dalek worker run --ticket 1 --prompt \"继续执行任务\"",
		)
	}
	home := fs.String("home", globalHome, "dalek Home 目录（默认 ~/.dalek）")
	proj := fs.String("project", globalProject, "项目名（可选）")
	projShort := fs.String("p", globalProject, "项目名（可选）")
	ticketID := fs.Uint("ticket", 0, "ticket id（必填）")
	prompt := fs.String("prompt", "", "入口 prompt（可选；为空则默认继续执行任务）")
	requestID := fs.String("request-id", "", "幂等请求 ID（可选）")
	timeout := fs.Duration("timeout", 0, "超时（可选，必须 >0）")
	output := addOutputFlag(fs, "输出格式: text|json（默认 text）")
	parseFlagSetOrExit(fs, args, globalOutput, "worker run 参数解析失败", "运行 dalek worker run --help 查看参数")
	timeoutProvided := flagProvided(fs, "timeout")
	if strings.TrimSpace(*projShort) != "" {
		*proj = strings.TrimSpace(*projShort)
	}
	out := parseOutputOrExit(*output, true)
	if *ticketID == 0 {
		exitUsageError(out,
			"缺少必填参数 --ticket",
			"worker run 需要 ticket id",
			"dalek worker run --ticket 1",
		)
	}
	if timeoutProvided && *timeout <= 0 {
		exitUsageError(out,
			"非法参数 --timeout",
			"--timeout 必须为正值",
			"例如: dalek worker run --ticket 1 --timeout 120m",
		)
	}
	enforceDispatchDepthGuardOrExit(out, "dalek worker run")
	p := mustOpenProjectWithOutput(out, *home, *proj)

	_, daemonClient := mustOpenDaemonClient(out, *home)
	ctx, cancel := projectCtx(*timeout)
	defer cancel()
	receipt, err := daemonClient.SubmitWorkerRun(ctx, app.DaemonWorkerRunSubmitRequest{
		Project:   strings.TrimSpace(p.Name()),
		TicketID:  uint(*ticketID),
		RequestID: strings.TrimSpace(*requestID),
		Prompt:    strings.TrimSpace(*prompt),
	})
	if err != nil {
		if app.IsDaemonUnavailable(err) {
			exitFixFirstError(out, 1,
				"daemon 不在线，无法异步执行 worker run",
				daemonUnavailableWorkerRunFix(uint(*ticketID)),
				daemonRuntimeErrorCause(err),
			)
		}
		exitRuntimeError(out,
			"异步 worker run 失败",
			daemonRuntimeErrorCause(err),
			"检查 daemon 日志（dalek daemon logs）后重试",
		)
	}

	if out == outputJSON {
		printJSONOrExit(map[string]any{
			"schema":      "dalek.worker.run.accepted.v1",
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

	fmt.Printf("worker run accepted: ticket=%d worker=%d request=%s run=%d\n",
		receipt.TicketID, receipt.WorkerID, strings.TrimSpace(receipt.RequestID), receipt.TaskRunID)
	fmt.Printf("query: dalek task show --id %d\n", receipt.TaskRunID)
	fmt.Printf("events: dalek task events --id %d\n", receipt.TaskRunID)
	fmt.Printf("cancel: dalek task cancel --id %d\n", receipt.TaskRunID)
}

func cmdWorkerReport(args []string) {
	fs := flag.NewFlagSet("worker report", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		printSubcommandUsage(
			fs,
			"上报 worker 运行状态",
			"dalek worker report --worker <id> [--summary ...] [--output text|json]",
			"dalek worker report --worker 1 --summary \"阶段1完成\"",
			"dalek worker report --worker 1 --needs-user true -o json",
		)
	}
	workerID := fs.Uint("worker", 0, "worker id（默认读 DALEK_WORKER_ID）")
	summary := fs.String("summary", "", "一句话摘要（可空）")
	needsUser := fs.String("needs-user", "", "是否需要人类输入（true/false；可空）")
	blockersJSON := fs.String("blockers-json", "", "blockers 的 JSON 数组（可空）")
	next := fs.String("next", "", "next_action：continue|wait_user|done（可空）")
	headSHA := fs.String("head-sha", "", "git HEAD SHA（可空；可自动推断）")
	dirty := fs.String("dirty", "", "工作区是否 dirty（true/false；可空；可自动推断）")
	timeout := fs.Duration("timeout", 10*time.Second, "超时（例如 10s）")
	output := addOutputFlag(fs, "输出格式: text|json（默认 text）")
	parseFlagSetOrExit(fs, args, globalOutput, "worker report 参数解析失败", "运行 dalek worker report --help 查看参数")
	out := parseOutputOrExit(*output, true)
	if *timeout <= 0 {
		exitUsageError(out,
			"非法参数 --timeout",
			"--timeout 必须大于 0",
			"例如: dalek worker report --worker 1 --timeout 10s",
		)
	}

	if *workerID == 0 {
		*workerID = uint(envUint("DALEK_WORKER_ID"))
	}
	if *workerID == 0 {
		exitUsageError(out,
			"缺少必填参数 --worker",
			"worker report 需要 worker id（或环境变量 DALEK_WORKER_ID）",
			"dalek worker report --worker 1 --summary \"...\"",
		)
	}

	p := mustOpenProjectWithOutput(out, globalHome, globalProject)
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	w, werr := p.WorkerByID(ctx, uint(*workerID))
	if werr != nil {
		exitRuntimeError(out,
			"读取 worker 失败",
			werr.Error(),
			"确认 worker id 正确后重试",
		)
	}

	r := contracts.WorkerReport{
		Schema:     contracts.WorkerReportSchemaV1,
		ReportedAt: time.Now().Format(time.RFC3339),
		ProjectKey: strings.TrimSpace(p.Key()),
		WorkerID:   uint(*workerID),
		TicketID:   w.TicketID,
		HeadSHA:    strings.TrimSpace(*headSHA),
		Summary:    strings.TrimSpace(*summary),
		NextAction: strings.TrimSpace(*next),
	}

	if strings.TrimSpace(*needsUser) != "" {
		b, err := parseBoolLoose(*needsUser)
		if err != nil {
			exitUsageError(out,
				"非法参数 --needs-user",
				err.Error(),
				"改为 true 或 false",
			)
		}
		r.NeedsUser = b
	}
	if strings.TrimSpace(*dirty) != "" {
		b, err := parseBoolLoose(*dirty)
		if err != nil {
			exitUsageError(out,
				"非法参数 --dirty",
				err.Error(),
				"改为 true 或 false",
			)
		}
		r.Dirty = b
	} else {
		cmd := exec.Command("git", "status", "--porcelain")
		cmd.Dir = strings.TrimSpace(w.WorktreePath)
		if output, _ := cmd.Output(); strings.TrimSpace(string(output)) != "" {
			r.Dirty = true
		}
	}
	if r.HeadSHA == "" {
		cmd := exec.Command("git", "rev-parse", "HEAD")
		cmd.Dir = strings.TrimSpace(w.WorktreePath)
		if output, _ := cmd.Output(); strings.TrimSpace(string(output)) != "" {
			r.HeadSHA = strings.TrimSpace(string(output))
		}
	}
	if strings.TrimSpace(*blockersJSON) != "" {
		var b []string
		if err := json.Unmarshal([]byte(*blockersJSON), &b); err != nil {
			exitUsageError(out,
				"非法参数 --blockers-json",
				err.Error(),
				"传入 JSON 字符串数组，例如 '[\"等待评审\"]'",
			)
		}
		r.Blockers = b
	}

	if err := p.ApplyWorkerReport(ctx, r, "cli"); err != nil {
		exitRuntimeError(out,
			"worker report 写入失败",
			err.Error(),
			"检查数据库状态后重试",
		)
	}
	waitCtx, waitCancel := context.WithTimeout(context.Background(), workerReportStatusHookWaitTimeout)
	if err := p.WaitStatusChangeHooks(waitCtx); err != nil {
		waitCancel()
		exitRuntimeError(out,
			"worker report 通知回调未完成",
			err.Error(),
			"检查 PM 状态通知链路（hook / gateway / feishu）后重试",
		)
	}
	waitCancel()

	if out == outputJSON {
		printJSONOrExit(map[string]any{
			"schema":      "dalek.worker.report.v1",
			"worker_id":   r.WorkerID,
			"ticket_id":   r.TicketID,
			"reported_at": r.ReportedAt,
			"summary":     r.Summary,
			"needs_user":  r.NeedsUser,
			"next_action": r.NextAction,
			"head_sha":    r.HeadSHA,
			"dirty":       r.Dirty,
			"blockers":    r.Blockers,
		})
		return
	}
	fmt.Println("ok")
}
