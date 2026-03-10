package pm

import (
	"context"
	"fmt"
	"strings"
	"time"

	"dalek/internal/contracts"
	"dalek/internal/fsm"
	"dalek/internal/services/ticketlifecycle"

	"gorm.io/gorm"
)

func (s *Service) promoteTicketActiveOnWorkerRunAccepted(
	ctx context.Context,
	ticketID, workerID, taskRunID uint,
	source string,
	actorType contracts.TicketLifecycleActorType,
	payload map[string]any,
	now time.Time,
) (bool, contracts.TicketWorkflowStatus, error) {
	_, db, err := s.require()
	if err != nil {
		return false, "", err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if now.IsZero() {
		now = time.Now()
	}
	source = strings.TrimSpace(source)
	if source == "" {
		source = "pm.activation"
	}
	payloadCopy := map[string]any{}
	for k, v := range payload {
		payloadCopy[k] = v
	}
	payloadCopy["ticket_id"] = ticketID
	payloadCopy["worker_id"] = workerID
	if taskRunID != 0 {
		payloadCopy["task_run_id"] = taskRunID
	}

	activated := false
	fromStatus := contracts.TicketWorkflowStatus("")
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var ticket contracts.Ticket
		if err := tx.WithContext(ctx).Select("id", "workflow_status").First(&ticket, ticketID).Error; err != nil {
			return err
		}
		fromStatus = contracts.CanonicalTicketWorkflowStatus(ticket.WorkflowStatus)
		if fromStatus == "" {
			fromStatus = contracts.TicketBacklog
		}
		if fromStatus == contracts.TicketActive {
			return nil
		}
		if !fsm.ShouldPromoteOnDispatchClaim(fromStatus) {
			return nil
		}
		res := tx.WithContext(ctx).Model(&contracts.Ticket{}).
			Where("id = ? AND workflow_status <> ?", ticketID, contracts.TicketActive).
			Updates(map[string]any{
				"workflow_status": contracts.TicketActive,
				"updated_at":      now,
			})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return nil
		}
		activated = true
		if err := s.appendTicketWorkflowEventTx(ctx, tx, ticketID, fromStatus, contracts.TicketActive, source, "worker run 已被系统接受，ticket 进入 active", payloadCopy, now); err != nil {
			return err
		}
		return s.appendTicketLifecycleEventTx(ctx, tx, ticketlifecycle.AppendInput{
			TicketID:       ticketID,
			EventType:      contracts.TicketLifecycleActivated,
			Source:         source,
			ActorType:      actorType,
			WorkerID:       workerID,
			TaskRunID:      taskRunID,
			IdempotencyKey: ticketlifecycle.ActivatedRunIdempotencyKey(ticketID, taskRunID),
			Payload:        payloadCopy,
			CreatedAt:      now,
		})
	})
	if err != nil {
		return false, fromStatus, fmt.Errorf("promote ticket active on worker run accepted: %w", err)
	}
	return activated, fromStatus, nil
}
