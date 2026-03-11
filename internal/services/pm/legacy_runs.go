package pm

import (
	"context"
	"fmt"
	"strings"
	"time"

	"dalek/internal/contracts"

	"gorm.io/gorm"
)

var legacyDispatchTaskTypes = []string{
	"dispatch_ticket",
	"pm_dispatch_agent",
}

func (s *Service) cancelLegacyDispatchRuns(ctx context.Context, db *gorm.DB, ticketID, workerID uint, reason string, now time.Time) (int, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if now.IsZero() {
		now = time.Now()
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "legacy dispatch run canceled after dispatch removal"
	}

	if db == nil {
		_, projectDB, err := s.require()
		if err != nil {
			return 0, err
		}
		db = projectDB
	}
	rt, err := s.taskRuntimeForDB(db)
	if err != nil {
		return 0, err
	}

	query := db.WithContext(ctx).
		Model(&contracts.TaskRun{}).
		Where("owner_type = ? AND task_type IN ? AND orchestration_state IN ?", contracts.TaskOwnerPM, legacyDispatchTaskTypes, []contracts.TaskOrchestrationState{contracts.TaskPending, contracts.TaskRunning})
	if ticketID != 0 {
		query = query.Where("ticket_id = ?", ticketID)
	}
	if workerID != 0 {
		query = query.Where("worker_id = ?", workerID)
	}

	var runs []contracts.TaskRun
	if err := query.Order("id asc").Find(&runs).Error; err != nil {
		return 0, err
	}
	canceled := 0
	for _, run := range runs {
		if err := rt.MarkRunCanceled(ctx, run.ID, "legacy_dispatch_removed", reason, now); err != nil {
			return canceled, err
		}
		if err := rt.AppendEvent(ctx, contracts.TaskEventInput{
			TaskRunID: run.ID,
			EventType: "task_canceled",
			FromState: map[string]any{
				"orchestration_state": run.OrchestrationState,
			},
			ToState: map[string]any{
				"orchestration_state": contracts.TaskCanceled,
			},
			Note: reason,
			Payload: map[string]any{
				"source":    "pm.legacy_dispatch_cleanup",
				"ticket_id": run.TicketID,
				"worker_id": run.WorkerID,
				"task_type": strings.TrimSpace(run.TaskType),
			},
			CreatedAt: now,
		}); err != nil {
			return canceled, err
		}
		canceled++
	}
	return canceled, nil
}

func legacyDispatchCleanupReason(ticketID uint) string {
	if ticketID == 0 {
		return "legacy dispatch runs canceled during recovery after dispatch removal"
	}
	return fmt.Sprintf("legacy dispatch runs canceled for ticket t%d after dispatch removal", ticketID)
}
