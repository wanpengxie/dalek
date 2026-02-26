package pm

import (
	"context"
	"crypto/rand"
	"dalek/internal/contracts"
	"dalek/internal/fsm"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"gorm.io/gorm"
)

func isPMDispatchTerminalStatus(st contracts.PMDispatchJobStatus) bool {
	switch st {
	case contracts.PMDispatchSucceeded, contracts.PMDispatchFailed:
		return true
	default:
		return false
	}
}

func newPMDispatchRequestID() string {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("dsp_%d", time.Now().UnixNano())
	}
	return "dsp_" + hex.EncodeToString(buf)
}

func newPMDispatchRunnerID() string {
	return fmt.Sprintf("runner-%d-%s", os.Getpid(), strings.TrimPrefix(newPMDispatchRequestID(), "dsp_"))
}

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
		taskRun, err := taskRuntime.CreateRun(ctx, contracts.TaskRunCreateInput{
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
		if err := taskRuntime.AppendEvent(ctx, contracts.TaskEventInput{
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

func isPMDispatchRequestIDUniqueConflict(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return true
	}
	msg := strings.ToLower(strings.TrimSpace(err.Error()))
	return strings.Contains(msg, "unique constraint failed") && strings.Contains(msg, "pm_dispatch_jobs.request_id")
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
			if err := taskRuntime.AppendEvent(ctx, contracts.TaskEventInput{
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
		if err := taskRuntime.AppendEvent(ctx, contracts.TaskEventInput{
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
		if err := taskRuntime.AppendEvent(ctx, contracts.TaskEventInput{
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

func (s *Service) promoteTicketActiveOnDispatchClaimTx(ctx context.Context, tx *gorm.DB, job contracts.PMDispatchJob, now time.Time) (*StatusChangeEvent, error) {
	if tx == nil || job.TicketID == 0 {
		return nil, nil
	}
	var ticket contracts.Ticket
	if err := tx.WithContext(ctx).Select("id", "workflow_status").First(&ticket, job.TicketID).Error; err != nil {
		return nil, err
	}
	from := normalizeTicketWorkflowStatus(ticket.WorkflowStatus)
	if !fsm.ShouldPromoteOnDispatchClaim(from) {
		return nil, nil
	}
	if err := tx.WithContext(ctx).Model(&contracts.Ticket{}).
		Where("id = ?", ticket.ID).
		Updates(map[string]any{
			"workflow_status": contracts.TicketActive,
			"updated_at":      now,
		}).Error; err != nil {
		return nil, err
	}
	if err := s.appendTicketWorkflowEventTx(ctx, tx, ticket.ID, from, contracts.TicketActive, "pm.dispatch", "dispatch 开始推进到 active", map[string]any{
		"ticket_id":   ticket.ID,
		"worker_id":   job.WorkerID,
		"dispatch_id": job.ID,
		"request_id":  strings.TrimSpace(job.RequestID),
	}, now); err != nil {
		return nil, err
	}
	return s.buildStatusChangeEvent(ticket.ID, from, contracts.TicketActive, "pm.dispatch.claim", now), nil
}

func (s *Service) demoteTicketBlockedOnDispatchFailedTx(ctx context.Context, tx *gorm.DB, job contracts.PMDispatchJob, errMsg string, now time.Time) (*StatusChangeEvent, error) {
	if tx == nil || job.TicketID == 0 {
		return nil, nil
	}
	var ticket contracts.Ticket
	if err := tx.WithContext(ctx).Select("id", "workflow_status").First(&ticket, job.TicketID).Error; err != nil {
		return nil, err
	}
	from := normalizeTicketWorkflowStatus(ticket.WorkflowStatus)
	if !fsm.ShouldDemoteOnDispatchFailed(from) {
		return nil, nil
	}
	if err := tx.WithContext(ctx).Model(&contracts.Ticket{}).
		Where("id = ?", ticket.ID).
		Updates(map[string]any{
			"workflow_status": contracts.TicketBlocked,
			"updated_at":      now,
		}).Error; err != nil {
		return nil, err
	}
	if err := s.appendTicketWorkflowEventTx(ctx, tx, ticket.ID, from, contracts.TicketBlocked, "pm.dispatch", "dispatch 失败推进到 blocked", map[string]any{
		"ticket_id":   ticket.ID,
		"worker_id":   job.WorkerID,
		"dispatch_id": job.ID,
		"request_id":  strings.TrimSpace(job.RequestID),
		"error":       strings.TrimSpace(errMsg),
	}, now); err != nil {
		return nil, err
	}
	ev := s.buildStatusChangeEvent(ticket.ID, from, contracts.TicketBlocked, "pm.dispatch.fail", now)
	if ev != nil {
		ev.WorkerID = job.WorkerID
		ev.Detail = strings.TrimSpace(errMsg)
	}
	return ev, nil
}

func (s *Service) getPMDispatchJob(ctx context.Context, jobID uint) (contracts.PMDispatchJob, error) {
	_, db, err := s.require()
	if err != nil {
		return contracts.PMDispatchJob{}, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if jobID == 0 {
		return contracts.PMDispatchJob{}, fmt.Errorf("job_id 不能为空")
	}
	var job contracts.PMDispatchJob
	if err := db.WithContext(ctx).First(&job, jobID).Error; err != nil {
		return contracts.PMDispatchJob{}, err
	}
	return job, nil
}

func (s *Service) waitPMDispatchJob(ctx context.Context, jobID uint, pollInterval time.Duration) (contracts.PMDispatchJob, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if pollInterval <= 0 {
		pollInterval = defaultDispatchPollInterval
	}
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		job, err := s.getPMDispatchJob(ctx, jobID)
		if err != nil {
			return contracts.PMDispatchJob{}, err
		}
		if isPMDispatchTerminalStatus(job.Status) {
			return job, nil
		}
		select {
		case <-ctx.Done():
			return contracts.PMDispatchJob{}, ctx.Err()
		case <-ticker.C:
		}
	}
}

func marshalJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}
