package pm

import (
	"context"
	"strings"
	"time"

	"dalek/internal/contracts"
	"dalek/internal/fsm"
	"dalek/internal/services/ticketlifecycle"

	"gorm.io/gorm"
)

func (s *Service) promoteTicketActiveOnDispatchClaimTx(ctx context.Context, tx *gorm.DB, job contracts.PMDispatchJob, now time.Time) (*StatusChangeEvent, error) {
	if tx == nil || job.TicketID == 0 {
		return nil, nil
	}
	var ticket contracts.Ticket
	if err := tx.WithContext(ctx).Select("id", "workflow_status").First(&ticket, job.TicketID).Error; err != nil {
		return nil, err
	}
	from := contracts.CanonicalTicketWorkflowStatus(ticket.WorkflowStatus)
	if !fsm.ShouldPromoteOnDispatchClaim(from) {
		return nil, nil
	}
	lifecycleResult, err := s.appendTicketLifecycleEventAndProjectSnapshotTx(ctx, tx, ticketlifecycle.AppendInput{
		TicketID:       ticket.ID,
		EventType:      contracts.TicketLifecycleActivated,
		Source:         "pm.dispatch",
		ActorType:      contracts.TicketLifecycleActorPM,
		WorkerID:       job.WorkerID,
		TaskRunID:      job.TaskRunID,
		IdempotencyKey: ticketlifecycle.ActivatedDispatchIdempotencyKey(ticket.ID, job.ID),
		Payload: map[string]any{
			"ticket_id":   ticket.ID,
			"worker_id":   job.WorkerID,
			"dispatch_id": job.ID,
			"request_id":  strings.TrimSpace(job.RequestID),
		},
		CreatedAt: now,
	})
	if err != nil {
		return nil, err
	}
	if !lifecycleResult.WorkflowChanged() {
		return nil, nil
	}
	if err := s.appendTicketWorkflowEventTx(ctx, tx, ticket.ID, lifecycleResult.Before.WorkflowStatus, lifecycleResult.After.WorkflowStatus, "pm.dispatch", "dispatch 开始推进到 active", map[string]any{
		"ticket_id":   ticket.ID,
		"worker_id":   job.WorkerID,
		"dispatch_id": job.ID,
		"request_id":  strings.TrimSpace(job.RequestID),
	}, now); err != nil {
		return nil, err
	}
	return s.buildStatusChangeEvent(ticket.ID, lifecycleResult.Before.WorkflowStatus, lifecycleResult.After.WorkflowStatus, "pm.dispatch.claim", now), nil
}

func (s *Service) demoteTicketBlockedOnDispatchFailedTx(ctx context.Context, tx *gorm.DB, job contracts.PMDispatchJob, errMsg string, now time.Time) (*StatusChangeEvent, error) {
	if tx == nil || job.TicketID == 0 {
		return nil, nil
	}
	var ticket contracts.Ticket
	if err := tx.WithContext(ctx).Select("id", "workflow_status").First(&ticket, job.TicketID).Error; err != nil {
		return nil, err
	}
	from := contracts.CanonicalTicketWorkflowStatus(ticket.WorkflowStatus)
	if !fsm.ShouldDemoteOnDispatchFailed(from) {
		return nil, nil
	}
	lifecycleResult, err := s.appendTicketLifecycleEventAndProjectSnapshotTx(ctx, tx, ticketlifecycle.AppendInput{
		TicketID:       ticket.ID,
		EventType:      contracts.TicketLifecycleRepaired,
		Source:         "pm.dispatch",
		ActorType:      contracts.TicketLifecycleActorSystem,
		WorkerID:       job.WorkerID,
		TaskRunID:      job.TaskRunID,
		IdempotencyKey: ticketlifecycle.RepairedIdempotencyKey(ticket.ID, "pm.dispatch.fail", now),
		Payload: lifecycleRepairPayload(contracts.TicketBlocked, contracts.IntegrationNone, map[string]any{
			"ticket_id":   ticket.ID,
			"worker_id":   job.WorkerID,
			"dispatch_id": job.ID,
			"request_id":  strings.TrimSpace(job.RequestID),
			"error":       strings.TrimSpace(errMsg),
		}),
		CreatedAt: now,
	})
	if err != nil {
		return nil, err
	}
	if !lifecycleResult.WorkflowChanged() {
		return nil, nil
	}
	if err := s.appendTicketWorkflowEventTx(ctx, tx, ticket.ID, lifecycleResult.Before.WorkflowStatus, lifecycleResult.After.WorkflowStatus, "pm.dispatch", "dispatch 失败推进到 blocked", map[string]any{
		"ticket_id":   ticket.ID,
		"worker_id":   job.WorkerID,
		"dispatch_id": job.ID,
		"request_id":  strings.TrimSpace(job.RequestID),
		"error":       strings.TrimSpace(errMsg),
	}, now); err != nil {
		return nil, err
	}
	ev := s.buildStatusChangeEvent(ticket.ID, lifecycleResult.Before.WorkflowStatus, lifecycleResult.After.WorkflowStatus, "pm.dispatch.fail", now)
	if ev != nil {
		ev.WorkerID = job.WorkerID
		ev.Detail = strings.TrimSpace(errMsg)
	}
	return ev, nil
}
