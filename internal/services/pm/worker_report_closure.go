package pm

import (
	"context"
	"fmt"
	"strings"

	"dalek/internal/contracts"
)

type workerLoopMissingReportError struct {
	Stages    int
	LastRunID uint
}

func (e *workerLoopMissingReportError) Error() string {
	if e == nil {
		return "worker 连续两轮执行完成但未提交 report next_action"
	}
	if e.LastRunID != 0 {
		return fmt.Sprintf("worker 连续两轮执行完成但未提交 report next_action（stages=%d last_run_id=%d）", e.Stages, e.LastRunID)
	}
	if e.Stages > 0 {
		return fmt.Sprintf("worker 连续两轮执行完成但未提交 report next_action（stages=%d）", e.Stages)
	}
	return "worker 连续两轮执行完成但未提交 report next_action"
}

func (s *Service) applyMissingWorkerReportWaitUser(ctx context.Context, ticketID uint, w contracts.Worker, loopResult WorkerLoopResult, source string) error {
	if ticketID == 0 {
		ticketID = w.TicketID
	}
	blockers := []string{
		"worker 未调用 dalek worker report 或 report 中缺少 next_action，请检查最近两轮执行日志与任务状态。",
	}
	if loopResult.LastRunID != 0 {
		blockers = append(blockers, fmt.Sprintf("最后一次未收口的 run_id=%d。", loopResult.LastRunID))
	}
	if loopResult.Stages > 0 {
		blockers = append(blockers, fmt.Sprintf("本轮 worker loop 已执行 %d 个 stage，并在补报重试后仍未收口。", loopResult.Stages))
	}
	report := contracts.WorkerReport{
		Schema:     contracts.WorkerReportSchemaV1,
		WorkerID:   w.ID,
		TicketID:   ticketID,
		Summary:    "worker 连续两轮执行完成但未提交 worker report，系统已自动阻塞并请求人工介入。",
		NeedsUser:  true,
		Blockers:   blockers,
		NextAction: string(contracts.NextWaitUser),
	}
	return s.ApplyWorkerReport(ctx, report, strings.TrimSpace(source))
}
