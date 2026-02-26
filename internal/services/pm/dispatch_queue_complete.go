package pm

import (
	"context"
	"fmt"
	"strings"
	"time"

	"dalek/internal/contracts"
	"dalek/internal/services/core"

	"gorm.io/gorm"
)

func (s *Service) completePMDispatchJobSuccess(ctx context.Context, jobID uint, runnerID string, resultJSON string) error {
	_, db, err := s.require()
	if err != nil {
		return err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if jobID == 0 {
		return fmt.Errorf("job_id 不能为空")
	}
	runnerID = strings.TrimSpace(runnerID)
	if runnerID == "" {
		return fmt.Errorf("runner_id 不能为空")
	}
	now := time.Now()
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		taskRuntime, terr := s.taskRuntimeForDB(tx)
		if terr != nil {
			return terr
		}
		res := tx.Model(&contracts.PMDispatchJob{}).
			Where("id = ? AND status = ? AND runner_id = ?", jobID, contracts.PMDispatchRunning, runnerID).
			Updates(map[string]any{
				"status":            contracts.PMDispatchSucceeded,
				"result_json":       strings.TrimSpace(resultJSON),
				"error":             "",
				"runner_id":         "",
				"lease_expires_at":  nil,
				"active_ticket_key": nil,
				"finished_at":       &now,
				"updated_at":        now,
			})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return fmt.Errorf("dispatch job 终态提交失败（runner ownership 丢失）: id=%d runner=%s", jobID, runnerID)
		}
		var job contracts.PMDispatchJob
		if err := tx.Select("task_run_id").First(&job, jobID).Error; err != nil {
			return err
		}
		if job.TaskRunID == 0 {
			return nil
		}
		if err := taskRuntime.MarkRunSucceeded(ctx, job.TaskRunID, strings.TrimSpace(resultJSON), now); err != nil {
			return err
		}
		if err := taskRuntime.AppendEvent(ctx, core.TaskRuntimeEventInput{
			TaskRunID: job.TaskRunID,
			EventType: "task_succeeded",
			FromState: map[string]any{"orchestration_state": contracts.TaskRunning},
			ToState: map[string]any{
				"orchestration_state": contracts.TaskSucceeded,
			},
			Note: "pm dispatch completed",
		}); err != nil {
			return err
		}
		return nil
	})
}

func (s *Service) completePMDispatchJobFailed(ctx context.Context, jobID uint, runnerID string, errMsg string) error {
	_, db, err := s.require()
	if err != nil {
		return err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if jobID == 0 {
		return fmt.Errorf("job_id 不能为空")
	}
	runnerID = strings.TrimSpace(runnerID)
	if runnerID == "" {
		return fmt.Errorf("runner_id 不能为空")
	}
	now := time.Now()
	var statusEvent *StatusChangeEvent
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		taskRuntime, terr := s.taskRuntimeForDB(tx)
		if terr != nil {
			return terr
		}
		res := tx.Model(&contracts.PMDispatchJob{}).
			Where("id = ? AND status = ? AND runner_id = ?", jobID, contracts.PMDispatchRunning, runnerID).
			Updates(map[string]any{
				"status":            contracts.PMDispatchFailed,
				"error":             strings.TrimSpace(errMsg),
				"runner_id":         "",
				"lease_expires_at":  nil,
				"active_ticket_key": nil,
				"finished_at":       &now,
				"updated_at":        now,
			})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return fmt.Errorf("dispatch job 失败态提交失败（runner ownership 丢失）: id=%d runner=%s", jobID, runnerID)
		}
		var job contracts.PMDispatchJob
		if err := tx.Select("task_run_id", "ticket_id", "worker_id", "request_id").First(&job, jobID).Error; err != nil {
			return err
		}
		if job.TaskRunID == 0 {
			return nil
		}
		if err := taskRuntime.MarkRunFailed(ctx, job.TaskRunID, "dispatch_failed", strings.TrimSpace(errMsg), now); err != nil {
			return err
		}
		if err := taskRuntime.AppendEvent(ctx, core.TaskRuntimeEventInput{
			TaskRunID: job.TaskRunID,
			EventType: "task_failed",
			FromState: map[string]any{"orchestration_state": contracts.TaskRunning},
			ToState: map[string]any{
				"orchestration_state": contracts.TaskFailed,
				"error_message":       strings.TrimSpace(errMsg),
			},
			Note: "pm dispatch failed",
		}); err != nil {
			return err
		}
		var demoteErr error
		statusEvent, demoteErr = s.demoteTicketBlockedOnDispatchFailedTx(ctx, tx, job, strings.TrimSpace(errMsg), now)
		if demoteErr != nil {
			return demoteErr
		}
		key := inboxKeyWorkerIncident(job.WorkerID, "dispatch_failed")
		title := fmt.Sprintf("派发失败：t%d w%d", job.TicketID, job.WorkerID)
		if job.WorkerID == 0 {
			key = inboxKeyTicketIncident(job.TicketID, "dispatch_failed")
			title = fmt.Sprintf("派发失败：t%d", job.TicketID)
		}
		_, uerr := s.upsertOpenInboxTx(ctx, tx, contracts.InboxItem{
			Key:      key,
			Status:   contracts.InboxOpen,
			Severity: contracts.InboxWarn,
			Reason:   contracts.InboxIncident,
			Title:    title,
			Body:     strings.TrimSpace(errMsg),
			TicketID: job.TicketID,
			WorkerID: job.WorkerID,
		})
		if uerr != nil {
			return uerr
		}
		return nil
	})
	if err != nil {
		return err
	}
	s.emitStatusChangeHookAsync(statusEvent)
	return nil
}

