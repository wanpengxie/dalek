package app

import (
	"context"
	"fmt"

	"dalek/internal/contracts"
	channelsvc "dalek/internal/services/channel"
	pmsvc "dalek/internal/services/pm"
	workersvc "dalek/internal/services/worker"
)

type channelTicketActionAdapter struct {
	project *Project
}

func (a channelTicketActionAdapter) List(ctx context.Context, includeArchived bool) ([]contracts.Ticket, error) {
	if a.project == nil {
		return nil, fmt.Errorf("channel ticket adapter project 为空")
	}
	return a.project.ListTickets(ctx, includeArchived)
}

func (a channelTicketActionAdapter) GetByID(ctx context.Context, ticketID uint) (*contracts.Ticket, error) {
	if a.project == nil {
		return nil, fmt.Errorf("channel ticket adapter project 为空")
	}
	return a.project.ticket.GetByID(ctx, ticketID)
}

func (a channelTicketActionAdapter) CreateWithDescriptionAndLabel(ctx context.Context, title, description, label string) (*contracts.Ticket, error) {
	if a.project == nil {
		return nil, fmt.Errorf("channel ticket adapter project 为空")
	}
	return a.project.CreateTicketWithDescriptionAndLabel(ctx, title, description, label)
}

type channelPMActionAdapter struct {
	svc *pmsvc.Service
}

func (a channelPMActionAdapter) StartTicket(ctx context.Context, ticketID uint, baseBranch string) (*contracts.Worker, error) {
	return a.svc.StartTicketWithOptions(ctx, ticketID, pmsvc.StartOptions{BaseBranch: baseBranch})
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

func newChannelActionExecutor(project *Project, pmSvc *pmsvc.Service, workerSvc *workersvc.Service) *channelsvc.ActionExecutor {
	return channelsvc.NewActionExecutor(
		channelTicketActionAdapter{project: project},
		channelPMActionAdapter{svc: pmSvc},
		channelWorkerActionAdapter{svc: workerSvc},
	)
}
