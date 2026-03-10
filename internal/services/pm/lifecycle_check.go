package pm

import (
	"context"
	"fmt"

	"dalek/internal/contracts"
	"dalek/internal/services/ticketlifecycle"
)

func (s *Service) ListTicketLifecycleEvents(ctx context.Context, ticketID uint) ([]contracts.TicketLifecycleEvent, error) {
	_, db, err := s.require()
	if err != nil {
		return nil, err
	}
	return ticketlifecycle.ListEventsByTicket(ctx, db, ticketID)
}

func (s *Service) RebuildTicketLifecycleSnapshot(ctx context.Context, ticketID uint) (ticketlifecycle.SnapshotProjection, error) {
	events, err := s.ListTicketLifecycleEvents(ctx, ticketID)
	if err != nil {
		return ticketlifecycle.SnapshotProjection{}, err
	}
	return ticketlifecycle.RebuildSnapshot(events), nil
}

func (s *Service) CheckTicketLifecycleConsistency(ctx context.Context, ticketID uint) (ticketlifecycle.ConsistencyCheck, error) {
	_, db, err := s.require()
	if err != nil {
		return ticketlifecycle.ConsistencyCheck{}, err
	}
	if ticketID == 0 {
		return ticketlifecycle.ConsistencyCheck{}, fmt.Errorf("ticket_id 不能为空")
	}
	return ticketlifecycle.CheckTicketConsistency(ctx, db, ticketID)
}
