package pm

import (
	"context"
	"dalek/internal/contracts"
	"dalek/internal/services/ticketlifecycle"
	"fmt"
	"sort"
	"time"

	"gorm.io/gorm"
)

type RecoverySummary struct {
	ActiveRunRepairs int
}

func (s *Service) RecoverActiveTaskRuns(ctx context.Context, projectName string, now time.Time, excludedRunIDs map[uint]struct{}) (int, error) {
	_, db, err := s.require()
	if err != nil {
		return 0, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if now.IsZero() {
		now = time.Now()
	}

	taskRuntime, err := s.taskRuntimeForDB(db)
	if err != nil {
		return 0, err
	}
	activeRuns, err := taskRuntime.ListStatus(ctx, contracts.TaskListStatusOptions{
		OwnerType:       contracts.TaskOwnerWorker,
		TaskType:        "deliver_ticket",
		IncludeTerminal: false,
		Limit:           5000,
	})
	if err != nil {
		return 0, err
	}
	if len(activeRuns) == 0 {
		return 0, nil
	}

	skipRunIDs := sortedRunIDsFromSet(excludedRunIDs)
	skipSet := make(map[uint]struct{}, len(skipRunIDs))
	for _, runID := range skipRunIDs {
		skipSet[runID] = struct{}{}
	}

	repaired := 0
	seenRuns := make(map[uint]struct{}, len(activeRuns))
	for _, run := range activeRuns {
		if run.RunID == 0 || run.TicketID == 0 || run.WorkerID == 0 {
			continue
		}
		if _, skip := skipSet[run.RunID]; skip {
			continue
		}
		if _, seen := seenRuns[run.RunID]; seen {
			continue
		}
		seenRuns[run.RunID] = struct{}{}
		changed, err := s.recoverActiveWorkerRunProjection(ctx, db, run, now)
		if err != nil {
			return repaired, err
		}
		if changed {
			repaired++
		}
	}
	return repaired, nil
}

func (s *Service) ListActiveTaskRunIDs(ctx context.Context) ([]uint, error) {
	_, db, err := s.require()
	if err != nil {
		return nil, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	var runIDs []uint
	if err := db.WithContext(ctx).
		Model(&contracts.TaskRun{}).
		Where("owner_type = ? AND task_type = ? AND orchestration_state IN ?", contracts.TaskOwnerWorker, "deliver_ticket", []contracts.TaskOrchestrationState{contracts.TaskPending, contracts.TaskRunning}).
		Order("id asc").
		Pluck("id", &runIDs).Error; err != nil {
		return nil, err
	}
	return runIDs, nil
}

func (s *Service) UpdateRecoverySummary(ctx context.Context, pmStateID uint, now time.Time, plannerOps, taskRuns, notes, workers int) error {
	_, db, err := s.require()
	if err != nil {
		return err
	}
	if pmStateID == 0 {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if now.IsZero() {
		now = time.Now()
	}
	return db.WithContext(ctx).Model(&contracts.PMState{}).Where("id = ?", pmStateID).Updates(map[string]any{
		"last_recovery_at":            &now,
		"last_recovery_dispatch_jobs": plannerOps,
		"last_recovery_task_runs":     taskRuns,
		"last_recovery_notes":         notes,
		"last_recovery_workers":       workers,
		"updated_at":                  now,
	}).Error
}

func (s *Service) recoverActiveWorkerRunProjection(ctx context.Context, db *gorm.DB, run contracts.TaskStatusView, now time.Time) (bool, error) {
	if db == nil {
		return false, fmt.Errorf("db 为空")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if now.IsZero() {
		now = time.Now()
	}

	var (
		ticket contracts.Ticket
		worker contracts.Worker
	)
	if err := db.WithContext(ctx).Select("id", "workflow_status").First(&ticket, run.TicketID).Error; err != nil {
		return false, err
	}
	if err := db.WithContext(ctx).Select("id", "ticket_id", "status").First(&worker, run.WorkerID).Error; err != nil {
		return false, err
	}

	changed := false
	if worker.Status != contracts.WorkerRunning {
		if err := s.worker.MarkWorkerRunning(ctx, worker.ID, now); err != nil {
			return false, err
		}
		changed = true
	}

	switch contracts.CanonicalTicketWorkflowStatus(ticket.WorkflowStatus) {
	case contracts.TicketActive, contracts.TicketBlocked, contracts.TicketDone, contracts.TicketArchived:
		return changed, nil
	case contracts.TicketBacklog, contracts.TicketQueued:
		repaired, err := s.repairTicketActiveProjectionFromRun(ctx, ticket.ID, worker.ID, run.RunID, now)
		if err != nil {
			return changed, err
		}
		return changed || repaired, nil
	default:
		return changed, nil
	}
}

func (s *Service) repairTicketActiveProjectionFromRun(ctx context.Context, ticketID, workerID, taskRunID uint, now time.Time) (bool, error) {
	_, db, err := s.require()
	if err != nil {
		return false, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if now.IsZero() {
		now = time.Now()
	}

	var statusEvent *StatusChangeEvent
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var ticket contracts.Ticket
		if err := tx.WithContext(ctx).Select("id", "workflow_status").First(&ticket, ticketID).Error; err != nil {
			return err
		}
		from := contracts.CanonicalTicketWorkflowStatus(ticket.WorkflowStatus)
		if from != contracts.TicketBacklog && from != contracts.TicketQueued {
			return nil
		}
		source := "pm.recovery.active_run"
		lifecycleResult, err := s.appendTicketLifecycleEventAndProjectSnapshotTx(ctx, tx, ticketlifecycle.AppendInput{
			TicketID:       ticketID,
			EventType:      contracts.TicketLifecycleRepaired,
			Source:         source,
			ActorType:      contracts.TicketLifecycleActorSystem,
			WorkerID:       workerID,
			TaskRunID:      taskRunID,
			IdempotencyKey: fmt.Sprintf("ticket:%d:recovery:active_run:%d", ticketID, taskRunID),
			Payload: lifecycleRepairPayload(contracts.TicketActive, contracts.IntegrationNone, map[string]any{
				"ticket_id":   ticketID,
				"worker_id":   workerID,
				"task_run_id": taskRunID,
				"reason":      "daemon recovery repaired active projection from deliver_ticket run",
			}),
			CreatedAt: now,
		})
		if err != nil {
			return err
		}
		if !lifecycleResult.WorkflowChanged() {
			return nil
		}
		if err := s.appendTicketWorkflowEventTx(ctx, tx, ticketID, lifecycleResult.Before.WorkflowStatus, lifecycleResult.After.WorkflowStatus, source, "daemon recovery repaired active projection from deliver_ticket run", map[string]any{
			"ticket_id":   ticketID,
			"worker_id":   workerID,
			"task_run_id": taskRunID,
		}, now); err != nil {
			return err
		}
		statusEvent = s.buildStatusChangeEvent(ticketID, lifecycleResult.Before.WorkflowStatus, lifecycleResult.After.WorkflowStatus, source, now)
		if statusEvent != nil {
			statusEvent.WorkerID = workerID
			statusEvent.Detail = fmt.Sprintf("active deliver_ticket run=%d repaired queued/backlog projection", taskRunID)
		}
		return nil
	})
	if err != nil {
		return false, err
	}
	s.emitStatusChangeHookAsync(statusEvent)
	return statusEvent != nil, nil
}

func sortedRunIDsFromSet(runIDs map[uint]struct{}) []uint {
	if len(runIDs) == 0 {
		return nil
	}
	ids := make([]uint, 0, len(runIDs))
	for id := range runIDs {
		if id == 0 {
			continue
		}
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return nil
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}
