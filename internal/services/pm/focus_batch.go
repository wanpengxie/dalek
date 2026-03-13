package pm

import (
	"context"
	"fmt"
	"strings"
	"time"

	"dalek/internal/contracts"
)

const (
	focusPollInterval    = 10 * time.Second
	focusMaxConflictRetry = 3
)

// RunBatchFocus 执行 batch focus 主循环。阻塞直到完成/blocked/canceled。
func (s *Service) RunBatchFocus(ctx context.Context, focus *contracts.FocusRun) error {
	focus.Status = contracts.FocusRunning
	if err := s.updateFocusRun(ctx, focus); err != nil {
		return err
	}

	ticketIDs, err := parseScopeTicketIDs(focus.ScopeTicketIDs)
	if err != nil {
		return s.finishFocusRun(ctx, focus, contracts.FocusFailed, "解析 scope 失败: "+err.Error())
	}

	s.slog().Info("focus batch: starting",
		"focus_id", focus.ID,
		"scope", ticketIDs,
		"budget", focus.AgentBudget,
	)

	for i, ticketID := range ticketIDs {
		if ctx.Err() != nil {
			return s.finishFocusRun(ctx, focus, contracts.FocusCanceled, "用户中断")
		}

		focus.ActiveTicketID = &ticketID
		if err := s.updateFocusRun(ctx, focus); err != nil {
			s.slog().Warn("focus batch: update active ticket failed", "error", err)
		}

		s.slog().Info("focus batch: processing ticket",
			"focus_id", focus.ID,
			"ticket_id", ticketID,
			"progress", fmt.Sprintf("%d/%d", i, len(ticketIDs)),
		)

		result := s.processTicket(ctx, focus, ticketID)
		switch result {
		case ticketResultMerged:
			focus.CompletedCount++
			s.slog().Info("focus batch: ticket completed", "ticket_id", ticketID)
		case ticketResultWaitUser:
			return s.finishFocusRun(ctx, focus, contracts.FocusBlocked,
				fmt.Sprintf("ticket T%d 需要用户介入", ticketID))
		case ticketResultBudgetExhausted:
			return s.finishFocusRun(ctx, focus, contracts.FocusBlocked,
				fmt.Sprintf("PM agent 预算耗尽（ticket T%d）", ticketID))
		case ticketResultError:
			return s.finishFocusRun(ctx, focus, contracts.FocusFailed,
				fmt.Sprintf("ticket T%d 处理失败", ticketID))
		}

		if err := s.updateFocusRun(ctx, focus); err != nil {
			s.slog().Warn("focus batch: update progress failed", "error", err)
		}
	}

	focus.ActiveTicketID = nil
	return s.finishFocusRun(ctx, focus, contracts.FocusCompleted,
		fmt.Sprintf("batch 完成：%d/%d tickets merged", focus.CompletedCount, focus.TotalCount))
}

type ticketResult int

const (
	ticketResultMerged ticketResult = iota
	ticketResultWaitUser
	ticketResultBudgetExhausted
	ticketResultError
)

func (s *Service) processTicket(ctx context.Context, focus *contracts.FocusRun, ticketID uint) ticketResult {
	// 1. Start ticket（幂等：已 queued/active 则跳过）
	if !s.isTicketActive(ctx, ticketID) {
		if _, err := s.StartTicket(ctx, ticketID); err != nil {
			s.slog().Warn("focus batch: start ticket failed", "ticket_id", ticketID, "error", err)
			return ticketResultError
		}
	}

	// 2. 等待 ticket 完成
	outcome := s.waitTicketOutcome(ctx, ticketID)

	// 3. 处理结果
	switch outcome {
	case outcomeTicketDone:
		// 直接进入 merge
	case outcomeTicketBlocked, outcomeTicketFailed:
		if focus.AgentBudget <= 0 {
			return ticketResultBudgetExhausted
		}
		action := s.triageBlockedTicket(ctx, focus, ticketID, outcome)
		switch action {
		case "restart":
			// 重新 start 并重新处理
			return s.processTicket(ctx, focus, ticketID)
		case "skip_merge":
			// 跳过失败，尝试 merge 当前代码
		case "wait_user":
			return ticketResultWaitUser
		}
	case outcomeTicketCanceled:
		return ticketResultError
	}

	// 4. 执行 merge
	return s.mergeTicket(ctx, focus, ticketID)
}

func (s *Service) triageBlockedTicket(ctx context.Context, focus *contracts.FocusRun, ticketID uint, outcome ticketOutcome) string {
	summary := string(outcome)
	// 尝试获取 worker report 信息
	if report, err := s.latestWorkerReport(ctx, ticketID); err == nil && report != "" {
		summary = report
	}

	action, err := s.callPMAgentTriage(ctx, ticketID, string(outcome), summary)
	focus.AgentBudget--
	if err != nil {
		s.slog().Warn("focus batch: triage agent failed", "ticket_id", ticketID, "error", err)
		return "wait_user"
	}
	s.slog().Info("focus batch: triage decision",
		"ticket_id", ticketID,
		"action", action.Action,
		"reason", action.Reason,
	)
	return action.Action
}

