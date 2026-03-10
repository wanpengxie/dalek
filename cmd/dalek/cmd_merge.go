package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"dalek/internal/contracts"
)

type mergeStatusPayload struct {
	TicketID          uint
	WorkflowStatus    string
	IntegrationStatus string
	MergeAnchorSHA    string
	TargetRef         string
	MergedAt          string
	AbandonedReason   string
}

func cmdMerge(args []string) {
	if len(args) == 0 {
		printMergeUsage()
		os.Exit(2)
	}
	sub := strings.TrimSpace(args[0])
	switch sub {
	case "ls":
		cmdMergeList(args[1:])
	case "status":
		cmdMergeStatus(args[1:])
	case "abandon":
		cmdMergeAbandon(args[1:])
	case "sync-ref":
		cmdMergeSyncRef(args[1:])
	case "retarget":
		cmdMergeRetarget(args[1:])
	case "rescan":
		cmdMergeRescan(args[1:])
	case "help", "-h", "--help":
		printMergeUsage()
		os.Exit(0)
	default:
		exitUsageError(globalOutput,
			fmt.Sprintf("未知 merge 子命令: %s", sub),
			"merge 命令组仅支持 ls|status|abandon|sync-ref|retarget|rescan",
			"运行 dalek merge --help 查看可用命令",
		)
	}
}

func printMergeUsage() {
	printGroupUsage("Ticket merge 状态管理", "dalek merge <command> [flags]", []string{
		"ls         列出 done tickets 的 merge 状态",
		"status     查看单个 ticket 的 merge 状态",
		"abandon    手动放弃 ticket merge",
		"sync-ref   按 git ref 更新同步 merge 状态（hook 入口）",
		"retarget   修改 ticket 的 target_ref（仅 needs_merge）",
		"rescan     重新扫描 needs_merge tickets 的 merge 状态",
	})
	fmt.Fprintln(os.Stderr, "Use \"dalek merge <command> --help\" for more information.")
}

func cmdMergeList(args []string) {
	fs := flag.NewFlagSet("merge ls", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		printSubcommandUsage(
			fs,
			"列出 done tickets 的 merge 状态",
			"dalek merge ls [--status needs_merge|merged|abandoned] [-n 50] [--output text|json]",
			"dalek merge ls",
			"dalek merge ls --status needs_merge",
			"dalek merge ls -o json",
		)
	}
	home := fs.String("home", globalHome, "dalek Home 目录（默认 ~/.dalek）")
	proj := fs.String("project", globalProject, "项目名（可选）")
	projShort := fs.String("p", globalProject, "项目名（可选）")
	statusRaw := fs.String("status", "", "过滤状态（可选）：needs_merge|merged|abandoned")
	limit := fs.Int("n", 50, "最多显示条数")
	output := addOutputFlag(fs, "输出格式: text|json（默认 text）")
	parseFlagSetOrExit(fs, args, globalOutput, "merge ls 参数解析失败", "运行 dalek merge ls --help 查看参数")
	if strings.TrimSpace(*projShort) != "" {
		*proj = strings.TrimSpace(*projShort)
	}
	out := parseOutputOrExit(*output, true)
	if *limit <= 0 {
		exitUsageError(out,
			"非法参数 --n",
			"--n 必须大于 0",
			"例如: dalek merge ls -n 50",
		)
	}
	statusFilter, err := parseMergeIntegrationStatus(*statusRaw)
	if err != nil {
		exitUsageError(out,
			"非法参数 --status",
			err.Error(),
			"改为 needs_merge|merged|abandoned 之一",
		)
	}

	p := mustOpenProjectWithOutput(out, *home, *proj)
	tickets, err := p.ListTickets(context.Background(), true)
	if err != nil {
		exitRuntimeError(out,
			"查询 merge 列表失败",
			err.Error(),
			"稍后重试，或检查项目数据库状态",
		)
	}

	items := make([]mergeStatusPayload, 0, minInt(len(tickets), *limit))
	for _, tk := range tickets {
		if contracts.CanonicalTicketWorkflowStatus(tk.WorkflowStatus) != contracts.TicketDone {
			continue
		}
		status := contracts.CanonicalIntegrationStatus(tk.IntegrationStatus)
		if status == contracts.IntegrationNone {
			continue
		}
		if statusFilter != contracts.IntegrationNone && status != statusFilter {
			continue
		}
		items = append(items, buildMergeStatusPayload(tk))
		if len(items) >= *limit {
			break
		}
	}

	if out == outputJSON {
		printJSONOrExit(map[string]any{
			"schema": "dalek.merge.list.v1",
			"items":  items,
		})
		return
	}
	if len(items) == 0 {
		fmt.Println("(empty)")
		return
	}
	for _, it := range items {
		fmt.Printf("t%d  workflow=%s  merge=%s  anchor=%s  target=%s  merged_at=%s\n",
			it.TicketID,
			emptyFallback(it.WorkflowStatus, "-"),
			emptyFallback(it.IntegrationStatus, "-"),
			emptyFallback(it.MergeAnchorSHA, "-"),
			emptyFallback(it.TargetRef, "-"),
			emptyFallback(it.MergedAt, "-"),
		)
	}
}

