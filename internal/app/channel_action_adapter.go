package app

import (
	"context"
	"fmt"
	"strings"

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
	svc     *pmsvc.Service
	project *Project
}

func (a channelPMActionAdapter) StartTicket(ctx context.Context, ticketID uint, baseBranch string) (*contracts.Worker, error) {
	return a.svc.StartTicketWithOptions(ctx, ticketID, pmsvc.StartOptions{BaseBranch: baseBranch})
}

func (a channelPMActionAdapter) DispatchTicket(ctx context.Context, ticketID uint, entryPrompt string) (channelsvc.DispatchTicketResult, error) {
	if a.project != nil {
		cfg := a.project.core.Config.WithDefaults().MultiNode
		if cfg.AutoRoute && strings.TrimSpace(cfg.DevBaseURL) != "" {
			prompt := strings.TrimSpace(entryPrompt)
			if prompt == "" {
				prompt = fmt.Sprintf("continue development for ticket %d", ticketID)
			}
			res, err := a.project.SubmitTaskRequest(ctx, SubmitTaskRequestOptions{
				TicketID:  ticketID,
				Prompt:    prompt,
				ForceRole: TaskRequestRoleDev,
			})
			if err != nil {
				return channelsvc.DispatchTicketResult{}, err
			}
			return channelsvc.DispatchTicketResult{
				TicketID:  res.TicketID,
				WorkerID:  res.WorkerID,
				TaskRunID: res.TaskRunID,
			}, nil
		}
	}
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

func (a channelPMActionAdapter) ApproveMerge(ctx context.Context, mergeItemID uint, approvedBy string) error {
	return a.svc.ApproveMerge(ctx, mergeItemID, approvedBy)
}

func (a channelPMActionAdapter) DiscardMerge(ctx context.Context, mergeItemID uint, note string) error {
	return a.svc.DiscardMerge(ctx, mergeItemID, note)
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

type channelTaskRequestActionAdapter struct {
	project *Project
}

func (a channelTaskRequestActionAdapter) SubmitTaskRequest(ctx context.Context, in channelsvc.SubmitTaskRequestActionInput) (channelsvc.SubmitTaskRequestActionResult, error) {
	if a.project == nil {
		return channelsvc.SubmitTaskRequestActionResult{}, fmt.Errorf("project 为空")
	}
	var role TaskRequestRole
	switch strings.TrimSpace(in.ForceRole) {
	case "":
	case string(TaskRequestRoleDev):
		role = TaskRequestRoleDev
	case string(TaskRequestRoleRun):
		role = TaskRequestRoleRun
	default:
		return channelsvc.SubmitTaskRequestActionResult{}, fmt.Errorf("非法角色: %s", strings.TrimSpace(in.ForceRole))
	}
	res, err := a.project.SubmitTaskRequest(ctx, SubmitTaskRequestOptions{
		TicketID:      in.TicketID,
		RequestID:     strings.TrimSpace(in.RequestID),
		Prompt:        strings.TrimSpace(in.Prompt),
		VerifyTarget:  strings.TrimSpace(in.VerifyTarget),
		RemoteBaseURL: strings.TrimSpace(in.RemoteBaseURL),
		RemoteProject: strings.TrimSpace(in.RemoteProject),
		ForceRole:     role,
	})
	if err != nil {
		return channelsvc.SubmitTaskRequestActionResult{}, err
	}
	return channelsvc.SubmitTaskRequestActionResult{
		Accepted:      res.Accepted,
		Role:          res.Role,
		RoleSource:    res.RoleSource,
		RouteReason:   res.RouteReason,
		RouteMode:     res.RouteMode,
		RouteTarget:   res.RouteTarget,
		TaskRunID:     res.TaskRunID,
		RemoteRunID:   res.RemoteRunID,
		RequestID:     res.RequestID,
		TicketID:      res.TicketID,
		WorkerID:      res.WorkerID,
		VerifyTarget:  res.VerifyTarget,
		LinkedRunID:   res.LinkedRunID,
		LinkedSummary: res.LinkedSummary,
	}, nil
}

func newChannelActionExecutor(project *Project, ticketSvc *ticketsvc.Service, pmSvc *pmsvc.Service, workerSvc *workersvc.Service) *channelsvc.ActionExecutor {
	return channelsvc.NewActionExecutor(
		channelTicketActionAdapter{svc: ticketSvc},
		channelPMActionAdapter{svc: pmSvc, project: project},
		channelWorkerActionAdapter{svc: workerSvc},
		channelTaskRequestActionAdapter{project: project},
	)
}
