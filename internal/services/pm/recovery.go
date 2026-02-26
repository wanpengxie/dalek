package pm

import (
	"context"
	"dalek/internal/contracts"
	"fmt"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"
)

type RecoverySummary struct {
	DispatchJobs   int
	TaskRuns       int
	TicketsQueued  int
	TicketsBlocked int
}

type dispatchRecoveryMode struct {
	source         string
	errorCode      string
	errorMessage   string
	eventType      string
	inboxKeyPrefix string
	inboxTitle     string
	leaseOnly      bool
}

var (
	recoveryModeDaemonRestart = dispatchRecoveryMode{
		source:         "daemon_recovery",
		errorCode:      "daemon_recovered",
		errorMessage:   "daemon restart recovery: dispatch job marked failed",
		eventType:      "daemon_recovery_dispatch_failed",
		inboxKeyPrefix: "daemon_recovery_dispatch",
		inboxTitle:     "daemon recovery: dispatch %d 已标记失败",
	}
	recoveryModeLeaseExpired = dispatchRecoveryMode{
		source:         "lease_expired",
		errorCode:      "lease_expired",
		errorMessage:   "dispatch lease expired: dispatch job marked failed",
		eventType:      "lease_expired_dispatch_failed",
		inboxKeyPrefix: "lease_expired_dispatch",
		inboxTitle:     "lease expired: dispatch %d 已标记失败",
		leaseOnly:      true,
	}
)

func (s *Service) RecoverStuckDispatchJobs(ctx context.Context, projectName string, now time.Time, autopilotEnabled bool) (RecoverySummary, map[uint]struct{}, error) {
	return s.recoverDispatchJobs(ctx, projectName, now, autopilotEnabled, recoveryModeDaemonRestart)
}

func (s *Service) CheckExpiredDispatchLeases(ctx context.Context, projectName string, now time.Time, autopilotEnabled bool) (RecoverySummary, error) {
	summary, _, err := s.recoverDispatchJobs(ctx, projectName, now, autopilotEnabled, recoveryModeLeaseExpired)
	return summary, err
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
	projectName = strings.TrimSpace(projectName)

	var runs []contracts.TaskRun
	query := db.WithContext(ctx).
		Where("orchestration_state IN ?", []contracts.TaskOrchestrationState{contracts.TaskPending, contracts.TaskRunning})
	if excluded := sortedRunIDsFromSet(excludedRunIDs); len(excluded) > 0 {
		query = query.Where("id NOT IN ?", excluded)
	}
	if err := query.Order("id asc").Find(&runs).Error; err != nil {
		return 0, err
	}
	if len(runs) == 0 {
		return 0, nil
	}

	recovered := 0
	for _, run := range runs {
		errMsg := "daemon restart recovery: previous run marked failed"
		err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			changed, err := markTaskRunFailedForRecoveryTx(tx, run.ID, "daemon_recovered", errMsg, now)
			if err != nil {
				return err
			}
			if !changed {
				return nil
			}
			recovered++
			_ = tx.WithContext(ctx).Create(&contracts.TaskEvent{
				TaskRunID:   run.ID,
				EventType:   "daemon_recovery_failed",
				ToStateJSON: contracts.JSONMap{"orchestration_state": "failed"},
				Note:        errMsg,
				PayloadJSON: contracts.JSONMap{"source": "daemon_recovery"},
				CreatedAt:   now,
			}).Error
			_, _ = s.upsertOpenInboxTx(ctx, tx, contracts.InboxItem{
				Key:      fmt.Sprintf("daemon_recovery_run_%d", run.ID),
				Status:   contracts.InboxOpen,
				Severity: contracts.InboxWarn,
				Reason:   contracts.InboxIncident,
				Title:    fmt.Sprintf("daemon recovery: run %d 已标记失败", run.ID),
				Body:     fmt.Sprintf("project=%s owner=%s task=%s ticket=%d worker=%d", projectName, string(run.OwnerType), strings.TrimSpace(run.TaskType), run.TicketID, run.WorkerID),
				TicketID: run.TicketID,
				WorkerID: run.WorkerID,
			})
			return nil
		})
		if err != nil {
			return recovered, err
		}
	}
	return recovered, nil
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
		Where("orchestration_state IN ?", []contracts.TaskOrchestrationState{contracts.TaskPending, contracts.TaskRunning}).
		Pluck("id", &runIDs).Error; err != nil {
		return nil, err
	}
	return runIDs, nil
}