func cmdMergeStatus(args []string) {
	fs := flag.NewFlagSet("merge status", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		printSubcommandUsage(
			fs,
			"查看单个 ticket 的 merge 状态",
			"dalek merge status --ticket <id> [--timeout 5s] [--output text|json]",
			"dalek merge status --ticket 1",
			"dalek merge status --ticket 1 -o json",
		)
	}
	home := fs.String("home", globalHome, "dalek Home 目录（默认 ~/.dalek）")
	proj := fs.String("project", globalProject, "项目名（可选）")
	projShort := fs.String("p", globalProject, "项目名（可选）")
	ticketID := fs.Uint("ticket", 0, "ticket ID (required)")
	fs.UintVar(ticketID, "t", 0, "ticket ID (required)")
	timeout := fs.Duration("timeout", 5*time.Second, "超时（默认 5s）")
	output := addOutputFlag(fs, "输出格式: text|json（默认 text）")
	parseFlagSetOrExit(fs, args, globalOutput, "merge status 参数解析失败", "运行 dalek merge status --help 查看参数")
	if strings.TrimSpace(*projShort) != "" {
		*proj = strings.TrimSpace(*projShort)
	}
	out := parseOutputOrExit(*output, true)
	if *ticketID == 0 {
		exitUsageError(out,
			"缺少必填参数 --ticket",
			"merge status 需要 ticket ID",
			"dalek merge status --ticket 1",
		)
	}
	if *timeout <= 0 {
		exitUsageError(out,
			"非法参数 --timeout",
			"--timeout 必须大于 0",
			"例如: dalek merge status --ticket 1 --timeout 5s",
		)
	}

	payload := mustLoadMergeStatus(out, *home, *proj, uint(*ticketID), *timeout)
	if out == outputJSON {
		printJSONOrExit(map[string]any{
			"schema":             "dalek.merge.status.v1",
			"ticket_id":          payload.TicketID,
			"workflow_status":    payload.WorkflowStatus,
			"integration_status": payload.IntegrationStatus,
			"merge_anchor_sha":   payload.MergeAnchorSHA,
			"target_ref":         payload.TargetRef,
			"target_branch":      payload.TargetRef,
			"merged_at":          payload.MergedAt,
			"abandoned_reason":   payload.AbandonedReason,
		})
		return
	}
	fmt.Printf("ticket:\tt%d\n", payload.TicketID)
	fmt.Printf("workflow:\t%s\n", emptyFallback(payload.WorkflowStatus, "-"))
	fmt.Printf("merge:\t%s\n", emptyFallback(payload.IntegrationStatus, "-"))
	fmt.Printf("anchor:\t%s\n", emptyFallback(payload.MergeAnchorSHA, "-"))
	fmt.Printf("target:\t%s\n", emptyFallback(payload.TargetRef, "-"))
	fmt.Printf("merged_at:\t%s\n", emptyFallback(payload.MergedAt, "-"))
	fmt.Printf("reason:\t%s\n", emptyFallback(payload.AbandonedReason, "-"))
}

