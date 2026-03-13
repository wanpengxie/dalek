package pm

import (
	"context"
	"dalek/internal/contracts"
	"dalek/internal/fsm"
	"dalek/internal/services/ticketlifecycle"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
)

func (s *Service) demoteTicketBlockedOnWorkerNotReady(ctx context.Context, ticketID, workerID uint, reason, source string, now time.Time) error {
	if ticketID == 0 {
		return nil
	}
	_, db, err := s.require()
	if err != nil {
		return err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "worker 未就绪，已自动降级为 blocked"
	}
	source = strings.TrimSpace(source)
	if source == "" {
		source = "pm.queue_consumer"
	}
	if now.IsZero() {
		now = time.Now()
	}

	var statusEvent *StatusChangeEvent
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var t contracts.Ticket
		if err := tx.WithContext(ctx).Select("id", "workflow_status").First(&t, ticketID).Error; err != nil {
			return err
		}
		from := contracts.CanonicalTicketWorkflowStatus(t.WorkflowStatus)
		if !fsm.ShouldDemoteOnDispatchFailed(from) {
			return nil
		}
		lifecycleResult, err := s.appendTicketLifecycleEventAndProjectSnapshotTx(ctx, tx, ticketlifecycle.AppendInput{
			TicketID:       ticketID,
			EventType:      contracts.TicketLifecycleRepaired,
			Source:         source,
			ActorType:      contracts.TicketLifecycleActorSystem,
			WorkerID:       workerID,
			IdempotencyKey: ticketlifecycle.RepairedIdempotencyKey(ticketID, source+".worker_not_ready", now),
			Payload: lifecycleRepairPayload(contracts.TicketBlocked, contracts.IntegrationNone, map[string]any{
				"ticket_id": ticketID,
				"worker_id": workerID,
				"error":     reason,
			}),
			CreatedAt: now,
		})
		if err != nil {
			return err
		}
		if !lifecycleResult.WorkflowChanged() {
			return nil
		}
		if err := s.appendTicketWorkflowEventTx(ctx, tx, ticketID, lifecycleResult.Before.WorkflowStatus, lifecycleResult.After.WorkflowStatus, source, "worker 未就绪自动降级 blocked", map[string]any{
			"ticket_id": ticketID,
			"worker_id": workerID,
			"error":     reason,
		}, now); err != nil {
			return err
		}
		statusEvent = s.buildStatusChangeEvent(ticketID, lifecycleResult.Before.WorkflowStatus, lifecycleResult.After.WorkflowStatus, source, now)
		if statusEvent != nil {
			statusEvent.WorkerID = workerID
			statusEvent.Detail = reason
		}

		key := inboxKeyTicketIncident(ticketID, "worker_not_ready")
		title := fmt.Sprintf("worker 未就绪：t%d", ticketID)
		if workerID > 0 {
			key = inboxKeyWorkerIncident(workerID, "worker_not_ready")
			title = fmt.Sprintf("worker 未就绪：t%d w%d", ticketID, workerID)
		}
		_, err = s.upsertOpenInboxTx(ctx, tx, contracts.InboxItem{
			Key:      key,
			Status:   contracts.InboxOpen,
			Severity: contracts.InboxBlocker,
			Reason:   contracts.InboxIncident,
			Title:    title,
			Body:     reason,
			TicketID: ticketID,
			WorkerID: workerID,
		})
		return err
	})
	if err != nil {
		return err
	}
	s.emitStatusChangeHookAsync(statusEvent)
	return nil
}
