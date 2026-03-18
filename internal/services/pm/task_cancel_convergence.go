package pm

import (
	"context"
	"fmt"
	"strings"
	"time"

	"dalek/internal/contracts"
	"dalek/internal/services/ticketlifecycle"

	"gorm.io/gorm"
)

const (
	ticketBlockedReasonUserStopped     = "user_stopped"
	ticketBlockedReasonUserInterrupted = "user_interrupted"
)

type userInitiatedTaskCancelInput struct {
	TicketID  uint
	WorkerID  uint
	TaskRunID uint
	Cause     contracts.TaskCancelCause
	Source    string
	Reason    string
	EventID   uint
	Now       time.Time
}

func taskCancelCauseFromEvent(ev contracts.TaskEventScopeRow) contracts.TaskCancelCause {
	return taskCancelCauseFromMaps(map[string]any(ev.ToStateJSON), map[string]any(ev.PayloadJSON))
}

func taskCancelCauseFromTaskEvent(ev contracts.TaskEvent) contracts.TaskCancelCause {
	return taskCancelCauseFromMaps(map[string]any(ev.ToStateJSON), map[string]any(ev.PayloadJSON))
}

func taskCancelCauseFromMaps(toState, payload map[string]any) contracts.TaskCancelCause {
	for _, raw := range []string{
		mapString(payload, "cancel_cause"),
		mapString(toState, "cancel_cause"),
		mapString(toState, "error_code"),
		mapString(payload, "error_code"),
	} {
		if cause := contracts.ParseTaskCancelCause(raw); cause.Valid() {
			return cause
		}
	}
	return contracts.TaskCancelCauseUnknown
}

func isUserInitiatedTaskCancelCause(cause contracts.TaskCancelCause) bool {
	switch cause {
	case contracts.TaskCancelCauseUserStop, contracts.TaskCancelCauseUserInterrupt, contracts.TaskCancelCauseUserCancel:
		return true
	default:
		return false
	}
}

func taskCancelBlockedReason(cause contracts.TaskCancelCause) string {
	switch cause {
	case contracts.TaskCancelCauseUserInterrupt:
		return ticketBlockedReasonUserInterrupted
	default:
		return ticketBlockedReasonUserStopped
	}
}

func isUserInitiatedBlockedReason(reason string) bool {
	switch strings.TrimSpace(strings.ToLower(reason)) {
	case ticketBlockedReasonUserStopped, ticketBlockedReasonUserInterrupted:
		return true
	default:
		return false
	}
}

func userInitiatedTaskCancelSummary(cause contracts.TaskCancelCause) string {
	switch cause {
	case contracts.TaskCancelCauseUserInterrupt:
		return "用户主动中断 ticket"
	case contracts.TaskCancelCauseUserCancel:
		return "用户主动取消任务运行"
	default:
		return "用户主动停止 ticket"
	}
}

func userInitiatedTaskCancelTitle(cause contracts.TaskCancelCause, ticketID, workerID uint) string {
	switch cause {
	case contracts.TaskCancelCauseUserInterrupt:
		if workerID != 0 {
			return fmt.Sprintf("用户已中断：t%d w%d", ticketID, workerID)
		}
		return fmt.Sprintf("用户已中断：t%d", ticketID)
	case contracts.TaskCancelCauseUserCancel:
		if workerID != 0 {
			return fmt.Sprintf("任务已取消：t%d w%d", ticketID, workerID)
		}
		return fmt.Sprintf("任务已取消：t%d", ticketID)
	default:
		if workerID != 0 {
			return fmt.Sprintf("用户已停止：t%d w%d", ticketID, workerID)
		}
		return fmt.Sprintf("用户已停止：t%d", ticketID)
	}
}

func userInitiatedTaskCancelInboxKey(ticketID, workerID uint, blockedReason string) string {
	if workerID != 0 {
		return inboxKeyWorkerIncident(workerID, blockedReason)
	}
	return inboxKeyTicketIncident(ticketID, blockedReason)
}

func userInitiatedTaskCancelIdempotencyKey(ticketID, taskRunID, workerID uint, cause contracts.TaskCancelCause) string {
	return fmt.Sprintf("ticket:%d:user_cancel:%s:run:%d:worker:%d", ticketID, strings.TrimSpace(string(cause)), taskRunID, workerID)
}

