package task

import (
	"context"
	"fmt"
	"strings"
	"time"

	"dalek/internal/contracts"

	"gorm.io/gorm"
)

const (
	orphanedByCrashErrorCode = "orphaned_by_crash"
	orphanedByCrashSource    = "daemon.startup_reconcile"
)

// ReconcileOrphanedExecutionHostRuns 收口 daemon crash 后遗留的 ExecutionHost 托管任务。
// daemon 启动窗口内若内存 handle 已丢失，则所有 worker deliver_ticket / subagent subagent_run 的 pending/running 记录都视为 crash orphan。
func (s *Service) ReconcileOrphanedExecutionHostRuns(ctx context.Context, now time.Time) (int, error) {
	db, err := s.requireDB()
	if err != nil {
		return 0, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if now.IsZero() {
		now = time.Now()
	}

	rows, err := s.ListStatus(ctx, contracts.TaskListStatusOptions{
		IncludeTerminal: false,
		Limit:           5000,
	})
	if err != nil {
		return 0, err
	}

	reconciled := 0
	for _, row := range rows {
		if !orphanedExecutionHostRun(row) {
			continue
		}
		note := orphanedExecutionHostRunNote(row)
		if err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			txSvc := New(tx)
			if err := txSvc.MarkRunFailed(ctx, row.RunID, orphanedByCrashErrorCode, note, now); err != nil {
				return err
			}
			return txSvc.AppendEvent(ctx, contracts.TaskEventInput{
				TaskRunID: row.RunID,
				EventType: "task_failed",
				FromState: map[string]any{
					"orchestration_state": strings.TrimSpace(row.OrchestrationState),
				},
				ToState: map[string]any{
					"orchestration_state": contracts.TaskFailed,
					"error_code":          orphanedByCrashErrorCode,
				},
				Note: note,
				Payload: map[string]any{
					"reason":     orphanedByCrashErrorCode,
					"source":     orphanedByCrashSource,
					"owner_type": strings.TrimSpace(row.OwnerType),
					"task_type":  strings.TrimSpace(row.TaskType),
					"ticket_id":  row.TicketID,
					"worker_id":  row.WorkerID,
				},
				CreatedAt: now,
			})
		}); err != nil {
			return reconciled, err
		}
		reconciled++
	}
	return reconciled, nil
}

func orphanedExecutionHostRun(row contracts.TaskStatusView) bool {
	ownerType := strings.TrimSpace(strings.ToLower(row.OwnerType))
	taskType := strings.TrimSpace(row.TaskType)
	state := strings.TrimSpace(strings.ToLower(row.OrchestrationState))
	switch {
	case ownerType == string(contracts.TaskOwnerSubagent) && taskType == contracts.TaskTypeSubagentRun:
		return state == string(contracts.TaskPending) || state == string(contracts.TaskRunning)
	case ownerType == string(contracts.TaskOwnerWorker) && taskType == contracts.TaskTypeDeliverTicket:
		return state == string(contracts.TaskPending) || state == string(contracts.TaskRunning)
	default:
		return false
	}
}

func orphanedExecutionHostRunNote(row contracts.TaskStatusView) string {
	ownerType := strings.TrimSpace(strings.ToLower(row.OwnerType))
	taskType := strings.TrimSpace(row.TaskType)
	state := strings.TrimSpace(strings.ToLower(row.OrchestrationState))
	return fmt.Sprintf("startup recovery marked orphaned execution-host run failed: reason=%s owner=%s task_type=%s state=%s", orphanedByCrashErrorCode, ownerType, taskType, state)
}