// ForceFailActiveDispatchesForTicket 用于 stop 闭环：
// 在 ticket stop 后，强制把 pending/running 的 dispatch job 终结为 failed，
// 并同步终结关联 task_run，避免 archive 被 stale dispatch 阻塞。
func (s *Service) ForceFailActiveDispatchesForTicket(ctx context.Context, ticketID uint, reason string) (int, error) {
	_, db, err := s.require()
	if err != nil {
		return 0, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if ticketID == 0 {
		return 0, fmt.Errorf("ticket_id 不能为空")
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = fmt.Sprintf("ticket stop: force fail active dispatch jobs for ticket=%d", ticketID)
	}
	now := time.Now()
	failedCount := 0
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var jobs []contracts.PMDispatchJob
		if err := tx.WithContext(ctx).
			Where("ticket_id = ? AND status IN ?", ticketID, []contracts.PMDispatchJobStatus{contracts.PMDispatchPending, contracts.PMDispatchRunning}).
			Order("id asc").
			Find(&jobs).Error; err != nil {
			return err
		}
		if len(jobs) == 0 {
			return nil
		}

		for _, job := range jobs {
			fromDispatchStatus := job.Status
			res := tx.Model(&contracts.PMDispatchJob{}).
				Where("id = ? AND status IN ?", job.ID, []contracts.PMDispatchJobStatus{contracts.PMDispatchPending, contracts.PMDispatchRunning}).
				Updates(map[string]any{
					"status":            contracts.PMDispatchFailed,
					"error":             reason,
					"runner_id":         "",
					"lease_expires_at":  nil,
					"active_ticket_key": nil,
					"finished_at":       &now,
					"updated_at":        now,
				})
			if res.Error != nil {
				return res.Error
			}
			if res.RowsAffected == 0 {
				continue
			}
			failedCount++
			if job.TaskRunID == 0 {
				continue
			}

			changed, err := markDispatchTaskRunFailedOnStopTx(tx, job.TaskRunID, reason, now)
			if err != nil {
				return err
			}
			if !changed {
				continue
			}
			fromTaskState := contracts.TaskPending
			if fromDispatchStatus == contracts.PMDispatchRunning {
				fromTaskState = contracts.TaskRunning
			}
			if err := tx.WithContext(ctx).Create(&contracts.TaskEvent{
				TaskRunID:     job.TaskRunID,
				EventType:     "dispatch_force_failed_on_stop",
				FromStateJSON: marshalJSON(map[string]any{"orchestration_state": fromTaskState}),
				ToStateJSON: marshalJSON(map[string]any{
					"orchestration_state": contracts.TaskFailed,
					"error_code":          "dispatch_force_failed_on_stop",
					"error_message":       reason,
				}),
				Note: strings.TrimSpace(reason),
				PayloadJSON: marshalJSON(map[string]any{
					"source":          "ticket_stop",
					"ticket_id":       job.TicketID,
					"worker_id":       job.WorkerID,
					"dispatch_job_id": job.ID,
					"request_id":      strings.TrimSpace(job.RequestID),
				}),
				CreatedAt: now,
			}).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return failedCount, nil
}

func markDispatchTaskRunFailedOnStopTx(tx *gorm.DB, runID uint, errMsg string, now time.Time) (bool, error) {
	if tx == nil || runID == 0 {
		return false, nil
	}
	res := tx.Model(&contracts.TaskRun{}).
		Where("id = ? AND orchestration_state IN ?", runID, []contracts.TaskOrchestrationState{contracts.TaskPending, contracts.TaskRunning}).
		Updates(map[string]any{
			"orchestration_state": contracts.TaskFailed,
			"error_code":          "dispatch_force_failed_on_stop",
			"error_message":       strings.TrimSpace(errMsg),
			"runner_id":           "",
			"lease_expires_at":    nil,
			"finished_at":         &now,
			"updated_at":          now,
		})
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected > 0, nil
}
