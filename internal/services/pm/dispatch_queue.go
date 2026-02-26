package pm

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"dalek/internal/contracts"
	"dalek/internal/services/core"

	"gorm.io/gorm"
)

func (s *Service) enqueuePMDispatchJob(ctx context.Context, ticketID, workerID uint, requestID string) (contracts.PMDispatchJob, error) {
	_, db, err := s.require()
	if err != nil {
		return contracts.PMDispatchJob{}, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if ticketID == 0 {
		return contracts.PMDispatchJob{}, fmt.Errorf("ticket_id 不能为空")
	}
	if workerID == 0 {
		return contracts.PMDispatchJob{}, fmt.Errorf("worker_id 不能为空")
	}
	requestID = strings.TrimSpace(requestID)
	var out contracts.PMDispatchJob
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		taskRuntime, terr := s.taskRuntimeForDB(tx)
		if terr != nil {
			return terr
		}
		if requestID != "" {
			var sameRequest contracts.PMDispatchJob
			err := tx.Where("request_id = ?", requestID).Order("id desc").First(&sameRequest).Error
			if err == nil {
				if sameRequest.TaskRunID == 0 {
					if run, ferr := taskRuntime.FindRunByRequestID(ctx, requestID); ferr == nil && run != nil {
						_ = tx.Model(&contracts.PMDispatchJob{}).Where("id = ?", sameRequest.ID).Update("task_run_id", run.ID).Error
						sameRequest.TaskRunID = run.ID
					}
				}
				out = sameRequest
				return nil
			}
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			run, ferr := taskRuntime.FindRunByRequestID(ctx, requestID)
			if ferr != nil {
				return ferr
			}
			if run != nil && (run.OwnerType != contracts.TaskOwnerPM || strings.TrimSpace(run.TaskType) != "dispatch_ticket") {
				return fmt.Errorf("request_id 已被其他任务占用: %s", requestID)
			}
		}

		var active contracts.PMDispatchJob
		err := tx.
			Where("ticket_id = ? AND status IN ?", ticketID, []contracts.PMDispatchJobStatus{contracts.PMDispatchPending, contracts.PMDispatchRunning}).
			Order("id desc").
			First(&active).Error
		if err == nil {
			if active.WorkerID != workerID {
				return fmt.Errorf("存在进行中的 dispatch job 绑定其他 worker: job=%d worker=%d current_worker=%d", active.ID, active.WorkerID, workerID)
			}
			if active.TaskRunID == 0 {
				if run, ferr := taskRuntime.FindRunByRequestID(ctx, strings.TrimSpace(active.RequestID)); ferr == nil && run != nil {
					_ = tx.Model(&contracts.PMDispatchJob{}).Where("id = ?", active.ID).Update("task_run_id", run.ID).Error
					active.TaskRunID = run.ID
				}
			}
			out = active
			return nil
		}
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}

		jobRequestID := requestID
		if jobRequestID == "" {
			jobRequestID = newPMDispatchRequestID()
		}
		requestPayload := marshalJSON(map[string]any{
			"ticket_id":       ticketID,
			"worker_id":       workerID,
			"orchestrator":    "pm_dispatch",
			"orchestration_v": "v1",
		})
		taskRun, err := taskRuntime.CreateRun(ctx, core.TaskRuntimeCreateRunInput{
			OwnerType:          contracts.TaskOwnerPM,
			TaskType:           "dispatch_ticket",
			ProjectKey:         strings.TrimSpace(s.p.Key),
			TicketID:           ticketID,
			WorkerID:           workerID,
			SubjectType:        "ticket",
			SubjectID:          fmt.Sprintf("%d", ticketID),
			RequestID:          jobRequestID,
			OrchestrationState: contracts.TaskPending,
			RequestPayloadJSON: requestPayload,
		})
		if err != nil {
			return err
		}

		job := contracts.PMDispatchJob{
			RequestID:       jobRequestID,
			TicketID:        ticketID,
			WorkerID:        workerID,
			TaskRunID:       taskRun.ID,
			ActiveTicketKey: func(v uint) *uint { return &v }(ticketID),
			Status:          contracts.PMDispatchPending,
			RunnerID:        "",
			LeaseExpiresAt:  nil,
			Attempt:         0,
			ResultJSON:      "",
			Error:           "",
			StartedAt:       nil,
			FinishedAt:      nil,
		}
		if err := tx.Create(&job).Error; err != nil {
			if isPMDispatchRequestIDUniqueConflict(err) {
				var sameRequest contracts.PMDispatchJob
				ferr := tx.Where("request_id = ?", strings.TrimSpace(jobRequestID)).Order("id desc").First(&sameRequest).Error
				if ferr == nil {
					out = sameRequest
					return nil
				}
				if !errors.Is(ferr, gorm.ErrRecordNotFound) {
					return ferr
				}
			}
			return err
		}
		if err := taskRuntime.AppendEvent(ctx, core.TaskRuntimeEventInput{
			TaskRunID: taskRun.ID,
			EventType: "task_enqueued",
			ToState: map[string]any{
				"orchestration_state": contracts.TaskPending,
			},
			Note: "pm dispatch job enqueued",
		}); err != nil {
			return err
		}
		out = job
		return nil
	})
	if err != nil {
		return contracts.PMDispatchJob{}, err
	}
	return out, nil
}