func (s *Service) mergeTicket(ctx context.Context, focus *contracts.FocusRun, ticketID uint) ticketResult {
	workerBranch, err := s.workerBranchForTicket(ctx, ticketID)
	if err != nil {
		s.slog().Warn("focus batch: get worker branch failed", "ticket_id", ticketID, "error", err)
		return ticketResultError
	}
	targetBranch := s.targetBranchForTicket(ctx, ticketID)

	result, err := s.gitMergeTicketBranch(ctx, workerBranch, targetBranch)
	if err != nil {
		s.slog().Warn("focus batch: git merge failed", "ticket_id", ticketID, "error", err)
		s.gitMergeAbort(ctx)
		return ticketResultError
	}

	if result == mergeConflict {
		resolved := s.resolveConflictLoop(ctx, focus, ticketID, workerBranch)
		if !resolved {
			s.gitMergeAbort(ctx)
			if focus.AgentBudget <= 0 {
				return ticketResultBudgetExhausted
			}
			return ticketResultError
		}
	}

	// 标记 merged
	s.markTicketMerged(ctx, ticketID)
	return ticketResultMerged
}

func (s *Service) resolveConflictLoop(ctx context.Context, focus *contracts.FocusRun, ticketID uint, branch string) bool {
	for attempt := 0; attempt < focusMaxConflictRetry; attempt++ {
		if focus.AgentBudget <= 0 {
			s.slog().Warn("focus batch: agent budget exhausted during conflict resolution",
				"ticket_id", ticketID, "attempt", attempt)
			return false
		}

		s.slog().Info("focus batch: calling PM agent to resolve conflict",
			"ticket_id", ticketID, "attempt", attempt+1)

		if err := s.callPMAgentResolveConflict(ctx, ticketID, branch, attempt); err != nil {
			s.slog().Warn("focus batch: conflict resolution agent failed",
				"ticket_id", ticketID, "error", err)
		}
		focus.AgentBudget--
		_ = s.updateFocusRun(ctx, focus)

		if !s.gitHasConflicts(ctx) {
			s.slog().Info("focus batch: conflict resolved", "ticket_id", ticketID)
			return true
		}
	}

	s.slog().Warn("focus batch: conflict resolution exhausted retries", "ticket_id", ticketID)
	return false
}

// --- 辅助：等待 ticket 结果 ---

type ticketOutcome string

const (
	outcomeTicketDone     ticketOutcome = "done"
	outcomeTicketBlocked  ticketOutcome = "blocked"
	outcomeTicketFailed   ticketOutcome = "failed"
	outcomeTicketCanceled ticketOutcome = "canceled"
)

func (s *Service) waitTicketOutcome(ctx context.Context, ticketID uint) ticketOutcome {
	for {
		select {
		case <-ctx.Done():
			return outcomeTicketCanceled
		default:
		}

		t, err := s.loadTicket(ctx, ticketID)
		if err != nil {
			s.slog().Warn("focus batch: load ticket failed", "ticket_id", ticketID, "error", err)
			time.Sleep(focusPollInterval)
			continue
		}

		status := contracts.CanonicalTicketWorkflowStatus(t.WorkflowStatus)
		switch status {
		case contracts.TicketDone:
			return outcomeTicketDone
		case contracts.TicketBlocked:
			return outcomeTicketBlocked
		case contracts.TicketArchived:
			return outcomeTicketCanceled
		}

		time.Sleep(focusPollInterval)
	}
}

func (s *Service) isTicketActive(ctx context.Context, ticketID uint) bool {
	t, err := s.loadTicket(ctx, ticketID)
	if err != nil {
		return false
	}
	status := contracts.CanonicalTicketWorkflowStatus(t.WorkflowStatus)
	return status == contracts.TicketQueued || status == contracts.TicketActive
}

func (s *Service) loadTicket(ctx context.Context, ticketID uint) (contracts.Ticket, error) {
	_, db, err := s.require()
	if err != nil {
		return contracts.Ticket{}, err
	}
	var t contracts.Ticket
	err = db.WithContext(ctx).First(&t, ticketID).Error
	return t, err
}

func (s *Service) latestWorkerReport(ctx context.Context, ticketID uint) (string, error) {
	_, db, err := s.require()
	if err != nil {
		return "", err
	}
	var report struct {
		Summary string
	}
	err = db.WithContext(ctx).
		Table("task_semantic_reports").
		Select("summary").
		Joins("JOIN task_runs ON task_runs.id = task_semantic_reports.task_run_id").
		Joins("JOIN workers ON workers.id = task_runs.subject_id AND task_runs.subject_type = 'worker'").
		Where("workers.ticket_id = ?", ticketID).
		Order("task_semantic_reports.id desc").
		Limit(1).
		Scan(&report).Error
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(report.Summary), nil
}

func (s *Service) markTicketMerged(ctx context.Context, ticketID uint) {
	_, db, err := s.require()
	if err != nil {
		return
	}
	db.WithContext(ctx).
		Model(&contracts.Ticket{}).
		Where("id = ?", ticketID).
		Update("integration_status", contracts.IntegrationMerged)
}
