package worker

import (
	"context"

	"dalek/internal/contracts"
)

type TicketLoopCancelResult struct {
	Found    bool
	Canceled bool
	Reason   string
}

type TicketLoopControl interface {
	CancelTicketLoop(ctx context.Context, ticketID uint, cause contracts.TaskCancelCause) (TicketLoopCancelResult, error)
}

func (s *Service) cancelTicketLoop(ctx context.Context, ticketID uint, cause contracts.TaskCancelCause) (TicketLoopCancelResult, bool, error) {
	ctrl := s.getTicketLoopControl()
	if ctrl == nil || ticketID == 0 || !cause.Valid() {
		return TicketLoopCancelResult{}, false, nil
	}
	res, err := ctrl.CancelTicketLoop(ctx, ticketID, cause)
	if err != nil {
		return TicketLoopCancelResult{}, true, err
	}
	return res, true, nil
}

func (s *Service) SetTicketLoopControl(ctrl TicketLoopControl) {
	if s == nil {
		return
	}
	s.controlMu.Lock()
	defer s.controlMu.Unlock()
	s.ticketLoopControl = ctrl
}

func (s *Service) getTicketLoopControl() TicketLoopControl {
	if s == nil {
		return nil
	}
	s.controlMu.RLock()
	defer s.controlMu.RUnlock()
	return s.ticketLoopControl
}
