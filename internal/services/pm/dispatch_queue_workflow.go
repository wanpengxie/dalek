package pm

import (
	"context"
	"strings"
	"time"

	"dalek/internal/contracts"
	"dalek/internal/fsm"

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
