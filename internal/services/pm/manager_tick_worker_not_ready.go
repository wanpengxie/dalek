package pm

import (
	"context"
	"fmt"
	"strings"
	"time"

	"dalek/internal/store"

	"gorm.io/gorm"
)

func (s *Service) demoteTicketBlockedOnWorkerNotReady(ctx context.Context, ticketID, workerID uint, reason string, now time.Time) error {
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
	if now.IsZero() {
		now = time.Now()
	}

	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var t store.Ticket
		if err := tx.WithContext(ctx).Select("id", "workflow_status").First(&t, ticketID).Error; err != nil {
			return err
		}
		from := normalizeTicketWorkflowStatus(t.WorkflowStatus)
		if from == store.TicketDone || from == store.TicketArchived || from == store.TicketBlocked {
			return nil
		}
		if err := tx.WithContext(ctx).Model(&store.Ticket{}).
			Where("id = ?", ticketID).
			Updates(map[string]any{
				"workflow_status": store.TicketBlocked,
				"updated_at":      now,
			}).Error; err != nil {
			return err
		}
		if err := s.appendTicketWorkflowEventTx(ctx, tx, ticketID, from, store.TicketBlocked, "pm.manager_tick", "worker 未就绪自动降级 blocked", map[string]any{
			"ticket_id": ticketID,
			"worker_id": workerID,
			"error":     reason,
		}, now); err != nil {
			return err
		}

		key := inboxKeyTicketIncident(ticketID, "worker_not_ready")
		title := fmt.Sprintf("worker 未就绪：t%d", ticketID)
		if workerID > 0 {
			key = inboxKeyWorkerIncident(workerID, "worker_not_ready")
			title = fmt.Sprintf("worker 未就绪：t%d w%d", ticketID, workerID)
		}
		_, err := s.upsertOpenInboxTx(ctx, tx, store.InboxItem{
			Key:      key,
			Status:   store.InboxOpen,
			Severity: store.InboxBlocker,
			Reason:   store.InboxIncident,
			Title:    title,
			Body:     reason,
			TicketID: ticketID,
			WorkerID: workerID,
		})
		return err
	})
}
