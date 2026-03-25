package pm

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"dalek/internal/contracts"

	"gorm.io/gorm"
)

// ---------------------------------------------------------------------------
// ConvergentTick — convergent mode 主入口
// ---------------------------------------------------------------------------

// ConvergentTick drives one tick of the convergent run. It is called by
// AdvanceFocusController when the active focus run has mode == "convergent".
func (s *Service) ConvergentTick(ctx context.Context, view contracts.FocusRunView) error {
	run := view.Run

	// Check desired_state for stop/cancel before dispatching.
	switch strings.TrimSpace(run.DesiredState) {
	case contracts.FocusDesiredStopping:
		return s.convergentHandleStop(ctx, run)
	case contracts.FocusDesiredCanceling:
		return s.convergentHandleCancel(ctx, run, view)
	}

	switch strings.TrimSpace(run.ConvergentPhase) {
	case "batch":
		return s.convergentTickBatch(ctx, run, view)
	case "pm_run":
		return s.convergentTickPMRun(ctx, run)
	default: // "" — new round start
		return s.convergentStartNewRound(ctx, run)
	}
}

// ---------------------------------------------------------------------------
// Phase: "" — start a new round
// ---------------------------------------------------------------------------

func (s *Service) convergentStartNewRound(ctx context.Context, run contracts.FocusRun) error {
	_, db, err := s.require()
	if err != nil {
		return err
	}

	// Determine which round we're starting.
	var lastRound contracts.ConvergentRound
	if err := db.WithContext(ctx).
		Where("focus_run_id = ?", run.ID).
		Order("round_number desc").
		First(&lastRound).Error; err != nil {
		return fmt.Errorf("convergent: 查询最新 round 失败: %w", err)
	}

	roundNumber := lastRound.RoundNumber
	var ticketIDs []uint

	if roundNumber == 1 && lastRound.BatchStatus == "pending" {
		// Round 1 was pre-created by FocusStart; use ScopeTicketIDs.
		// Items are already created. Just transition phase.
		if err := json.Unmarshal([]byte(lastRound.BatchTicketIDs), &ticketIDs); err != nil {
			return fmt.Errorf("convergent: 解析 round 1 batch_ticket_ids 失败: %w", err)
		}
		now := time.Now()
		return s.convergentSetPhaseAndEvent(ctx, db, run.ID, "batch", lastRound.ID,
			map[string]any{"batch_status": "running", "started_at": &now},
			contracts.FocusEventConvergentRoundStarted, "convergent round started",
			map[string]any{"round": roundNumber, "ticket_ids": ticketIDs})
	}

	// Round N>1: use last round's fix_ticket_ids.
	if err := json.Unmarshal([]byte(lastRound.FixTicketIDs), &ticketIDs); err != nil || len(ticketIDs) == 0 {
		return fmt.Errorf("convergent: 上一轮 fix_ticket_ids 为空或解析失败")
	}

	// Find max seq in existing items.
	var maxSeq int
	if err := db.WithContext(ctx).Model(&contracts.FocusRunItem{}).
		Where("focus_run_id = ?", run.ID).
		Select("COALESCE(MAX(seq), 0)").Scan(&maxSeq).Error; err != nil {
		return fmt.Errorf("convergent: 查询 max seq 失败: %w", err)
	}

	now := time.Now()
	newRoundNumber := roundNumber + 1
	ticketIDsJSON, _ := json.Marshal(ticketIDs)

	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Create new round record.
		newRound := contracts.ConvergentRound{
			FocusRunID:     run.ID,
			RoundNumber:    newRoundNumber,
			BatchTicketIDs: string(ticketIDsJSON),
			BatchStatus:    "running",
			PMRunStatus:    "pending",
			StartedAt:      &now,
		}
		if err := tx.WithContext(ctx).Create(&newRound).Error; err != nil {
			return err
		}

		// Create pending items for the new round's tickets.
		items := make([]contracts.FocusRunItem, 0, len(ticketIDs))
		for i, tid := range ticketIDs {
			items = append(items, contracts.FocusRunItem{
				FocusRunID: run.ID,
				Seq:        maxSeq + i + 1,
				TicketID:   tid,
				Status:     contracts.FocusItemPending,
			})
		}
		if len(items) > 0 {
			if err := tx.WithContext(ctx).Create(&items).Error; err != nil {
				return err
			}
		}

		// Update run: convergent_phase = "batch"
		if err := tx.WithContext(ctx).Model(&contracts.FocusRun{}).Where("id = ?", run.ID).Updates(map[string]any{
			"convergent_phase": "batch",
			"status":           contracts.FocusRunning,
			"updated_at":       now,
		}).Error; err != nil {
			return err
		}

		// Append event.
		if _, err := appendFocusEventTx(ctx, tx, run.ID, nil,
			contracts.FocusEventConvergentRoundStarted, "convergent round started",
			map[string]any{"round": newRoundNumber, "ticket_ids": ticketIDs}, now); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return err
	}
	s.projectWake()
	return nil
}

