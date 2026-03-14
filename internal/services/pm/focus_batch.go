package pm

import (
	"context"
	"fmt"
	"strings"
	"time"

	"dalek/internal/contracts"
)

const (
	focusPollInterval     = 10 * time.Second
	focusMaxConflictRetry = 3
	focusMaxRestart       = 3
	focusTicketTimeout    = 4 * time.Hour
)

// TicketStarter 是 start ticket 的抽象——由调用方注入实现。
// CLI 层通过 daemon client API 实现，确保走 daemon 的 queue consumer + execution host。
type TicketStarter func(ctx context.Context, ticketID uint) error

// RunBatchFocus 执行 batch focus 主循环。阻塞直到完成/blocked/canceled。
// startTicket 必须通过 daemon API 实现，不能走本地 PM Service。
// softStop 用于优雅停止：关闭该 channel 后，loop 会在当前 ticket 处理完后退出。
func (s *Service) RunBatchFocus(ctx context.Context, focus *contracts.FocusRun, startTicket TicketStarter, softStop <-chan struct{}) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	s.focusCancelMu.Lock()
	s.focusCancelFn = cancel
	s.focusCancelMu.Unlock()
	defer func() {
		s.focusCancelMu.Lock()
		s.focusCancelFn = nil
		s.focusCancelMu.Unlock()
	}()

	focus.Status = contracts.FocusRunning
	if err := s.updateFocusRun(ctx, focus); err != nil {
		return err
	}

	ticketIDs, err := parseScopeTicketIDs(focus.ScopeTicketIDs)
	if err != nil {
		return s.finishFocusRun(context.Background(), focus, contracts.FocusFailed, "解析 scope 失败: "+err.Error())
	}

	s.slog().Info("focus batch: starting",
		"focus_id", focus.ID,
		"scope", ticketIDs,
		"budget", focus.AgentBudget,
	)

	for i, ticketID := range ticketIDs {
		// 检查优雅停止信号
		select {
		case <-softStop:
			return s.finishFocusRun(context.Background(), focus, contracts.FocusCanceled, "graceful stop")
		default:
		}

		if ctx.Err() != nil {
			return s.finishFocusRun(context.Background(), focus, contracts.FocusCanceled, "用户中断")
		}

		focus.ActiveTicketID = &ticketID
		_ = s.updateFocusRun(ctx, focus)

		s.slog().Info("focus batch: processing ticket",
			"focus_id", focus.ID,
			"ticket_id", ticketID,
			"progress", fmt.Sprintf("%d/%d", i+1, len(ticketIDs)),
		)

		result := s.processTicket(ctx, focus, ticketID, startTicket)
		switch result {
		case ticketResultMerged:
			focus.CompletedCount++
			s.slog().Info("focus batch: ticket completed", "ticket_id", ticketID)
		case ticketResultWaitUser:
			return s.finishFocusRun(context.Background(), focus, contracts.FocusBlocked,
				fmt.Sprintf("ticket T%d 需要用户介入", ticketID))
		case ticketResultBudgetExhausted:
			return s.finishFocusRun(context.Background(), focus, contracts.FocusBlocked,
				fmt.Sprintf("PM agent 预算耗尽（ticket T%d）", ticketID))
		case ticketResultError:
			return s.finishFocusRun(context.Background(), focus, contracts.FocusFailed,
				fmt.Sprintf("ticket T%d 处理失败", ticketID))
		}

		_ = s.updateFocusRun(ctx, focus)
	}

	focus.ActiveTicketID = nil
	return s.finishFocusRun(context.Background(), focus, contracts.FocusCompleted,
		fmt.Sprintf("batch 完成：%d/%d tickets merged", focus.CompletedCount, focus.TotalCount))
}

type ticketResult int

const (
	ticketResultMerged ticketResult = iota
	ticketResultWaitUser
	ticketResultBudgetExhausted
	ticketResultError
)

