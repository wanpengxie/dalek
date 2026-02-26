package app

import (
	"context"
	"fmt"

	"dalek/internal/contracts"
)

func (p *Project) ListTicketViews(ctx context.Context) ([]TicketView, error) {
	if p == nil || p.ticketQuery == nil {
		return nil, fmt.Errorf("project ticket query service 为空")
	}
	views, err := p.ticketQuery.ListTicketViews(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]TicketView, 0, len(views))
	for _, v := range views {
		out = append(out, TicketView{
			Ticket:             v.Ticket,
			LatestWorker:       v.LatestWorker,
			SessionAlive:       v.SessionAlive,
			SessionProbeFailed: v.SessionProbeFailed,
			DerivedStatus:      v.DerivedStatus,
			Capability:         v.Capability,
			TaskRunID:          v.TaskRunID,
			RuntimeHealthState: v.RuntimeHealthState,
			RuntimeNeedsUser:   v.RuntimeNeedsUser,
			RuntimeSummary:     v.RuntimeSummary,
			RuntimeObservedAt:  v.RuntimeObservedAt,
			SemanticPhase:      v.SemanticPhase,
			SemanticNextAction: v.SemanticNextAction,
			SemanticSummary:    v.SemanticSummary,
			SemanticReportedAt: v.SemanticReportedAt,
			LastEventType:      v.LastEventType,
			LastEventNote:      v.LastEventNote,
			LastEventAt:        v.LastEventAt,
		})
	}
	return out, nil
}

func (p *Project) ListTickets(ctx context.Context, includeArchived bool) ([]contracts.Ticket, error) {
	if p == nil || p.ticket == nil {
		return nil, fmt.Errorf("project ticket service 为空")
	}
	return p.ticket.List(ctx, includeArchived)
}

func (p *Project) CreateTicket(ctx context.Context, title string) (*contracts.Ticket, error) {
	return p.CreateTicketWithDescription(ctx, title, "")
}

func (p *Project) CreateTicketWithDescription(ctx context.Context, title, description string) (*contracts.Ticket, error) {
	if p == nil || p.ticket == nil {
		return nil, fmt.Errorf("project ticket service 为空")
	}
	return p.ticket.CreateWithDescription(ctx, title, description)
}

func (p *Project) ArchiveTicket(ctx context.Context, ticketID uint) error {
	if p == nil || p.pm == nil {
		return fmt.Errorf("project pm service 为空")
	}
	return p.pm.ArchiveTicket(ctx, ticketID)
}

func (p *Project) SetTicketWorkflowStatus(ctx context.Context, ticketID uint, status contracts.TicketWorkflowStatus) error {
	if p == nil || p.pm == nil {
		return fmt.Errorf("project pm service 为空")
	}
	return p.pm.SetTicketWorkflowStatus(ctx, ticketID, status)
}

func (p *Project) BumpTicketPriority(ctx context.Context, ticketID uint, delta int) (int, error) {
	if p == nil || p.ticket == nil {
		return 0, fmt.Errorf("project ticket service 为空")
	}
	return p.ticket.BumpPriority(ctx, ticketID, delta)
}

func (p *Project) UpdateTicketText(ctx context.Context, ticketID uint, title, description string) error {
	if p == nil || p.ticket == nil {
		return fmt.Errorf("project ticket service 为空")
	}
	return p.ticket.UpdateText(ctx, ticketID, title, description)
}

func (p *Project) ApplyWorkerReport(ctx context.Context, r contracts.WorkerReport, source string) error {
	if p == nil || p.pm == nil {
		return fmt.Errorf("project pm service 为空")
	}
	return p.pm.ApplyWorkerReport(ctx, r, source)
}