func (s *Service) UpdateRecoverySummary(ctx context.Context, pmStateID uint, now time.Time, dispatchJobs, taskRuns, notes, workers int) error {
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
		"last_recovery_dispatch_jobs": dispatchJobs,
		"last_recovery_task_runs":     taskRuns,
		"last_recovery_notes":         notes,
		"last_recovery_workers":       workers,
		"updated_at":                  now,
	}).Error
}

func (s *Service) recoverDispatchJobs(ctx context.Context, projectName string, now time.Time, autopilotEnabled bool, mode dispatchRecoveryMode) (RecoverySummary, map[uint]struct{}, error) {
	_, db, err := s.require()
	if err != nil {
		return RecoverySummary{}, nil, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if now.IsZero() {
		now = time.Now()
	}
	projectName = strings.TrimSpace(projectName)

	var jobs []contracts.PMDispatchJob
	query := db.WithContext(ctx).Order("id asc")
	if mode.leaseOnly {
		query = query.Where("status = ? AND lease_expires_at IS NOT NULL AND lease_expires_at < ?", contracts.PMDispatchRunning, now)
	} else {
		query = query.Where("status IN ?", []contracts.PMDispatchJobStatus{contracts.PMDispatchPending, contracts.PMDispatchRunning})
	}
	if err := query.Find(&jobs).Error; err != nil {
		return RecoverySummary{}, nil, err
	}
	recoveredRunIDs := make(map[uint]struct{})
	if len(jobs) == 0 {
		return RecoverySummary{}, recoveredRunIDs, nil
	}

	targetStatus, retryAction, retryReason := recoveryTicketPolicy(autopilotEnabled, mode.leaseOnly)
	out := RecoverySummary{}
	for _, job := range jobs {
		recovered := false
		taskRunRecovered := false
		queued := false
		blocked := false
		err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			res := tx.Model(&contracts.PMDispatchJob{})
			if mode.leaseOnly {
				res = res.Where("id = ? AND status = ? AND lease_expires_at IS NOT NULL AND lease_expires_at < ?", job.ID, contracts.PMDispatchRunning, now)
			} else {
				res = res.Where("id = ? AND status IN ?", job.ID, []contracts.PMDispatchJobStatus{contracts.PMDispatchPending, contracts.PMDispatchRunning})
			}
			res = res.Updates(map[string]any{
				"status":            contracts.PMDispatchFailed,
				"error":             mode.errorMessage,
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
				return nil
			}
			recovered = true

			if job.TaskRunID != 0 {
				changed, err := markTaskRunFailedForRecoveryTx(tx, job.TaskRunID, mode.errorCode, mode.errorMessage, now)
				if err != nil {
					return err
				}
				if changed {
					taskRunRecovered = true
					_ = tx.WithContext(ctx).Create(&contracts.TaskEvent{
						TaskRunID:   job.TaskRunID,
						EventType:   mode.eventType,
						ToStateJSON: contracts.JSONMap{"orchestration_state": "failed"},
						Note:        mode.errorMessage,
						PayloadJSON: contracts.JSONMap{
							"source":          mode.source,
							"dispatch_job_id": job.ID,
							"ticket_id":       job.TicketID,
							"worker_id":       job.WorkerID,
							"request_id":      strings.TrimSpace(job.RequestID),
							"retry_action":    retryAction,
						},
						CreatedAt: now,
					}).Error
				}
			}

			changed, err := s.applyTicketStatusForRecoveryTx(ctx, tx, job, targetStatus, retryReason, "daemon."+mode.source, now)
			if err != nil {
				return err
			}
			if changed {
				if targetStatus == contracts.TicketQueued {
					queued = true
				} else if targetStatus == contracts.TicketBlocked {
					blocked = true
				}
			}

			_, _ = s.upsertOpenInboxTx(ctx, tx, contracts.InboxItem{
				Key:      fmt.Sprintf("%s_%d", mode.inboxKeyPrefix, job.ID),
				Status:   contracts.InboxOpen,
				Severity: contracts.InboxWarn,
				Reason:   contracts.InboxIncident,
				Title:    fmt.Sprintf(mode.inboxTitle, job.ID),
				Body:     fmt.Sprintf("project=%s ticket=%d worker=%d request=%s action=%s", projectName, job.TicketID, job.WorkerID, strings.TrimSpace(job.RequestID), retryAction),
				TicketID: job.TicketID,
				WorkerID: job.WorkerID,
			})
			return nil
		})
		if err != nil {
			return out, recoveredRunIDs, err
		}
		if recovered {
			out.DispatchJobs++
		}
		if taskRunRecovered {
			out.TaskRuns++
			recoveredRunIDs[job.TaskRunID] = struct{}{}
		}
		if queued {
			out.TicketsQueued++
		}
		if blocked {
			out.TicketsBlocked++
		}
	}
	return out, recoveredRunIDs, nil
}