func (s *Service) processTicket(ctx context.Context, focus *contracts.FocusRun, ticketID uint, startTicket TicketStarter) ticketResult {
	restartCount := 0

	for {
		// 1. Start ticket（通过 daemon API，幂等：已 queued/active 则跳过）
		if !s.isTicketActive(ctx, ticketID) {
			if err := startTicket(ctx, ticketID); err != nil {
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
			return s.mergeTicket(ctx, focus, ticketID)

		case outcomeTicketBlocked:
			if focus.AgentBudget <= 0 {
				return ticketResultBudgetExhausted
			}
			action := s.triageBlockedTicket(ctx, focus, ticketID, outcome)
			switch action {
			case "restart":
				restartCount++
				if restartCount >= focusMaxRestart {
					s.slog().Warn("focus batch: max restart reached", "ticket_id", ticketID, "restarts", restartCount)
					return ticketResultError
				}
				s.slog().Info("focus batch: restarting ticket", "ticket_id", ticketID, "attempt", restartCount)
				continue // 回到 for 循环顶部重新 start
			case "skip_merge":
				return s.mergeTicket(ctx, focus, ticketID)
			default: // "wait_user" 或未知
				return ticketResultWaitUser
			}

		case outcomeTicketTimeout:
			s.slog().Warn("focus batch: ticket execution timed out", "ticket_id", ticketID)
			return ticketResultError

		case outcomeTicketCanceled:
			return ticketResultError
		}
	}
}

func (s *Service) triageBlockedTicket(ctx context.Context, focus *contracts.FocusRun, ticketID uint, outcome ticketOutcome) string {
	summary := string(outcome)
	if report, err := s.latestWorkerReport(ctx, ticketID); err == nil && report != "" {
		summary = report
	}

	action, _ := s.callPMAgentTriage(ctx, ticketID, string(outcome), summary)
	focus.AgentBudget--
	_ = s.updateFocusRun(ctx, focus) // 持久化 budget 变更

	s.slog().Info("focus batch: triage decision",
		"ticket_id", ticketID,
		"action", action.Action,
		"reason", action.Reason,
		"budget_remaining", focus.AgentBudget,
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

	// 标记 merged（通过 integration 路径）
	if err := s.markTicketIntegrationMerged(ctx, ticketID); err != nil {
		s.slog().Warn("focus batch: mark merged failed", "ticket_id", ticketID, "error", err)
	}
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

		if s.gitMergeClean(ctx) {
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
	outcomeTicketTimeout  ticketOutcome = "timeout"
	outcomeTicketCanceled ticketOutcome = "canceled"
)

func (s *Service) waitTicketOutcome(ctx context.Context, ticketID uint) ticketOutcome {
	deadline := time.Now().Add(focusTicketTimeout)

	for {
		select {
		case <-ctx.Done():
			return outcomeTicketCanceled
		default:
		}

		if time.Now().After(deadline) {
			return outcomeTicketTimeout
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

		// 检查 worker 是否已死但 ticket 还在 active（queued 时 worker 未启动是正常的）
		if status == contracts.TicketActive {
			if w, werr := s.worker.LatestWorker(ctx, ticketID); werr == nil && w != nil {
				if w.Status == contracts.WorkerFailed || w.Status == contracts.WorkerStopped {
					return outcomeTicketBlocked
				}
			}
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

// markTicketIntegrationMerged 通过 integration 路径标记 ticket 为 merged。
func (s *Service) markTicketIntegrationMerged(ctx context.Context, ticketID uint) error {
	_, db, err := s.require()
	if err != nil {
		return err
	}
	now := time.Now()
	return db.WithContext(ctx).
		Model(&contracts.Ticket{}).
		Where("id = ? AND integration_status = ?", ticketID, contracts.IntegrationNeedsMerge).
		Updates(map[string]any{
			"integration_status": contracts.IntegrationMerged,
			"merged_at":          &now,
		}).Error
}