func (s *Service) claimPMDispatchJob(ctx context.Context, jobID uint, runnerID string, leaseTTL time.Duration) (contracts.PMDispatchJob, bool, error) {
	_, db, err := s.require()
	if err != nil {
		return contracts.PMDispatchJob{}, false, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if jobID == 0 {
		return contracts.PMDispatchJob{}, false, fmt.Errorf("job_id 不能为空")
	}
	runnerID = strings.TrimSpace(runnerID)
	if runnerID == "" {
		return contracts.PMDispatchJob{}, false, fmt.Errorf("runner_id 不能为空")
	}
	if leaseTTL <= 0 {
		leaseTTL = dispatchLeaseTTLMin
	}

	now := time.Now()
	lease := now.Add(leaseTTL)
	claimed := false
	var out contracts.PMDispatchJob
	var statusEvent *StatusChangeEvent
	if err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		taskRuntime, terr := s.taskRuntimeForDB(tx)
		if terr != nil {
			return terr
		}
		res := tx.Model(&contracts.PMDispatchJob{}).
			Where("id = ? AND (status = ? OR (status = ? AND lease_expires_at IS NOT NULL AND lease_expires_at <= ?))", jobID, contracts.PMDispatchPending, contracts.PMDispatchRunning, now).
			Updates(map[string]any{
				"status":           contracts.PMDispatchRunning,
				"runner_id":        runnerID,
				"lease_expires_at": &lease,
				"attempt":          gorm.Expr("attempt + 1"),
				"started_at":       &now,
				"finished_at":      nil,
				"error":            "",
			})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected > 0 {
			claimed = true
		}
		if err := tx.First(&out, jobID).Error; err != nil {
			return err
		}
		if claimed && out.TaskRunID != 0 {
			prev, _ := taskRuntime.FindRunByID(ctx, out.TaskRunID)
			if err := taskRuntime.MarkRunRunning(ctx, out.TaskRunID, runnerID, &lease, now, true); err != nil {
				return err
			}
			fromState := map[string]any{"orchestration_state": contracts.TaskPending}
			if prev != nil {
				fromState = map[string]any{"orchestration_state": prev.OrchestrationState}
			}
			if err := taskRuntime.AppendEvent(ctx, core.TaskRuntimeEventInput{
				TaskRunID: out.TaskRunID,
				EventType: "task_claimed",
				FromState: fromState,
				ToState: map[string]any{
					"orchestration_state": contracts.TaskRunning,
					"runner_id":           runnerID,
					"lease_expires_at":    lease.Local().Format(time.RFC3339),
				},
				Note: "pm dispatch claimed",
			}); err != nil {
				return err
			}
		}
		if claimed {
			var promoteErr error
			statusEvent, promoteErr = s.promoteTicketActiveOnDispatchClaimTx(ctx, tx, out, now)
			if promoteErr != nil {
				return promoteErr
			}
		}
		return nil
	}); err != nil {
		return contracts.PMDispatchJob{}, false, err
	}
	s.emitStatusChangeHookAsync(statusEvent)
	return out, claimed, nil
}

func (s *Service) renewPMDispatchJobLease(ctx context.Context, jobID uint, runnerID string, leaseTTL time.Duration) error {
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
	if leaseTTL <= 0 {
		leaseTTL = dispatchLeaseTTLMin
	}
	now := time.Now()
	lease := now.Add(leaseTTL)
	res := db.WithContext(ctx).
		Model(&contracts.PMDispatchJob{}).
		Where("id = ? AND status = ? AND runner_id = ?", jobID, contracts.PMDispatchRunning, runnerID).
		Updates(map[string]any{
			"lease_expires_at": &lease,
			"updated_at":       now,
		})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("dispatch job 不可续租: id=%d runner=%s", jobID, runnerID)
	}
	var job contracts.PMDispatchJob
	if err := db.WithContext(ctx).Select("task_run_id").First(&job, jobID).Error; err == nil && job.TaskRunID != 0 {
		taskRuntime, terr := s.taskRuntimeForDB(db)
		if terr != nil {
			s.slog().Warn("pm dispatch renew lease sync task run failed",
				"job_id", jobID,
				"task_run_id", job.TaskRunID,
				"runner_id", runnerID,
				"error", terr,
			)
		} else if err := taskRuntime.RenewLease(ctx, job.TaskRunID, runnerID, &lease); err != nil {
			s.slog().Warn("pm dispatch renew lease sync task run failed",
				"job_id", jobID,
				"task_run_id", job.TaskRunID,
				"runner_id", runnerID,
				"error", err,
			)
		}
	}
	return nil
}