func (s *Service) convergeUserInitiatedTaskCancel(ctx context.Context, input userInitiatedTaskCancelInput) (bool, error) {
	_, db, err := s.require()
	if err != nil {
		return false, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if input.TicketID == 0 {
		return false, fmt.Errorf("ticket_id 不能为空")
	}
	if !isUserInitiatedTaskCancelCause(input.Cause) {
		return false, nil
	}
	if input.Now.IsZero() {
		input.Now = time.Now()
	}
	input.Source = strings.TrimSpace(input.Source)
	if input.Source == "" {
		input.Source = "pm.task_canceled"
	}
	input.Reason = strings.TrimSpace(input.Reason)
	if input.Reason == "" {
		input.Reason = userInitiatedTaskCancelSummary(input.Cause)
	}
	blockedReason := taskCancelBlockedReason(input.Cause)

	blocked := false
	var statusEvent *StatusChangeEvent
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var ticket contracts.Ticket
		if err := tx.WithContext(ctx).
			Select("id", "workflow_status", "integration_status").
			First(&ticket, input.TicketID).Error; err != nil {
			return err
		}
		if contracts.CanonicalTicketWorkflowStatus(ticket.WorkflowStatus) != contracts.TicketActive {
			return nil
		}
		lifecycleResult, err := s.appendTicketLifecycleEventAndProjectSnapshotTx(ctx, tx, ticketlifecycle.AppendInput{
			TicketID:       input.TicketID,
			EventType:      contracts.TicketLifecycleRepaired,
			Source:         input.Source,
			ActorType:      contracts.TicketLifecycleActorUser,
			WorkerID:       input.WorkerID,
			TaskRunID:      input.TaskRunID,
			IdempotencyKey: userInitiatedTaskCancelIdempotencyKey(input.TicketID, input.TaskRunID, input.WorkerID, input.Cause),
			Payload: lifecycleRepairPayload(contracts.TicketBlocked, contracts.CanonicalIntegrationStatus(ticket.IntegrationStatus), map[string]any{
				"ticket_id":      input.TicketID,
				"worker_id":      input.WorkerID,
				"task_run_id":    input.TaskRunID,
				"blocked_reason": blockedReason,
				"failure_code":   string(input.Cause),
				"cancel_cause":   string(input.Cause),
				"reason":         input.Reason,
				"event_id":       input.EventID,
			}),
			CreatedAt: input.Now,
		})
		if err != nil {
			return err
		}
		if !lifecycleResult.WorkflowChanged() {
			return nil
		}
		if err := s.appendTicketWorkflowEventTx(ctx, tx, input.TicketID, lifecycleResult.Before.WorkflowStatus, lifecycleResult.After.WorkflowStatus, input.Source, input.Reason, map[string]any{
			"ticket_id":      input.TicketID,
			"worker_id":      input.WorkerID,
			"task_run_id":    input.TaskRunID,
			"blocked_reason": blockedReason,
			"failure_code":   string(input.Cause),
			"cancel_cause":   string(input.Cause),
			"event_id":       input.EventID,
		}, input.Now); err != nil {
			return err
		}
		if _, err := s.upsertOpenInboxTx(ctx, tx, contracts.InboxItem{
			Key:      userInitiatedTaskCancelInboxKey(input.TicketID, input.WorkerID, blockedReason),
			Status:   contracts.InboxOpen,
			Severity: contracts.InboxInfo,
			Reason:   contracts.InboxIncident,
			Title:    userInitiatedTaskCancelTitle(input.Cause, input.TicketID, input.WorkerID),
			Body:     input.Reason,
			TicketID: input.TicketID,
			WorkerID: input.WorkerID,
		}); err != nil {
			return err
		}
		blocked = true
		statusEvent = s.buildStatusChangeEvent(input.TicketID, lifecycleResult.Before.WorkflowStatus, lifecycleResult.After.WorkflowStatus, input.Source, input.Now)
		if statusEvent != nil {
			statusEvent.WorkerID = input.WorkerID
			statusEvent.Detail = input.Reason
		}
		return nil
	})
	if err != nil {
		return false, err
	}
	s.emitStatusChangeHookAsync(statusEvent)
	return blocked, nil
}

func latestTaskCancelCause(ctx context.Context, db *gorm.DB, workerID, taskRunID uint) (contracts.TaskCancelCause, error) {
	if taskRunID != 0 {
		ev, err := latestTaskCancelEventByRun(ctx, db, taskRunID)
		if err != nil {
			return contracts.TaskCancelCauseUnknown, err
		}
		if ev != nil {
			cause := taskCancelCauseFromTaskEvent(*ev)
			if cause.Valid() || workerID == 0 {
				return cause, nil
			}
		}
	}
	if workerID == 0 {
		return contracts.TaskCancelCauseUnknown, nil
	}
	ev, err := latestTaskCancelEventByWorker(ctx, db, workerID)
	if err != nil {
		return contracts.TaskCancelCauseUnknown, err
	}
	if ev == nil {
		return contracts.TaskCancelCauseUnknown, nil
	}
	return taskCancelCauseFromTaskEvent(*ev), nil
}

func latestTaskCancelEventByRun(ctx context.Context, db *gorm.DB, taskRunID uint) (*contracts.TaskEvent, error) {
	if db == nil || taskRunID == 0 {
		return nil, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	var ev contracts.TaskEvent
	if err := db.WithContext(ctx).
		Where("task_run_id = ? AND event_type = ?", taskRunID, "task_canceled").
		Order("id desc").
		First(&ev).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &ev, nil
}

func latestTaskCancelEventByWorker(ctx context.Context, db *gorm.DB, workerID uint) (*contracts.TaskEvent, error) {
	if db == nil || workerID == 0 {
		return nil, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	var ev contracts.TaskEvent
	if err := db.WithContext(ctx).
		Table("task_events AS ev").
		Select("ev.id", "ev.created_at", "ev.task_run_id", "ev.event_type", "ev.from_state_json", "ev.to_state_json", "ev.note", "ev.payload_json").
		Joins("JOIN task_runs tr ON tr.id = ev.task_run_id").
		Where("tr.worker_id = ? AND ev.event_type = ?", workerID, "task_canceled").
		Order("ev.id desc").
		Limit(1).
		Scan(&ev).Error; err != nil {
		return nil, err
	}
	if ev.ID == 0 {
		return nil, nil
	}
	return &ev, nil
}
