package pm

import (
	"context"
	"fmt"
	"strings"
	"time"

	"dalek/internal/contracts"
	"dalek/internal/fsm"
	"dalek/internal/services/ticketlifecycle"

	"gorm.io/gorm"
)

func validTicketWorkflowStatus(st contracts.TicketWorkflowStatus) bool {
	return fsm.TicketWorkflowTable.IsKnownState(st)
}

func archiveTicketGuardError(ticketID uint, workflow contracts.TicketWorkflowStatus, integration contracts.IntegrationStatus) error {
	return fmt.Errorf(
		"ticket t%d 当前状态不允许归档（workflow=%s, integration=%s）",
		ticketID,
		contracts.CanonicalTicketWorkflowStatus(workflow),
		contracts.CanonicalIntegrationStatus(integration),
	)
}

// SetTicketWorkflowStatus 是 repair-only 入口；正常生命周期推进必须走 lifecycle event。
func (s *Service) SetTicketWorkflowStatus(ctx context.Context, ticketID uint, status contracts.TicketWorkflowStatus) error {
	_, db, err := s.require()
	if err != nil {
		return err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if ticketID == 0 {
		return fmt.Errorf("ticket_id 不能为空")
	}
	status = contracts.TicketWorkflowStatus(strings.TrimSpace(strings.ToLower(string(status))))
	if status == "" {
		return fmt.Errorf("workflow_status 不能为空")
	}
	if !validTicketWorkflowStatus(status) {
		return fmt.Errorf("非法 workflow_status: %s", status)
	}
	var statusEvent *StatusChangeEvent
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var t contracts.Ticket
		if err := tx.WithContext(ctx).First(&t, ticketID).Error; err != nil {
			return err
		}
		from := contracts.CanonicalTicketWorkflowStatus(t.WorkflowStatus)
		if !fsm.CanManualSetWorkflowStatus(from) {
			return fmt.Errorf("ticket 已归档，不能修改 workflow_status: t%d", ticketID)
		}
		if from == status {
			return nil
		}
		now := time.Now()
		lifecycleResult, err := s.appendTicketLifecycleEventAndProjectSnapshotTx(ctx, tx, ticketlifecycle.AppendInput{
			TicketID:       ticketID,
			EventType:      contracts.TicketLifecycleRepaired,
			Source:         "pm.set_workflow",
			ActorType:      contracts.TicketLifecycleActorUser,
			IdempotencyKey: ticketlifecycle.RepairedIdempotencyKey(ticketID, "pm.set_workflow", now),
			Payload: lifecycleRepairPayload(status, contracts.IntegrationNone, map[string]any{
				"ticket_id": ticketID,
			}),
			CreatedAt: now,
		})
		if err != nil {
			return err
		}
		if !lifecycleResult.WorkflowChanged() {
			return nil
		}
		if err := s.appendTicketWorkflowEventTx(ctx, tx, ticketID, lifecycleResult.Before.WorkflowStatus, lifecycleResult.After.WorkflowStatus, "pm.set_workflow", "手动设置 workflow_status", map[string]any{
			"ticket_id": ticketID,
		}, now); err != nil {
			return err
		}
		statusEvent = s.buildStatusChangeEvent(ticketID, lifecycleResult.Before.WorkflowStatus, lifecycleResult.After.WorkflowStatus, "pm.set_workflow", now)
		return nil
	})
	if err != nil {
		return err
	}
	s.emitStatusChangeHookAsync(statusEvent)
	return nil
}

