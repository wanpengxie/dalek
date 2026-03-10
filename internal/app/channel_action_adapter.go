package app

import (
	"context"

	"dalek/internal/contracts"
	channelsvc "dalek/internal/services/channel"
	pmsvc "dalek/internal/services/pm"
	ticketsvc "dalek/internal/services/ticket"
	workersvc "dalek/internal/services/worker"
)

type channelTicketActionAdapter struct {
	svc *ticketsvc.Service
}

func (a channelTicketActionAdapter) List(ctx context.Context, includeArchived bool) ([]contracts.Ticket, error) {
	return a.svc.List(ctx, includeArchived)
}

func (a channelTicketActionAdapter) GetByID(ctx context.Context, ticketID uint) (*contracts.Ticket, error) {
	return a.svc.GetByID(ctx, ticketID)
}

func (a channelTicketActionAdapter) CreateWithDescriptionAndLabel(ctx context.Context, title, description, label string) (*contracts.Ticket, error) {
	return a.svc.CreateWithDescriptionAndLabel(ctx, title, description, label)
}

type channelPMActionAdapter struct {
	svc *pmsvc.Service
}

func (a channelPMActionAdapter) StartTicket(ctx context.Context, ticketID uint, baseBranch string) (*contracts.Worker, error) {
	return a.svc.StartTicketWithOptions(ctx, ticketID, pmsvc.StartOptions{BaseBranch: baseBranch})
}

func (a channelPMActionAdapter) DispatchTicket(ctx context.Context, ticketID uint, entryPrompt string) (channelsvc.DispatchTicketResult, error) {
	res, err := a.svc.DispatchTicketWithOptions(ctx, ticketID, pmsvc.DispatchOptions{EntryPrompt: entryPrompt})
	if err != nil {
		return channelsvc.DispatchTicketResult{}, err
	}
	return channelsvc.DispatchTicketResult{
		TicketID:  res.TicketID,
		WorkerID:  res.WorkerID,
		TaskRunID: res.TaskRunID,
	}, nil
}

func (a channelPMActionAdapter) ArchiveTicket(ctx context.Context, ticketID uint) error {
	return a.svc.ArchiveTicket(ctx, ticketID)
}

func (a channelPMActionAdapter) ListMergeItems(ctx context.Context, status contracts.MergeStatus, limit int) ([]contracts.MergeItem, error) {
	return a.svc.ListMergeItems(ctx, pmsvc.ListMergeOptions{
		Status: status,
		Limit:  limit,
	})
}

type channelWorkerActionAdapter struct {
	svc *workersvc.Service
}

func (a channelWorkerActionAdapter) InterruptTicket(ctx context.Context, ticketID uint) (channelsvc.InterruptTicketResult, error) {
	res, err := a.svc.InterruptTicket(ctx, ticketID)
	if err != nil {
		return channelsvc.InterruptTicketResult{}, err
	}
	return channelsvc.InterruptTicketResult{
		TicketID:  res.TicketID,
		WorkerID:  res.WorkerID,
		Mode:      res.Mode,
		TaskRunID: res.TaskRunID,
		LogPath:   res.LogPath,
	}, nil
}

func (a channelWorkerActionAdapter) StopTicket(ctx context.Context, ticketID uint) error {
	return a.svc.StopTicket(ctx, ticketID)
}

func newChannelActionExecutor(ticketSvc *ticketsvc.Service, pmSvc *pmsvc.Service, workerSvc *workersvc.Service) *channelsvc.ActionExecutor {
	return channelsvc.NewActionExecutor(
		channelTicketActionAdapter{svc: ticketSvc},
		channelPMActionAdapter{svc: pmSvc},
		channelWorkerActionAdapter{svc: workerSvc},
	)
}