// ---------------------------------------------------------------------------
// Phase: "batch" — drive batch items to completion
// ---------------------------------------------------------------------------

func (s *Service) convergentTickBatch(ctx context.Context, run contracts.FocusRun, view contracts.FocusRunView) error {
	_, db, err := s.require()
	if err != nil {
		return err
	}

	// Load current round.
	round, err := s.convergentCurrentRound(ctx, db, run.ID)
	if err != nil {
		return err
	}

	// Determine which ticket IDs belong to this round.
	roundTicketIDs, err := convergentParseTicketIDs(round.BatchTicketIDs)
	if err != nil {
		return fmt.Errorf("convergent: 解析当前 round batch_ticket_ids 失败: %w", err)
	}
	roundTicketSet := make(map[uint]bool, len(roundTicketIDs))
	for _, id := range roundTicketIDs {
		roundTicketSet[id] = true
	}

	// Filter items for the current round.
	var roundItems []contracts.FocusRunItem
	for _, item := range view.Items {
		if roundTicketSet[item.TicketID] {
			roundItems = append(roundItems, item)
		}
	}

	// If there's an active (non-terminal, non-pending) item in this round, tick it.
	// But for convergent mode, a blocked item means the whole run is blocked.
	if view.ActiveItem != nil && roundTicketSet[view.ActiveItem.TicketID] {
		activeStatus := strings.TrimSpace(view.ActiveItem.Status)
		switch activeStatus {
		case contracts.FocusItemBlocked:
			return s.convergentSetTerminal(ctx, db, run, round, contracts.FocusBlocked, "batch item blocked")
		case contracts.FocusItemFailed:
			return s.convergentSetTerminal(ctx, db, run, round, contracts.FocusFailed, "batch item failed")
		default:
			return s.focusTickItem(ctx, run, *view.ActiveItem)
		}
	}

	// Check if all round items are terminal.
	allCompleted := true
	anyFailed := false
	anyBlocked := false
	pendingItem := (*contracts.FocusRunItem)(nil)

	for i := range roundItems {
		st := strings.TrimSpace(roundItems[i].Status)
		switch st {
		case contracts.FocusItemCompleted:
			// ok
		case contracts.FocusItemFailed:
			anyFailed = true
			allCompleted = false
		case contracts.FocusItemBlocked:
			anyBlocked = true
			allCompleted = false
		case contracts.FocusItemStopped, contracts.FocusItemCanceled:
			// treated as terminal but not "completed"
			allCompleted = false
		default:
			allCompleted = false
			if pendingItem == nil && (st == contracts.FocusItemPending || st == "" || st == contracts.FocusItemQueued) {
				item := roundItems[i]
				pendingItem = &item
			}
		}
	}

	// If there are still pending items, tick the next one.
	if pendingItem != nil {
		return s.focusTickPendingItem(ctx, run, *pendingItem)
	}

	// Terminal outcomes.
	if anyFailed {
		return s.convergentSetTerminal(ctx, db, run, round, contracts.FocusFailed, "batch item failed")
	}
	if anyBlocked {
		return s.convergentSetTerminal(ctx, db, run, round, contracts.FocusBlocked, "batch item blocked")
	}

	if !allCompleted {
		// Items still executing — wait for next tick.
		return nil
	}

	// All completed — batch done.
	now := time.Now()
	batchDonePayload := map[string]any{
		"round":      round.RoundNumber,
		"ticket_ids": roundTicketIDs,
	}

	// Check PM run exhaustion BEFORE transitioning.
	if run.PMRunCount >= run.MaxPMRuns {
		return s.convergentSetTerminalWithEvent(ctx, db, run, round, contracts.FocusExhausted,
			contracts.FocusEventConvergentExhausted, "convergent exhausted: PM run limit reached",
			map[string]any{
				"total_rounds":  round.RoundNumber,
				"total_pm_runs": run.PMRunCount,
				"max_pm_runs":   run.MaxPMRuns,
			})
	}

	// Transition to pm_run phase.
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.WithContext(ctx).Model(&contracts.ConvergentRound{}).Where("id = ?", round.ID).Updates(map[string]any{
			"batch_status": "completed",
			"updated_at":   now,
		}).Error; err != nil {
			return err
		}
		if err := tx.WithContext(ctx).Model(&contracts.FocusRun{}).Where("id = ?", run.ID).Updates(map[string]any{
			"convergent_phase": "pm_run",
			"updated_at":       now,
		}).Error; err != nil {
			return err
		}
		if _, err := appendFocusEventTx(ctx, tx, run.ID, nil,
			contracts.FocusEventConvergentBatchDone, "convergent batch done",
			batchDonePayload, now); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return err
	}
	s.projectWake()
	return nil
}