func cmdMergeAbandon(args []string) {
	fs := flag.NewFlagSet("merge abandon", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		printSubcommandUsage(
			fs,
			"手动放弃 ticket merge",
			"dalek merge abandon --ticket <id> --reason \"...\" [--timeout 5s] [--output text|json]",
			"dalek merge abandon --ticket 1 --reason \"不再需要合并\"",
			"dalek merge abandon --ticket 1 --reason \"需求已变更\" -o json",
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
	parseFlagSetOrExit(fs, args, globalOutput, "merge abandon 参数解析失败", "运行 dalek merge abandon --help 查看参数")
	if strings.TrimSpace(*projShort) != "" {
		*proj = strings.TrimSpace(*projShort)
	}
	out := parseOutputOrExit(*output, true)
	if *ticketID == 0 {
		exitUsageError(out,
			"缺少必填参数 --ticket",
			"merge abandon 需要 ticket ID",
			"dalek merge abandon --ticket 1 --reason \"需求变更\"",
		)
	}
	if strings.TrimSpace(*reason) == "" {
		exitUsageError(out,
			"缺少必填参数 --reason",
			"merge abandon 需要说明原因",
			"dalek merge abandon --ticket 1 --reason \"需求变更\"",
		)
	}
	if *timeout <= 0 {
		exitUsageError(out,
			"非法参数 --timeout",
			"--timeout 必须大于 0",
			"例如: dalek merge abandon --ticket 1 --reason \"需求变更\" --timeout 5s",
		)
	}

	p := mustOpenProjectWithOutput(out, *home, *proj)
	ctx, cancel := projectCtx(*timeout)
	defer cancel()
	if err := p.AbandonTicketIntegration(ctx, uint(*ticketID), strings.TrimSpace(*reason)); err != nil {
		exitRuntimeError(out,
			"abandon ticket merge 失败",
			err.Error(),
			"确认 ticket 已 done 且 integration_status 为 needs_merge/merged 后重试",
		)
	}

	if out == outputJSON {
		printJSONOrExit(map[string]any{
			"schema":             "dalek.merge.abandon.v1",
			"ticket_id":          uint(*ticketID),
			"integration_status": string(contracts.IntegrationAbandoned),
			"abandoned_reason":   strings.TrimSpace(*reason),
		})
		return
	}
	fmt.Printf("ticket t%d merge abandoned: %s\n", *ticketID, strings.TrimSpace(*reason))
}

func cmdMergeSyncRef(args []string) {
	fs := flag.NewFlagSet("merge sync-ref", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		printSubcommandUsage(
			fs,
			"按 ref 更新同步 done tickets 的 merge 状态（hook 入口）",
			"dalek merge sync-ref --ref refs/heads/main --old <sha> --new <sha> [--timeout 5s] [--output text|json]",
			"dalek merge sync-ref --ref refs/heads/main --old 0000000 --new a1b2c3d",
			"dalek merge sync-ref --ref main --old 1111111 --new 2222222 -o json",
		)
	}
	home := fs.String("home", globalHome, "dalek Home 目录（默认 ~/.dalek）")
	proj := fs.String("project", globalProject, "项目名（可选）")
	projShort := fs.String("p", globalProject, "项目名（可选）")
	ref := fs.String("ref", "", "发生更新的 ref（required）")
	oldSHA := fs.String("old", "", "更新前 commit（可选）")
	newSHA := fs.String("new", "", "更新后 commit（required）")
	timeout := fs.Duration("timeout", 5*time.Second, "超时（默认 5s）")
	output := addOutputFlag(fs, "输出格式: text|json（默认 text）")
	parseFlagSetOrExit(fs, args, globalOutput, "merge sync-ref 参数解析失败", "运行 dalek merge sync-ref --help 查看参数")
	if strings.TrimSpace(*projShort) != "" {
		*proj = strings.TrimSpace(*projShort)
	}
	out := parseOutputOrExit(*output, true)
	if strings.TrimSpace(*ref) == "" {
		exitUsageError(out,
			"缺少必填参数 --ref",
			"merge sync-ref 需要 ref",
			"dalek merge sync-ref --ref refs/heads/main --old <sha> --new <sha>",
		)
	}
	if strings.TrimSpace(*newSHA) == "" {
		exitUsageError(out,
			"缺少必填参数 --new",
			"merge sync-ref 需要更新后的 commit",
			"dalek merge sync-ref --ref refs/heads/main --old <sha> --new <sha>",
		)
	}
	if *timeout <= 0 {
		exitUsageError(out,
			"非法参数 --timeout",
			"--timeout 必须大于 0",
			"例如: dalek merge sync-ref --ref refs/heads/main --new <sha> --timeout 5s",
		)
	}

	p := mustOpenProjectWithOutput(out, *home, *proj)
	ctx, cancel := projectCtx(*timeout)
	defer cancel()
	res, err := p.SyncMergeRef(ctx, *ref, *oldSHA, *newSHA)
	if err != nil {
		exitRuntimeError(out,
			"sync-ref 失败",
			err.Error(),
			"检查 ref/sha 参数后重试",
		)
	}
	if out == outputJSON {
		printJSONOrExit(map[string]any{
			"schema":            "dalek.merge.sync_ref.v1",
			"ref":               res.Ref,
			"old_sha":           res.OldSHA,
			"new_sha":           res.NewSHA,
			"candidate_tickets": res.CandidateTickets,
			"merged_ticket_ids": res.MergedTicketIDs,
			"errors":            res.Errors,
		})
		return
	}
	fmt.Printf("ref=%s candidates=%d merged=%d\n", emptyFallback(res.Ref, "-"), res.CandidateTickets, len(res.MergedTicketIDs))
	if len(res.MergedTicketIDs) > 0 {
		fmt.Printf("merged_ticket_ids=%v\n", res.MergedTicketIDs)
	}
	if len(res.Errors) > 0 {
		fmt.Printf("errors=%d\n", len(res.Errors))
	}
}

func cmdMergeRetarget(args []string) {
	fs := flag.NewFlagSet("merge retarget", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		printSubcommandUsage(
			fs,
			"修改 ticket 的 target_ref（仅 done+needs_merge）",
			"dalek merge retarget --ticket <id> --ref refs/heads/main [--timeout 5s] [--output text|json]",
			"dalek merge retarget --ticket 1 --ref refs/heads/release",
			"dalek merge retarget --ticket 1 --ref main -o json",
		)
	}
	home := fs.String("home", globalHome, "dalek Home 目录（默认 ~/.dalek）")
	proj := fs.String("project", globalProject, "项目名（可选）")
	projShort := fs.String("p", globalProject, "项目名（可选）")
	ticketID := fs.Uint("ticket", 0, "ticket ID (required)")
	fs.UintVar(ticketID, "t", 0, "ticket ID (required)")
	targetRef := fs.String("ref", "", "新的 target_ref（required）")
	timeout := fs.Duration("timeout", 5*time.Second, "超时（默认 5s）")
	output := addOutputFlag(fs, "输出格式: text|json（默认 text）")
	parseFlagSetOrExit(fs, args, globalOutput, "merge retarget 参数解析失败", "运行 dalek merge retarget --help 查看参数")
	if strings.TrimSpace(*projShort) != "" {
		*proj = strings.TrimSpace(*projShort)
	}
	out := parseOutputOrExit(*output, true)
	if *ticketID == 0 {
		exitUsageError(out,
			"缺少必填参数 --ticket",
			"merge retarget 需要 ticket ID",
			"dalek merge retarget --ticket 1 --ref refs/heads/main",
		)
	}
	if strings.TrimSpace(*targetRef) == "" {
		exitUsageError(out,
			"缺少必填参数 --ref",
			"merge retarget 需要目标 ref",
			"dalek merge retarget --ticket 1 --ref refs/heads/main",
		)
	}
	if *timeout <= 0 {
		exitUsageError(out,
			"非法参数 --timeout",
			"--timeout 必须大于 0",
			"例如: dalek merge retarget --ticket 1 --ref refs/heads/main --timeout 5s",
		)
	}

	p := mustOpenProjectWithOutput(out, *home, *proj)
	ctx, cancel := projectCtx(*timeout)
	defer cancel()
	res, err := p.RetargetTicketIntegration(ctx, uint(*ticketID), *targetRef)
	if err != nil {
		exitRuntimeError(out,
			"retarget 失败",
			err.Error(),
			"确认 ticket 为 done+needs_merge，并检查 --ref",
		)
	}
	if out == outputJSON {
		printJSONOrExit(map[string]any{
			"schema":       "dalek.merge.retarget.v1",
			"ticket_id":    res.TicketID,
			"previous_ref": res.PreviousRef,
			"target_ref":   res.TargetRef,
		})
		return
	}
	fmt.Printf("ticket t%d retarget: %s -> %s\n", res.TicketID, emptyFallback(res.PreviousRef, "-"), emptyFallback(res.TargetRef, "-"))
}

func cmdMergeRescan(args []string) {
	fs := flag.NewFlagSet("merge rescan", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		printSubcommandUsage(
			fs,
			"重扫 needs_merge tickets，按 ref 事实推进 merged",
			"dalek merge rescan [--ref refs/heads/main] [--timeout 10s] [--output text|json]",
			"dalek merge rescan",
			"dalek merge rescan --ref refs/heads/main -o json",
		)
	}
	home := fs.String("home", globalHome, "dalek Home 目录（默认 ~/.dalek）")
	proj := fs.String("project", globalProject, "项目名（可选）")
	projShort := fs.String("p", globalProject, "项目名（可选）")
	targetRef := fs.String("ref", "", "仅扫描指定 target_ref（可选）")
	timeout := fs.Duration("timeout", 10*time.Second, "超时（默认 10s）")
	output := addOutputFlag(fs, "输出格式: text|json（默认 text）")
	parseFlagSetOrExit(fs, args, globalOutput, "merge rescan 参数解析失败", "运行 dalek merge rescan --help 查看参数")
	if strings.TrimSpace(*projShort) != "" {
		*proj = strings.TrimSpace(*projShort)
	}
	out := parseOutputOrExit(*output, true)
	if *timeout <= 0 {
		exitUsageError(out,
			"非法参数 --timeout",
			"--timeout 必须大于 0",
			"例如: dalek merge rescan --timeout 10s",
		)
	}

	p := mustOpenProjectWithOutput(out, *home, *proj)
	ctx, cancel := projectCtx(*timeout)
	defer cancel()
	res, err := p.RescanTicketMergeStatus(ctx, *targetRef)
	if err != nil {
		exitRuntimeError(out,
			"rescan 失败",
			err.Error(),
			"检查 ref 参数后重试",
		)
	}
	if out == outputJSON {
		printJSONOrExit(map[string]any{
			"schema":     "dalek.merge.rescan.v1",
			"ref_filter": res.RefFilter,
			"results":    res.Results,
			"errors":     res.Errors,
		})
		return
	}
	totalMerged := 0
	for _, item := range res.Results {
		totalMerged += len(item.MergedTicketIDs)
	}
	fmt.Printf("rescan refs=%d merged=%d\n", len(res.Results), totalMerged)
	if len(res.Errors) > 0 {
		fmt.Printf("errors=%d\n", len(res.Errors))
	}
}

func parseMergeIntegrationStatus(raw string) (contracts.IntegrationStatus, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return contracts.IntegrationNone, nil
	}
	status := contracts.CanonicalIntegrationStatus(contracts.IntegrationStatus(raw))
	switch status {
	case contracts.IntegrationNeedsMerge, contracts.IntegrationMerged, contracts.IntegrationAbandoned:
		return status, nil
	default:
		return contracts.IntegrationNone, fmt.Errorf("只支持 needs_merge|merged|abandoned")
	}
}

func mustLoadMergeStatus(out cliOutputFormat, home, project string, ticketID uint, timeout time.Duration) mergeStatusPayload {
	p := mustOpenProjectWithOutput(out, home, project)
	ctx, cancel := projectCtx(timeout)
	defer cancel()
	view, err := p.GetTicketViewByID(ctx, ticketID)
	if err != nil {
		exitRuntimeError(out,
			"读取 ticket merge 状态失败",
			err.Error(),
			"确认 ticket 存在后重试",
		)
	}
	if view == nil {
		exitRuntimeError(out,
			"读取 ticket merge 状态失败",
			"ticket 不存在",
			"确认 ticket ID 后重试",
		)
	}
	return buildMergeStatusPayload(view.Ticket)
}

func buildMergeStatusPayload(tk contracts.Ticket) mergeStatusPayload {
	return mergeStatusPayload{
		TicketID:          tk.ID,
		WorkflowStatus:    string(contracts.CanonicalTicketWorkflowStatus(tk.WorkflowStatus)),
		IntegrationStatus: string(contracts.CanonicalIntegrationStatus(tk.IntegrationStatus)),
		MergeAnchorSHA:    strings.TrimSpace(tk.MergeAnchorSHA),
		TargetRef:         strings.TrimSpace(tk.TargetBranch),
		MergedAt:          formatMergeTimestamp(tk.MergedAt),
		AbandonedReason:   strings.TrimSpace(tk.AbandonedReason),
	}
}

func formatMergeTimestamp(ts *time.Time) string {
	if ts == nil || ts.IsZero() {
		return ""
	}
	return ts.Local().Format("2006-01-02 15:04:05")
}

func emptyFallback(v, fallback string) string {
	if strings.TrimSpace(v) == "" {
		return fallback
	}
	return strings.TrimSpace(v)
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
