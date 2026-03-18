package app

import (
	"context"
	"strings"

	"dalek/internal/contracts"
	pmsvc "dalek/internal/services/pm"
	workersvc "dalek/internal/services/worker"
)

type projectDaemonWorkerLoopControl struct {
	home        *Home
	projectName string
}

func (c projectDaemonWorkerLoopControl) CancelTicketLoop(ctx context.Context, ticketID uint, cause contracts.TaskCancelCause) (workersvc.TicketLoopCancelResult, error) {
	client, err := c.daemonClient()
	if err != nil {
		return workersvc.TicketLoopCancelResult{}, err
	}
	res, err := client.CancelTicketLoopWithCause(ctx, c.projectName, ticketID, cause)
	if err != nil {
		return workersvc.TicketLoopCancelResult{}, err
	}
	return workersvc.TicketLoopCancelResult{
		Found:    res.Found,
		Canceled: res.Canceled,
		Reason:   strings.TrimSpace(res.Reason),
	}, nil
}

func (c projectDaemonWorkerLoopControl) daemonClient() (*DaemonAPIClient, error) {
	return NewDaemonAPIClientFromHome(c.home)
}

type projectDaemonFocusLoopControl struct {
	home        *Home
	projectName string
}

func (c projectDaemonFocusLoopControl) CancelTaskRun(ctx context.Context, runID uint) error {
	client, err := NewDaemonAPIClientFromHome(c.home)
	if err != nil {
		return err
	}
	_, err = client.CancelTaskRun(ctx, runID)
	return err
}

func (c projectDaemonFocusLoopControl) CancelTicketLoop(ctx context.Context, ticketID uint) error {
	client, err := NewDaemonAPIClientFromHome(c.home)
	if err != nil {
		return err
	}
	_, err = client.CancelTicketLoopWithCause(ctx, c.projectName, ticketID, contracts.TaskCancelCauseFocusCancel)
	return err
}

var _ workersvc.TicketLoopControl = projectDaemonWorkerLoopControl{}
var _ pmsvc.FocusLoopControl = projectDaemonFocusLoopControl{}
