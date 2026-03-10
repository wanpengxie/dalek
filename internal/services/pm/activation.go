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

func (s *Service) acceptWorkerRun(
	ctx context.Context,
	ticketID uint,
	w *contracts.Worker,
	taskRunID uint,
	source string,
	actorType contracts.TicketLifecycleActorType,
	payload map[string]any,
) (bool, error) {
	if taskRunID == 0 {
		return false, fmt.Errorf("accept worker run: task_run_id 不能为空")
	}
	if w == nil || w.ID == 0 {
		return false, fmt.Errorf("accept worker run: worker 不能为空")
	}
	now := time.Now()
	if w.Status != contracts.WorkerRunning {
		if err := s.worker.MarkWorkerRunning(ctx, w.ID, now); err != nil {
			return false, fmt.Errorf("mark worker running on accepted run: %w", err)
		}
		w.Status = contracts.WorkerRunning
	}
	activated, _, err := s.promoteTicketActiveOnWorkerRunAccepted(ctx, ticketID, w.ID, taskRunID, source, actorType, payload, now)
	if err != nil {
		return false, err
	}
	return activated, nil
}

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
		lifecycleResult, err := s.appendTicketLifecycleEventAndProjectSnapshotTx(ctx, tx, ticketlifecycle.AppendInput{
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
		if err != nil {
			return err
		}
		if !lifecycleResult.WorkflowChanged() {
			return nil
		}
		activated = true
		return s.appendTicketWorkflowEventTx(ctx, tx, ticketID, lifecycleResult.Before.WorkflowStatus, lifecycleResult.After.WorkflowStatus, source, "worker run 已被系统接受，ticket 进入 active", payloadCopy, now)
	})
	if err != nil {
		return false, fromStatus, fmt.Errorf("promote ticket active on worker run accepted: %w", err)
	}
	return activated, fromStatus, nil
}