// ---------------------------------------------------------------------------
// Phase: "pm_run" — drive PM agent run
// ---------------------------------------------------------------------------

func (s *Service) convergentTickPMRun(ctx context.Context, run contracts.FocusRun) error {
	_, db, err := s.require()
	if err != nil {
		return err
	}

	round, err := s.convergentCurrentRound(ctx, db, run.ID)
	if err != nil {
		return err
	}

	if round.PMRunTaskRunID == nil {
		// Submit PM run.
		return s.convergentSubmitPMRun(ctx, db, run, round)
	}

	// Poll task run status.
	return s.convergentPollPMRun(ctx, db, run, round)
}

func (s *Service) convergentSubmitPMRun(ctx context.Context, db *gorm.DB, run contracts.FocusRun, round contracts.ConvergentRound) error {
	ticketIDs, _ := convergentParseTicketIDs(round.BatchTicketIDs)

	repoRoot := ""
	if p, _, err := s.require(); err == nil {
		repoRoot = strings.TrimSpace(p.RepoRoot)
	}

	reviewDir, err := ensureReviewDir(repoRoot, run.ID, round.RoundNumber)
	if err != nil {
		return fmt.Errorf("convergent: 创建 review 目录失败: %w", err)
	}

	input := PMRunInput{
		FocusID:     run.ID,
		RoundNumber: round.RoundNumber,
		TicketIDs:   ticketIDs,
		ReviewDir:   reviewDir,
	}

	// Use the service-level PM run submitter if available, otherwise create one.
	submitter := s.getPMRunSubmitter()
	result, err := s.submitPMRun(ctx, submitter, input)
	if err != nil {
		return s.convergentSetTerminal(ctx, db, run, round, contracts.FocusFailed,
			fmt.Sprintf("PM run 提交失败: %v", err),
			map[string]any{"pm_run_status": "failed"})
	}

	now := time.Now()
	taskRunID := result.TaskRunID
	newPMRunCount := run.PMRunCount + 1

	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.WithContext(ctx).Model(&contracts.ConvergentRound{}).Where("id = ?", round.ID).Updates(map[string]any{
			"pm_run_task_run_id": taskRunID,
			"pm_run_status":      "running",
			"updated_at":         now,
		}).Error; err != nil {
			return err
		}
		if err := tx.WithContext(ctx).Model(&contracts.FocusRun{}).Where("id = ?", run.ID).Updates(map[string]any{
			"pm_run_count": newPMRunCount,
			"updated_at":   now,
		}).Error; err != nil {
			return err
		}
		if _, err := appendFocusEventTx(ctx, tx, run.ID, nil,
			contracts.FocusEventConvergentPMRunStarted, "convergent PM run started",
			map[string]any{
				"round":        round.RoundNumber,
				"pm_run_count": newPMRunCount,
				"max_pm_runs":  run.MaxPMRuns,
				"task_run_id":  taskRunID,
			}, now); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return err
	}
	s.projectWake()
	return nil
}