func (s *Service) applyTicketStatusForRecoveryTx(ctx context.Context, tx *gorm.DB, job contracts.PMDispatchJob, target contracts.TicketWorkflowStatus, reason, source string, now time.Time) (bool, error) {
	if tx == nil || job.TicketID == 0 {
		return false, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if now.IsZero() {
		now = time.Now()
	}
	var ticket contracts.Ticket
	if err := tx.WithContext(ctx).Select("id", "workflow_status").First(&ticket, job.TicketID).Error; err != nil {
		return false, err
	}
	from := contracts.CanonicalTicketWorkflowStatus(ticket.WorkflowStatus)
	if from == contracts.TicketDone || from == contracts.TicketArchived || from == target {
		return false, nil
	}
	if err := tx.WithContext(ctx).Model(&contracts.Ticket{}).
		Where("id = ?", ticket.ID).
		Updates(map[string]any{
			"workflow_status": target,
			"updated_at":      now,
		}).Error; err != nil {
		return false, err
	}
	if err := s.appendTicketWorkflowEventTx(ctx, tx, ticket.ID, from, target, strings.TrimSpace(source), strings.TrimSpace(reason), map[string]any{
		"ticket_id":       ticket.ID,
		"worker_id":       job.WorkerID,
		"dispatch_id":     job.ID,
		"request_id":      strings.TrimSpace(job.RequestID),
		"target_workflow": string(target),
	}, now); err != nil {
		return false, err
	}
	return true, nil
}

func markTaskRunFailedForRecoveryTx(tx *gorm.DB, runID uint, errorCode, errMsg string, now time.Time) (bool, error) {
	if tx == nil || runID == 0 {
		return false, nil
	}
	errorCode = strings.TrimSpace(errorCode)
	if errorCode == "" {
		errorCode = "daemon_recovered"
	}
	errMsg = strings.TrimSpace(errMsg)
	res := tx.Model(&contracts.TaskRun{}).
		Where("id = ? AND orchestration_state IN ?", runID, []contracts.TaskOrchestrationState{contracts.TaskPending, contracts.TaskRunning}).
		Updates(map[string]any{
			"orchestration_state": contracts.TaskFailed,
			"error_code":          errorCode,
			"error_message":       errMsg,
			"finished_at":         now,
			"updated_at":          now,
		})
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected > 0, nil
}

func recoveryTicketPolicy(autopilotEnabled, leaseOnly bool) (contracts.TicketWorkflowStatus, string, string) {
	if autopilotEnabled {
		if leaseOnly {
			return contracts.TicketQueued, "queued", "dispatch lease expired: ticket queued for redispatch"
		}
		return contracts.TicketQueued, "queued", "daemon recovery: dispatch interrupted, ticket queued for redispatch"
	}
	if leaseOnly {
		return contracts.TicketBlocked, "blocked", "dispatch lease expired: ticket moved to blocked"
	}
	return contracts.TicketBlocked, "blocked", "daemon recovery: dispatch interrupted, ticket moved to blocked"
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
