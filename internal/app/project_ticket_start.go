package app

import (
	"context"
	"fmt"

	"dalek/internal/contracts"
	pmsvc "dalek/internal/services/pm"
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

func (p *Project) RunTicketWorker(ctx context.Context, ticketID uint, opt pmsvc.WorkerRunOptions) (pmsvc.WorkerRunResult, error) {
	if p == nil || p.pm == nil {
		return pmsvc.WorkerRunResult{}, fmt.Errorf("project pm service 为空")
	}
	return p.pm.RunTicketWorker(ctx, ticketID, opt)
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