func (s *Service) convergentPollPMRun(ctx context.Context, db *gorm.DB, run contracts.FocusRun, round contracts.ConvergentRound) error {
	if round.PMRunTaskRunID == nil {
		return fmt.Errorf("convergent: PMRunTaskRunID is nil")
	}
	taskRunID := *round.PMRunTaskRunID

	rt, err := s.taskRuntime()
	if err != nil {
		return fmt.Errorf("convergent: 获取 task runtime 失败: %w", err)
	}

	taskRun, err := rt.FindRunByID(ctx, taskRunID)
	if err != nil {
		return fmt.Errorf("convergent: 查询 task run 失败: %w", err)
	}
	if taskRun == nil {
		return fmt.Errorf("convergent: task run %d 不存在", taskRunID)
	}

	switch taskRun.OrchestrationState {
	case contracts.TaskRunning, contracts.TaskPending:
		// Still running — wait for next tick.
		return nil

	case contracts.TaskFailed, contracts.TaskCanceled:
		return s.convergentSetTerminalWithEvent(ctx, db, run, round, contracts.FocusFailed,
			contracts.FocusEventConvergentPMRunDone, "convergent PM run failed",
			map[string]any{
				"round":       round.RoundNumber,
				"task_run_id": taskRunID,
				"status":      string(taskRun.OrchestrationState),
			},
			map[string]any{"pm_run_status": "failed"})

	case contracts.TaskSucceeded:
		return s.convergentHandlePMRunDone(ctx, db, run, round)

	default:
		return nil
	}
}

