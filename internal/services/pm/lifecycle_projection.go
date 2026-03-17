package pm

import (
	"context"
	"fmt"
	"time"

	"dalek/internal/contracts"
	"dalek/internal/services/ticketlifecycle"

	"gorm.io/gorm"
)

type ticketLifecycleMutationResult struct {
	Event           *contracts.TicketLifecycleEvent
	Inserted        bool
	Before          ticketlifecycle.SnapshotProjection
	After           ticketlifecycle.SnapshotProjection
	SnapshotUpdated bool
}

func (r ticketLifecycleMutationResult) WorkflowChanged() bool {
	return r.Before.WorkflowStatus != r.After.WorkflowStatus
}

func (r ticketLifecycleMutationResult) IntegrationChanged() bool {
	return r.Before.IntegrationStatus != r.After.IntegrationStatus
}

func ticketSnapshotProjectionFromTicket(t contracts.Ticket) ticketlifecycle.SnapshotProjection {
	return ticketlifecycle.SnapshotProjection{
		WorkflowStatus:    contracts.CanonicalTicketWorkflowStatus(t.WorkflowStatus),
		IntegrationStatus: contracts.CanonicalIntegrationStatus(t.IntegrationStatus),
	}
}

func (s *Service) appendTicketLifecycleEventAndProjectSnapshotTx(
	ctx context.Context,
	tx *gorm.DB,
	input ticketlifecycle.AppendInput,
) (ticketLifecycleMutationResult, error) {
	if tx == nil {
		return ticketLifecycleMutationResult{}, fmt.Errorf("tx 不能为空")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	var ticket contracts.Ticket
	if err := tx.WithContext(ctx).
		Select("id", "workflow_status", "integration_status").
		First(&ticket, input.TicketID).Error; err != nil {
		return ticketLifecycleMutationResult{}, err
	}

	result := ticketLifecycleMutationResult{
		Before: ticketSnapshotProjectionFromTicket(ticket),
	}
	ev, inserted, err := ticketlifecycle.AppendEventTx(ctx, tx, input)
	if err != nil {
		return result, err
	}
	result.Event = ev
	result.Inserted = inserted
	result.After = result.Before
	if !inserted {
		return result, nil
	}

	events, err := ticketlifecycle.ListEventsByTicket(ctx, tx, input.TicketID)
	if err != nil {
		return result, err
	}
	result.After = ticketlifecycle.RebuildSnapshot(events)
	if !result.WorkflowChanged() && !result.IntegrationChanged() {
		return result, nil
	}

	updatedAt := input.CreatedAt
	if updatedAt.IsZero() {
		updatedAt = time.Now()
	}
	if result.WorkflowChanged() &&
		contracts.CanonicalTicketWorkflowStatus(result.Before.WorkflowStatus) == contracts.TicketBlocked &&
		contracts.CanonicalTicketWorkflowStatus(result.After.WorkflowStatus) != contracts.TicketBlocked {
		if err := s.closeNeedsUserInboxOnBlockedExitTx(ctx, tx, input.TicketID, updatedAt); err != nil {
			return result, err
		}
	}
	if err := tx.WithContext(ctx).Model(&contracts.Ticket{}).
		Where("id = ?", input.TicketID).
		Updates(map[string]any{
			"workflow_status":    result.After.WorkflowStatus,
			"integration_status": result.After.IntegrationStatus,
			"updated_at":         updatedAt,
		}).Error; err != nil {
		return result, err
	}
	result.SnapshotUpdated = true
	return result, nil
}
