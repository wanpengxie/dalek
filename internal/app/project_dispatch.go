package app

import (
	"context"
	"fmt"

	"dalek/internal/contracts"
)

func (p *Project) StartTicket(ctx context.Context, ticketID uint) (*contracts.Worker, error) {
	return p.StartTicketWithOptions(ctx, ticketID, StartOptions{})
}

func (p *Project) StartTicketWithOptions(ctx context.Context, ticketID uint, opt StartOptions) (*contracts.Worker, error) {
	if p == nil || p.pm == nil {
		return nil, fmt.Errorf("project pm service 为空")
	}
	return p.pm.StartTicketWithOptions(ctx, ticketID, opt)
}

func (p *Project) DispatchTicket(ctx context.Context, ticketID uint) (DispatchResult, error) {
	return p.DispatchTicketWithOptions(ctx, ticketID, DispatchOptions{})
}

func (p *Project) SubmitDispatchTicket(ctx context.Context, ticketID uint, opt DispatchSubmitOptions) (DispatchSubmission, error) {
	if p == nil || p.pm == nil {
		return DispatchSubmission{}, fmt.Errorf("project pm service 为空")
	}
	return p.pm.SubmitDispatchTicket(ctx, ticketID, opt)
}

func (p *Project) RunDispatchJob(ctx context.Context, jobID uint, opt DispatchRunOptions) error {
	if p == nil || p.pm == nil {
		return fmt.Errorf("project pm service 为空")
	}
	return p.pm.RunDispatchJob(ctx, jobID, opt)
}

func (p *Project) DispatchTicketWithOptions(ctx context.Context, ticketID uint, opt DispatchOptions) (DispatchResult, error) {
	if p == nil || p.pm == nil {
		return DispatchResult{}, fmt.Errorf("project pm service 为空")
	}
	return p.pm.DispatchTicketWithOptions(ctx, ticketID, opt)
}

func (p *Project) DirectDispatchWorker(ctx context.Context, ticketID uint, opt DirectDispatchOptions) (DirectDispatchResult, error) {
	if p == nil || p.pm == nil {
		return DirectDispatchResult{}, fmt.Errorf("project pm service 为空")
	}
	return p.pm.DirectDispatchWorker(ctx, ticketID, opt)
}

func (p *Project) FindLatestWorkerRun(ctx context.Context, ticketID uint, afterRunID uint) (*TaskStatus, error) {
	if p == nil || p.task == nil {
		return nil, fmt.Errorf("project task service 为空")
	}
	if ticketID == 0 {
		return nil, fmt.Errorf("ticket_id 不能为空")
	}
	statuses, err := p.ListTaskStatus(ctx, ListTaskOptions{
		OwnerType:       contracts.TaskOwnerWorker,
		TicketID:        ticketID,
		IncludeTerminal: true,
		Limit:           20,
	})
	if err != nil {
		return nil, err
	}
	for _, st := range statuses {
		if st.RunID > afterRunID {
			v := st
			return &v, nil
		}
	}
	return nil, nil
}