func (s *Service) convergentHandlePMRunDone(ctx context.Context, db *gorm.DB, run contracts.FocusRun, round contracts.ConvergentRound) error {
	reviewDir := strings.TrimSpace(round.ReviewPath)
	if reviewDir == "" {
		repoRoot := ""
		if p, _, err := s.require(); err == nil {
			repoRoot = strings.TrimSpace(p.RepoRoot)
		}
		var err error
		reviewDir, err = ensureReviewDir(repoRoot, run.ID, round.RoundNumber)
		if err != nil {
			return s.convergentSetTerminal(ctx, db, run, round, contracts.FocusFailed,
				fmt.Sprintf("无法确定 review 目录: %v", err),
				map[string]any{"pm_run_status": "failed"})
		}
	}

	result, err := parsePMRunResult(reviewDir)
	if err != nil {
		return s.convergentSetTerminalWithEvent(ctx, db, run, round, contracts.FocusFailed,
			contracts.FocusEventConvergentPMRunDone, "convergent PM run result parse failed",
			map[string]any{
				"round": round.RoundNumber,
				"error": err.Error(),
			},
			map[string]any{"pm_run_status": "failed"})
	}

	now := time.Now()

	if result.Converged {
		// Converged! Terminal state.
		scopeIDs, _ := convergentParseTicketIDs(run.ScopeTicketIDs)
		return s.convergentSetTerminalWithEvent(ctx, db, run, round, contracts.FocusConverged,
			contracts.FocusEventConvergentConverged, "convergent converged",
			map[string]any{
				"total_rounds":     round.RoundNumber,
				"total_pm_runs":    run.PMRunCount,
				"original_tickets": scopeIDs,
				"summary":          result.Summary,
			},
			map[string]any{"pm_run_status": "done", "verdict": "converged", "review_path": reviewDir})
	}

	// Not converged — needs fix.
	if len(result.FixTicketIDs) == 0 {
		// PM agent says needs_fix but created no fix tickets → blocked.
		return s.convergentSetTerminalWithEvent(ctx, db, run, round, contracts.FocusBlocked,
			contracts.FocusEventConvergentPMRunDone, "convergent PM run needs_fix but no fix tickets",
			map[string]any{
				"round":   round.RoundNumber,
				"verdict": "needs_fix",
			},
			map[string]any{"pm_run_status": "done", "verdict": "needs_fix", "review_path": reviewDir})
	}

	// Record fix tickets and prepare for new round.
	fixTicketIDsJSON, _ := json.Marshal(result.FixTicketIDs)

	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.WithContext(ctx).Model(&contracts.ConvergentRound{}).Where("id = ?", round.ID).Updates(map[string]any{
			"pm_run_status":  "done",
			"verdict":        "needs_fix",
			"fix_ticket_ids": string(fixTicketIDsJSON),
			"review_path":    reviewDir,
			"finished_at":    &now,
			"updated_at":     now,
		}).Error; err != nil {
			return err
		}
		if err := tx.WithContext(ctx).Model(&contracts.FocusRun{}).Where("id = ?", run.ID).Updates(map[string]any{
			"convergent_phase": "", // triggers new round
			"updated_at":       now,
		}).Error; err != nil {
			return err
		}
		if _, err := appendFocusEventTx(ctx, tx, run.ID, nil,
			contracts.FocusEventConvergentPMRunDone, "convergent PM run done: needs_fix",
			map[string]any{
				"round":            round.RoundNumber,
				"verdict":          "needs_fix",
				"fix_ticket_ids":   result.FixTicketIDs,
				"effective_issues": result.EffectiveIssues,
			}, now); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return err
	}
	s.projectWake()
	return nil
}

// ---------------------------------------------------------------------------
// Stop / Cancel handling
// ---------------------------------------------------------------------------

func (s *Service) convergentHandleStop(ctx context.Context, run contracts.FocusRun) error {
	_, db, err := s.require()
	if err != nil {
		return err
	}

	switch strings.TrimSpace(run.ConvergentPhase) {
	case "batch":
		// In batch phase, delegate to FocusTick which already handles stopping.
		// It will stop pending items and wait for active ones to complete.
		return s.FocusTick(ctx)
	case "pm_run":
		// In pm_run phase: let PM agent finish, then stop.
		round, err := s.convergentCurrentRound(ctx, db, run.ID)
		if err != nil {
			return s.focusSetRunTerminal(ctx, run.ID, contracts.FocusStopped)
		}
		if round.PMRunTaskRunID == nil {
			// PM run not started yet — stop now.
			return s.convergentSetTerminal(ctx, db, run, round, contracts.FocusStopped, "stopped at pm_run phase boundary")
		}
		// PM run in progress — poll and if done, stop instead of continuing.
		rt, err := s.taskRuntime()
		if err != nil {
			return s.focusSetRunTerminal(ctx, run.ID, contracts.FocusStopped)
		}
		taskRun, err := rt.FindRunByID(ctx, *round.PMRunTaskRunID)
		if err != nil || taskRun == nil {
			return s.focusSetRunTerminal(ctx, run.ID, contracts.FocusStopped)
		}
		switch taskRun.OrchestrationState {
		case contracts.TaskSucceeded, contracts.TaskFailed, contracts.TaskCanceled:
			pmStatus := "done"
			if taskRun.OrchestrationState != contracts.TaskSucceeded {
				pmStatus = "failed"
			}
			return s.convergentSetTerminal(ctx, db, run, round, contracts.FocusStopped, "stopped after PM run completed",
				map[string]any{"pm_run_status": pmStatus})
		default:
			// Still running — wait for completion.
			return nil
		}
	default:
		// Phase boundary — stop immediately.
		return s.focusSetRunTerminal(ctx, run.ID, contracts.FocusStopped)
	}
}

