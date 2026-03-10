package pm

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"dalek/internal/contracts"
	"dalek/internal/services/ticketlifecycle"

	"gorm.io/gorm"
)

type executionLossInput struct {
	TicketID    uint
	WorkerID    uint
	TaskRunID   uint
	Source      string
	FailureCode string
	Reason      string
	Now         time.Time
}

type executionLossResult struct {
	TicketID       uint
	WorkerID       uint
	TaskRunID      uint
	RetryCount     int
	TargetWorkflow contracts.TicketWorkflowStatus
	Requeued       bool
	Escalated      bool
}

func (s *Service) queuedRetryBackoffRemaining(ctx context.Context, ticketID uint, now time.Time) (uint, time.Duration, error) {
	if ticketID == 0 {
		return 0, 0, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	w, err := s.worker.LatestWorker(ctx, ticketID)
	if err != nil || w == nil {
		return 0, 0, err
	}
	return w.ID, workerRetryBackoffRemaining(w, now), nil
}

func workerRetryBackoffRemaining(w *contracts.Worker, now time.Time) time.Duration {
	if w == nil || w.RetryCount <= 0 || w.LastRetryAt == nil || w.LastRetryAt.IsZero() {
		return 0
	}
	if now.IsZero() {
		now = time.Now()
	}
	wait := zombieRetryBackoff(w.RetryCount - 1)
	if wait <= 0 {
		return 0
	}
	elapsed := now.Sub(*w.LastRetryAt)
	if elapsed >= wait {
		return 0
	}
	return wait - elapsed
}

func executionLossHash(parts ...string) string {
	raw := strings.TrimSpace(strings.Join(parts, "|"))
	sum := sha1.Sum([]byte(raw))
	return hex.EncodeToString(sum[:])
}

func (s *Service) convergeExecutionLost(ctx context.Context, input executionLossInput) (executionLossResult, error) {
	_, db, err := s.require()
	if err != nil {
		return executionLossResult{}, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if input.TicketID == 0 {
		return executionLossResult{}, fmt.Errorf("ticket_id 不能为空")
	}
	if input.Now.IsZero() {
		input.Now = time.Now()
	}
	input.Source = strings.TrimSpace(input.Source)
	if input.Source == "" {
		input.Source = "pm.execution"
	}
	input.FailureCode = strings.TrimSpace(strings.ToLower(input.FailureCode))
	if input.FailureCode == "" {
		input.FailureCode = "execution_lost"
	}
	input.Reason = strings.TrimSpace(input.Reason)
	if input.Reason == "" {
		input.Reason = "worker execution lost"
	}

	var (
		result      = executionLossResult{TicketID: input.TicketID, WorkerID: input.WorkerID, TaskRunID: input.TaskRunID}
		statusEvent *StatusChangeEvent
	)
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var ticket contracts.Ticket
		if err := tx.WithContext(ctx).Select("id", "workflow_status").First(&ticket, input.TicketID).Error; err != nil {
			return err
		}
		from := contracts.CanonicalTicketWorkflowStatus(ticket.WorkflowStatus)
		if from == "" {
			from = contracts.TicketBacklog
		}
		result.TargetWorkflow = from
		if from != contracts.TicketActive {
			return nil
		}

		var worker contracts.Worker
		hasWorker := false
		if input.WorkerID != 0 {
			if err := tx.WithContext(ctx).
				Select("id", "ticket_id", "retry_count", "last_retry_at", "last_error_hash").
				First(&worker, input.WorkerID).Error; err != nil {
				if err != gorm.ErrRecordNotFound {
					return err
				}
			} else {
				hasWorker = true
			}
		}

		taskRunID := input.TaskRunID
		if taskRunID == 0 && hasWorker {
			latestRunID, err := latestWorkerTaskRunIDTx(ctx, tx, worker.ID)
			if err != nil {
				return err
			}
			taskRunID = latestRunID
		}
		result.TaskRunID = taskRunID

		errHash := executionLossHash(
			fmt.Sprintf("ticket:%d", input.TicketID),
			fmt.Sprintf("worker:%d", input.WorkerID),
			fmt.Sprintf("run:%d", taskRunID),
			input.FailureCode,
			input.Reason,
		)
		if err := s.appendTicketLifecycleEventTx(ctx, tx, ticketlifecycle.AppendInput{
			TicketID:       input.TicketID,
			EventType:      contracts.TicketLifecycleExecutionLost,
			Source:         input.Source,
			ActorType:      contracts.TicketLifecycleActorSystem,
			WorkerID:       input.WorkerID,
			TaskRunID:      taskRunID,
			IdempotencyKey: ticketlifecycle.ExecutionLostIdempotencyKey(input.TicketID, taskRunID, input.WorkerID),
			Payload: map[string]any{
				"ticket_id":    input.TicketID,
				"worker_id":    input.WorkerID,
				"task_run_id":  taskRunID,
				"failure_code": input.FailureCode,
				"reason":       input.Reason,
			},
			CreatedAt: input.Now,
		}); err != nil {
			return err
		}

		target := contracts.TicketQueued
		retryCount := 0
		if hasWorker {
			retryCount = worker.RetryCount
		}
		exhausted := !hasWorker || retryCount >= defaultZombieMaxRetries
		workerUpdates := map[string]any{
			"updated_at":      input.Now,
			"last_error_hash": errHash,
		}
		if exhausted {
			target = contracts.TicketBlocked
			workerUpdates["last_retry_at"] = &input.Now
		} else {
			retryCount++
			workerUpdates["retry_count"] = retryCount
			workerUpdates["last_retry_at"] = &input.Now
		}
		result.RetryCount = retryCount
		result.TargetWorkflow = target
		result.Requeued = target == contracts.TicketQueued
		result.Escalated = target == contracts.TicketBlocked

		if hasWorker {
			if err := tx.WithContext(ctx).Model(&contracts.Worker{}).
				Where("id = ?", worker.ID).
				Updates(workerUpdates).Error; err != nil {
				return err
			}
		}

		if err := tx.WithContext(ctx).Model(&contracts.Ticket{}).
			Where("id = ? AND workflow_status = ?", input.TicketID, contracts.TicketActive).
			Updates(map[string]any{
				"workflow_status": target,
				"updated_at":      input.Now,
			}).Error; err != nil {
			return err
		}
		if err := s.appendTicketWorkflowEventTx(ctx, tx, input.TicketID, contracts.TicketActive, target, input.Source, executionConvergenceReason(target, exhausted), map[string]any{
			"ticket_id":    input.TicketID,
			"worker_id":    input.WorkerID,
			"task_run_id":  taskRunID,
			"failure_code": input.FailureCode,
			"reason":       input.Reason,
			"retry_count":  retryCount,
			"max_retries":  defaultZombieMaxRetries,
		}, input.Now); err != nil {
			return err
		}
		statusEvent = s.buildStatusChangeEvent(input.TicketID, contracts.TicketActive, target, input.Source, input.Now)
		if statusEvent != nil {
			statusEvent.WorkerID = input.WorkerID
			statusEvent.Detail = input.Reason
		}

		if target == contracts.TicketQueued {
			return s.appendTicketLifecycleEventTx(ctx, tx, ticketlifecycle.AppendInput{
				TicketID:       input.TicketID,
				EventType:      contracts.TicketLifecycleRequeued,
				Source:         input.Source,
				ActorType:      contracts.TicketLifecycleActorSystem,
				WorkerID:       input.WorkerID,
				TaskRunID:      taskRunID,
				IdempotencyKey: ticketlifecycle.RequeuedIdempotencyKey(input.TicketID, taskRunID, input.WorkerID, retryCount),
				Payload: map[string]any{
					"ticket_id":        input.TicketID,
					"worker_id":        input.WorkerID,
					"task_run_id":      taskRunID,
					"failure_code":     input.FailureCode,
					"reason":           input.Reason,
					"retry_count":      retryCount,
					"max_retries":      defaultZombieMaxRetries,
					"target_workflow":  string(contracts.TicketQueued),
					"retry_backoff_ms": workerRetryBackoffDurationMS(retryCount),
				},
				CreatedAt: input.Now,
			})
		}

		if _, err := s.upsertOpenInboxTx(ctx, tx, contracts.InboxItem{
			Key:      executionEscalatedInboxKey(input.TicketID, input.WorkerID),
			Status:   contracts.InboxOpen,
			Severity: contracts.InboxBlocker,
			Reason:   contracts.InboxIncident,
			Title:    executionEscalatedInboxTitle(input.TicketID, input.WorkerID),
			Body:     input.Reason,
			TicketID: input.TicketID,
			WorkerID: input.WorkerID,
		}); err != nil {
			return err
		}
		return s.appendTicketLifecycleEventTx(ctx, tx, ticketlifecycle.AppendInput{
			TicketID:       input.TicketID,
			EventType:      contracts.TicketLifecycleExecutionEscalated,
			Source:         input.Source,
			ActorType:      contracts.TicketLifecycleActorSystem,
			WorkerID:       input.WorkerID,
			TaskRunID:      taskRunID,
			IdempotencyKey: ticketlifecycle.ExecutionEscalatedIdempotencyKey(input.TicketID, taskRunID, input.WorkerID, retryCount),
			Payload: map[string]any{
				"ticket_id":       input.TicketID,
				"worker_id":       input.WorkerID,
				"task_run_id":     taskRunID,
				"failure_code":    input.FailureCode,
				"reason":          input.Reason,
				"retry_count":     retryCount,
				"max_retries":     defaultZombieMaxRetries,
				"target_workflow": string(contracts.TicketBlocked),
				"blocked_reason":  "system_incident",
			},
			CreatedAt: input.Now,
		})
	})
	if err != nil {
		return result, err
	}
	s.emitStatusChangeHookAsync(statusEvent)
	return result, nil
}

func executionConvergenceReason(target contracts.TicketWorkflowStatus, exhausted bool) string {
	if target == contracts.TicketQueued {
		return "execution lost，ticket 回 queued 等待 auto-retry"
	}
	if exhausted {
		return "execution lost，自动重试耗尽，ticket 升级 blocked(system_incident)"
	}
	return "execution lost 收敛"
}

func executionEscalatedInboxKey(ticketID, workerID uint) string {
	if workerID != 0 {
		return inboxKeyWorkerIncident(workerID, "execution_escalated")
	}
	return inboxKeyTicketIncident(ticketID, "execution_escalated")
}

func executionEscalatedInboxTitle(ticketID, workerID uint) string {
	if workerID != 0 {
		return fmt.Sprintf("执行丢失已升级：t%d w%d", ticketID, workerID)
	}
	return fmt.Sprintf("执行丢失已升级：t%d", ticketID)
}

func workerRetryBackoffDurationMS(retryCount int) int64 {
	if retryCount <= 0 {
		return 0
	}
	return int64(zombieRetryBackoff(retryCount-1) / time.Millisecond)
}