// ArchiveTicket 归档 ticket（终态）：把 workflow_status 设置为 archived。
func (s *Service) ArchiveTicket(ctx context.Context, ticketID uint) error {
	_, db, err := s.require()
	if err != nil {
		return err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if ticketID == 0 {
		return fmt.Errorf("ticket_id 不能为空")
	}

	var t contracts.Ticket
	if err := db.WithContext(ctx).First(&t, ticketID).Error; err != nil {
		return err
	}
	if !fsm.CanArchiveTicket(t.WorkflowStatus, t.IntegrationStatus) {
		return archiveTicketGuardError(ticketID, t.WorkflowStatus, t.IntegrationStatus)
	}

	var cnt int64
	if err := db.WithContext(ctx).
		Model(&contracts.Worker{}).
		Where("ticket_id = ? AND status = ?", ticketID, contracts.WorkerRunning).
		Count(&cnt).Error; err != nil {
		return err
	}
	if cnt > 0 {
		return fmt.Errorf("该 ticket 仍有运行中的 worker，请先停止再归档")
	}
	now := time.Now()
	var statusEvent *StatusChangeEvent
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var cur contracts.Ticket
		if err := tx.WithContext(ctx).First(&cur, ticketID).Error; err != nil {
			return err
		}
		from := contracts.CanonicalTicketWorkflowStatus(cur.WorkflowStatus)
		if !fsm.CanArchiveTicket(from, cur.IntegrationStatus) {
			return archiveTicketGuardError(ticketID, from, cur.IntegrationStatus)
		}
		cleanupRequested := false
		cleanupWorkerID := uint(0)
		cleanupWorktree := ""
		var w contracts.Worker
		werr := tx.WithContext(ctx).Where("ticket_id = ?", ticketID).Order("id DESC").First(&w).Error
		if werr != nil && werr != gorm.ErrRecordNotFound {
			return werr
		}
		if werr == nil {
			if err := tx.WithContext(ctx).Model(&contracts.Worker{}).Where("id = ?", w.ID).Updates(map[string]any{
				"worktree_gc_requested_at": &now,
				"worktree_gc_cleaned_at":   nil,
				"worktree_cleanup_error":   "",
				"updated_at":               now,
			}).Error; err != nil {
				return err
			}
			cleanupRequested = true
			cleanupWorkerID = w.ID
			cleanupWorktree = strings.TrimSpace(w.WorktreePath)
		}
		lifecycleResult, err := s.appendTicketLifecycleEventAndProjectSnapshotTx(ctx, tx, ticketlifecycle.AppendInput{
			TicketID:       ticketID,
			EventType:      contracts.TicketLifecycleArchived,
			Source:         "pm.archive",
			ActorType:      contracts.TicketLifecycleActorUser,
			WorkerID:       cleanupWorkerID,
			IdempotencyKey: ticketlifecycle.ArchivedIdempotencyKey(ticketID),
			Payload: map[string]any{
				"ticket_id":            ticketID,
				"cleanup_requested":    cleanupRequested,
				"cleanup_worker_id":    cleanupWorkerID,
				"cleanup_worktree":     cleanupWorktree,
				"cleanup_requested_at": now,
			},
			CreatedAt: now,
		})
		if err != nil {
			return err
		}
		if !lifecycleResult.WorkflowChanged() {
			return nil
		}
		if err := s.appendTicketWorkflowEventTx(ctx, tx, ticketID, lifecycleResult.Before.WorkflowStatus, lifecycleResult.After.WorkflowStatus, "pm.archive", "手动归档 ticket", map[string]any{
			"ticket_id":            ticketID,
			"cleanup_requested":    cleanupRequested,
			"cleanup_worker_id":    cleanupWorkerID,
			"cleanup_worktree":     cleanupWorktree,
			"cleanup_requested_at": now,
		}, now); err != nil {
			return err
		}
		statusEvent = s.buildStatusChangeEvent(ticketID, lifecycleResult.Before.WorkflowStatus, lifecycleResult.After.WorkflowStatus, "pm.archive", now)
		return nil
	})
	if err != nil {
		return err
	}
	s.emitStatusChangeHookAsync(statusEvent)
	return nil
}