func (s *Service) convergentHandleCancel(ctx context.Context, run contracts.FocusRun, view contracts.FocusRunView) error {
	_, db, err := s.require()
	if err != nil {
		return err
	}

	phase := strings.TrimSpace(run.ConvergentPhase)

	// Cancel active task runs if any.
	if phase == "pm_run" {
		round, err := s.convergentCurrentRound(ctx, db, run.ID)
		if err == nil && round.PMRunTaskRunID != nil {
			if rt, err := s.taskRuntime(); err == nil {
				_ = rt.MarkRunCanceled(ctx, *round.PMRunTaskRunID, "user_cancel", "convergent run canceled by user", time.Now())
			}
		}
	}

	// Also cancel any active batch items.
	if view.ActiveItem != nil {
		if lc := s.getFocusLoopControl(); lc != nil {
			if view.ActiveItem.CurrentTaskRunID != nil {
				_ = lc.CancelTaskRun(ctx, *view.ActiveItem.CurrentTaskRunID, contracts.TaskCancelCauseUserStop)
			}
		}
	}

	// Update round state to reflect cancellation, then terminate run + items.
	round, err := s.convergentCurrentRound(ctx, db, run.ID)
	if err != nil {
		// No round found (shouldn't happen) — fall back to run-level terminal.
		return s.focusSetRunTerminalWithOutstandingItems(ctx, run.ID, contracts.FocusCanceled, contracts.FocusItemCanceled)
	}

	var roundUpdates map[string]any
	switch phase {
	case "pm_run":
		roundUpdates = map[string]any{"pm_run_status": "canceled"}
	case "batch":
		roundUpdates = map[string]any{"batch_status": "canceled"}
	}

	return s.convergentSetTerminal(ctx, db, run, round, contracts.FocusCanceled, "canceled by user", roundUpdates)
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// convergentCurrentRound loads the most recent ConvergentRound for a run.
func (s *Service) convergentCurrentRound(ctx context.Context, db *gorm.DB, focusRunID uint) (contracts.ConvergentRound, error) {
	var round contracts.ConvergentRound
	if err := db.WithContext(ctx).
		Where("focus_run_id = ?", focusRunID).
		Order("round_number desc").
		First(&round).Error; err != nil {
		return round, fmt.Errorf("convergent: 查询当前 round 失败: %w", err)
	}
	return round, nil
}

// convergentParseTicketIDs parses a JSON array of uint from string.
func convergentParseTicketIDs(raw string) ([]uint, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "[]" {
		return nil, nil
	}
	var ids []uint
	if err := json.Unmarshal([]byte(raw), &ids); err != nil {
		return nil, err
	}
	return ids, nil
}

// convergentSetTerminal moves the convergent run to a terminal state.
// roundUpdates (optional) are extra fields merged into the round row update.
func (s *Service) convergentSetTerminal(ctx context.Context, db *gorm.DB, run contracts.FocusRun, round contracts.ConvergentRound, status, reason string, roundUpdates ...map[string]any) error {
	now := time.Now()
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		roundCols := map[string]any{
			"finished_at": &now,
			"updated_at":  now,
		}
		for _, extra := range roundUpdates {
			for k, v := range extra {
				roundCols[k] = v
			}
		}
		if err := tx.WithContext(ctx).Model(&contracts.ConvergentRound{}).Where("id = ?", round.ID).Updates(roundCols).Error; err != nil {
			return err
		}
		if err := tx.WithContext(ctx).Model(&contracts.FocusRun{}).Where("id = ?", run.ID).Updates(map[string]any{
			"status":      strings.TrimSpace(status),
			"updated_at":  now,
			"finished_at": &now,
		}).Error; err != nil {
			return err
		}
		return focusFinalizeRemainingItemsTx(ctx, tx, run.ID, 0, convergentItemStatusForRun(status), now)
	})
	if err != nil {
		return err
	}
	s.projectWake()
	return nil
}