// ApplyWorkerReport 摄入 worker report，并由 PM reducer 推进 workflow（唯一写者）。
func (s *Service) ApplyWorkerReport(ctx context.Context, r contracts.WorkerReport, source string) error {
	p, _, err := s.require()
	if err != nil {
		return err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	r.Normalize()
	if err := r.Validate(); err != nil {
		return err
	}
	source = strings.TrimSpace(source)
	if source == "" {
		source = "report"
	}
	if strings.TrimSpace(r.ProjectKey) == "" {
		r.ProjectKey = strings.TrimSpace(p.Key)
	}

	// 1) 运行观测与事件（append-only）
	if err := s.worker.ApplyWorkerReport(ctx, r, source); err != nil {
		return err
	}

	ticketID := r.TicketID
	if ticketID == 0 {
		if w, werr := s.worker.WorkerByID(ctx, r.WorkerID); werr == nil && w != nil {
			ticketID = w.TicketID
		}
	}
	if ticketID == 0 {
		// report 仍然被接收并写入运行观测，但无法推进 workflow。
		return nil
	}

	// 2) workflow reducer（唯一写者）+ 因果 side effects（同事务）
	next := strings.TrimSpace(strings.ToLower(r.NextAction))
	var promoteTo contracts.TicketWorkflowStatus
	switch next {
	case string(contracts.NextDone):
		promoteTo = contracts.TicketDone
	case string(contracts.NextWaitUser):
		promoteTo = contracts.TicketBlocked
	case string(contracts.NextContinue):
		promoteTo = contracts.TicketActive
	default:
		// unknown 不强推 workflow。
		return nil
	}

	_, db, err := s.require()
	if err != nil {
		return err
	}
	now := time.Now()
	var statusEvent *StatusChangeEvent
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var t contracts.Ticket
		if err := tx.WithContext(ctx).First(&t, ticketID).Error; err != nil {
			return err
		}
		if !fsm.ShouldApplyWorkerReport(t.WorkflowStatus) {
			return nil
		}
		// 不允许自动把 done 回滚为 active（例如 report continue 误发）。
		if !fsm.CanReportPromoteTo(t.WorkflowStatus, promoteTo) {
			return nil
		}
		from := contracts.CanonicalTicketWorkflowStatus(t.WorkflowStatus)
		taskRunID, err := latestWorkerTaskRunIDTx(ctx, tx, r.WorkerID)
		if err != nil {
			return err
		}
		lifecycleSource := fmt.Sprintf("pm.apply_worker_report(%s)", source)

		switch next {
		case string(contracts.NextWaitUser):
			lifecycleResult, err := s.appendTicketLifecycleEventAndProjectSnapshotTx(ctx, tx, ticketlifecycle.AppendInput{
				TicketID:       t.ID,
				EventType:      contracts.TicketLifecycleWaitUserReported,
				Source:         lifecycleSource,
				ActorType:      contracts.TicketLifecycleActorWorker,
				WorkerID:       r.WorkerID,
				TaskRunID:      taskRunID,
				IdempotencyKey: ticketlifecycle.WaitUserReportedIdempotencyKey(t.ID, taskRunID, r.WorkerID),
				Payload: map[string]any{
					"ticket_id":   t.ID,
					"worker_id":   r.WorkerID,
					"task_run_id": taskRunID,
					"next_action": next,
					"source":      source,
					"summary":     strings.TrimSpace(r.Summary),
					"blockers":    r.Blockers,
				},
				CreatedAt: now,
			})
			if err != nil {
				return err
			}
			if !lifecycleResult.Inserted {
				return nil
			}
			if lifecycleResult.WorkflowChanged() {
				if err := s.appendTicketWorkflowEventTx(ctx, tx, t.ID, lifecycleResult.Before.WorkflowStatus, lifecycleResult.After.WorkflowStatus, "pm.apply_worker_report", "worker report 推进 workflow", map[string]any{
					"worker_id":   r.WorkerID,
					"ticket_id":   t.ID,
					"next_action": next,
					"source":      source,
				}, now); err != nil {
					return err
				}
				statusEvent = s.buildStatusChangeEvent(t.ID, lifecycleResult.Before.WorkflowStatus, lifecycleResult.After.WorkflowStatus, lifecycleSource, now)
				if statusEvent != nil {
					statusEvent.WorkerID = r.WorkerID
					statusEvent.Detail = buildNeedsUserInboxBodyFromReport(r)
				}
			}
			_, err = s.upsertOpenInboxTx(ctx, tx, contracts.InboxItem{
				Key:      inboxKeyNeedsUser(r.WorkerID),
				Status:   contracts.InboxOpen,
				Severity: contracts.InboxBlocker,
				Reason:   contracts.InboxNeedsUser,
				Title:    fmt.Sprintf("需要你输入：t%d w%d", t.ID, r.WorkerID),
				Body:     buildNeedsUserInboxBodyFromReport(r),
				TicketID: t.ID,
				WorkerID: r.WorkerID,
			})
			return err

		case string(contracts.NextDone):
			freeze, err := s.resolveDoneIntegrationFreezeTx(ctx, tx, t.ID, r.WorkerID)
			if err != nil {
				return err
			}
			lifecycleResult, err := s.appendTicketLifecycleEventAndProjectSnapshotTx(ctx, tx, ticketlifecycle.AppendInput{
				TicketID:       t.ID,
				EventType:      contracts.TicketLifecycleDoneReported,
				Source:         lifecycleSource,
				ActorType:      contracts.TicketLifecycleActorWorker,
				WorkerID:       r.WorkerID,
				TaskRunID:      taskRunID,
				IdempotencyKey: ticketlifecycle.DoneReportedIdempotencyKey(t.ID, taskRunID, r.WorkerID),
				Payload: map[string]any{
					"ticket_id":          t.ID,
					"worker_id":          r.WorkerID,
					"task_run_id":        taskRunID,
					"next_action":        next,
					"source":             source,
					"summary":            strings.TrimSpace(r.Summary),
					"head_sha":           freeze.AnchorSHA,
					"anchor_sha":         freeze.AnchorSHA,
					"target_ref":         freeze.TargetRef,
					"integration_status": string(contracts.IntegrationNeedsMerge),
					"workflow_status":    string(contracts.TicketDone),
				},
				CreatedAt: now,
			})
			if err != nil {
				return err
			}
			if !lifecycleResult.Inserted {
				return nil
			}
			if err := s.applyDoneIntegrationFreezeTx(ctx, tx, t.ID, freeze, now); err != nil {
				return err
			}
			if !lifecycleResult.WorkflowChanged() {
				return nil
			}
			if err := s.appendTicketWorkflowEventTx(ctx, tx, t.ID, lifecycleResult.Before.WorkflowStatus, lifecycleResult.After.WorkflowStatus, "pm.apply_worker_report", "worker report 推进 workflow", map[string]any{
				"worker_id":   r.WorkerID,
				"ticket_id":   t.ID,
				"next_action": next,
				"source":      source,
			}, now); err != nil {
				return err
			}
			statusEvent = s.buildStatusChangeEvent(t.ID, lifecycleResult.Before.WorkflowStatus, lifecycleResult.After.WorkflowStatus, lifecycleSource, now)
			if statusEvent != nil {
				statusEvent.WorkerID = r.WorkerID
				summary := strings.TrimSpace(r.Summary)
				if summary != "" && summary != "-" {
					statusEvent.Detail = summary
				}
			}
			return nil

		case string(contracts.NextContinue):
			if from == contracts.TicketActive {
				return nil
			}
			idempotencyKey := ticketlifecycle.ActivatedRunIdempotencyKey(t.ID, taskRunID)
			if taskRunID == 0 {
				idempotencyKey = fmt.Sprintf("ticket:%d:activated:worker_report:%d", t.ID, r.WorkerID)
			}
			lifecycleResult, err := s.appendTicketLifecycleEventAndProjectSnapshotTx(ctx, tx, ticketlifecycle.AppendInput{
				TicketID:       t.ID,
				EventType:      contracts.TicketLifecycleActivated,
				Source:         lifecycleSource,
				ActorType:      contracts.TicketLifecycleActorWorker,
				WorkerID:       r.WorkerID,
				TaskRunID:      taskRunID,
				IdempotencyKey: idempotencyKey,
				Payload: map[string]any{
					"ticket_id":   t.ID,
					"worker_id":   r.WorkerID,
					"task_run_id": taskRunID,
					"next_action": next,
					"source":      source,
				},
				CreatedAt: now,
			})
			if err != nil {
				return err
			}
			if !lifecycleResult.WorkflowChanged() {
				return nil
			}
			if err := s.appendTicketWorkflowEventTx(ctx, tx, t.ID, lifecycleResult.Before.WorkflowStatus, lifecycleResult.After.WorkflowStatus, "pm.apply_worker_report", "worker report 补齐 active 投影", map[string]any{
				"worker_id":   r.WorkerID,
				"ticket_id":   t.ID,
				"next_action": next,
				"source":      source,
			}, now); err != nil {
				return err
			}
			statusEvent = s.buildStatusChangeEvent(t.ID, lifecycleResult.Before.WorkflowStatus, lifecycleResult.After.WorkflowStatus, lifecycleSource, now)
			if statusEvent != nil {
				statusEvent.WorkerID = r.WorkerID
			}
			return nil
		}
		return nil
	})
	if err != nil {
		return err
	}
	s.emitStatusChangeHookAsync(statusEvent)
	return nil
}

func buildNeedsUserInboxBodyFromReport(r contracts.WorkerReport) string {
	summary := strings.TrimSpace(r.Summary)
	lines := make([]string, 0, len(r.Blockers)+2)
	if summary != "" && summary != "-" {
		lines = append(lines, summary)
	}
	for _, b := range r.Blockers {
		b = strings.TrimSpace(b)
		if b == "" {
			continue
		}
		lines = append(lines, "- "+b)
	}
	if len(lines) == 0 {
		return "worker 请求人工介入"
	}
	return strings.Join(lines, "\n")
}