// convergentSetTerminalWithEvent sets terminal state + appends an event.
// roundUpdates (optional) are extra fields merged into the round row update.
func (s *Service) convergentSetTerminalWithEvent(ctx context.Context, db *gorm.DB, run contracts.FocusRun, round contracts.ConvergentRound, status, eventKind, eventSummary string, payload map[string]any, roundUpdates ...map[string]any) error {
	now := time.Now()
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		roundCols := map[string]any{
			"finished_at": &now,
			"updated_at":  now,
		}
		for _, extra := range roundUpdates {
			for k, v := range extra {
				roundCols[k] = v
			}
		}
		if err := tx.WithContext(ctx).Model(&contracts.ConvergentRound{}).Where("id = ?", round.ID).Updates(roundCols).Error; err != nil {
			return err
		}
		if err := tx.WithContext(ctx).Model(&contracts.FocusRun{}).Where("id = ?", run.ID).Updates(map[string]any{
			"status":      strings.TrimSpace(status),
			"updated_at":  now,
			"finished_at": &now,
		}).Error; err != nil {
			return err
		}
		if err := focusFinalizeRemainingItemsTx(ctx, tx, run.ID, 0, convergentItemStatusForRun(status), now); err != nil {
			return err
		}
		if _, err := appendFocusEventTx(ctx, tx, run.ID, nil,
			strings.TrimSpace(eventKind), strings.TrimSpace(eventSummary), payload, now); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return err
	}
	s.projectWake()
	return nil
}

// convergentSetPhaseAndEvent updates the run's convergent_phase and round fields,
// plus appends an event, all in one transaction.
func (s *Service) convergentSetPhaseAndEvent(ctx context.Context, db *gorm.DB, runID uint, phase string, roundID uint, roundUpdates map[string]any, eventKind, eventSummary string, payload map[string]any) error {
	now := time.Now()
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if len(roundUpdates) > 0 {
			roundUpdates["updated_at"] = now
			if err := tx.WithContext(ctx).Model(&contracts.ConvergentRound{}).Where("id = ?", roundID).Updates(roundUpdates).Error; err != nil {
				return err
			}
		}
		if err := tx.WithContext(ctx).Model(&contracts.FocusRun{}).Where("id = ?", runID).Updates(map[string]any{
			"convergent_phase": strings.TrimSpace(phase),
			"status":           contracts.FocusRunning,
			"updated_at":       now,
		}).Error; err != nil {
			return err
		}
		if _, err := appendFocusEventTx(ctx, tx, runID, nil,
			strings.TrimSpace(eventKind), strings.TrimSpace(eventSummary), payload, now); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return err
	}
	s.projectWake()
	return nil
}

// convergentItemStatusForRun maps a run terminal status to an item terminal status.
func convergentItemStatusForRun(runStatus string) string {
	switch strings.TrimSpace(runStatus) {
	case contracts.FocusStopped:
		return contracts.FocusItemStopped
	case contracts.FocusCanceled:
		return contracts.FocusItemCanceled
	case contracts.FocusFailed:
		return contracts.FocusItemFailed
	default:
		return ""
	}
}

// getPMRunSubmitter returns the PMRunSubmitter to use for convergent mode.
func (s *Service) getPMRunSubmitter() PMRunSubmitter {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.pmSubmitter
}

// SetPMRunSubmitter injects a PMRunSubmitter (used in tests and wiring).
func (s *Service) SetPMRunSubmitter(sub PMRunSubmitter) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pmSubmitter = sub
}
