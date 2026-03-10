package app

import (
	"context"
	"fmt"
	"os/exec"

	"dalek/internal/contracts"
)

func (p *Project) InterruptTicket(ctx context.Context, ticketID uint) (InterruptResult, error) {
	if p == nil || p.worker == nil {
		return InterruptResult{}, fmt.Errorf("project worker service 为空")
	}
	return p.worker.InterruptTicket(ctx, ticketID)
}

func (p *Project) InterruptWorker(ctx context.Context, workerID uint) (InterruptResult, error) {
	if p == nil || p.worker == nil {
		return InterruptResult{}, fmt.Errorf("project worker service 为空")
	}
	return p.worker.InterruptWorker(ctx, workerID)
}

func (p *Project) StopWorker(ctx context.Context, workerID uint) error {
	if p == nil || p.worker == nil {
		return fmt.Errorf("project worker service 为空")
	}
	return p.worker.StopWorker(ctx, workerID)
}

func (p *Project) StopTicket(ctx context.Context, ticketID uint) error {
	if p == nil || p.pm == nil {
		return fmt.Errorf("project pm service 为空")
	}
	return p.pm.StopTicket(ctx, ticketID)
}

func (p *Project) CleanupTicketWorktree(ctx context.Context, ticketID uint, opt WorktreeCleanupOptions) (WorktreeCleanupResult, error) {
	if p == nil || p.worker == nil {
		return WorktreeCleanupResult{}, fmt.Errorf("project worker service 为空")
	}
	return p.worker.CleanupTicketWorktree(ctx, ticketID, opt)
}

func (p *Project) CountPendingWorktreeCleanup(ctx context.Context) (int64, error) {
	if p == nil || p.worker == nil {
		return 0, fmt.Errorf("project worker service 为空")
	}
	return p.worker.CountPendingWorktreeCleanup(ctx)
}

func (p *Project) AttachCmd(ctx context.Context, ticketID uint) (*exec.Cmd, error) {
	if p == nil || p.worker == nil {
		return nil, fmt.Errorf("project worker service 为空")
	}
	return p.worker.AttachCmd(ctx, ticketID)
}

func (p *Project) LatestWorker(ctx context.Context, ticketID uint) (*contracts.Worker, error) {
	if p == nil || p.worker == nil {
		return nil, fmt.Errorf("project worker service 为空")
	}
	return p.worker.LatestWorker(ctx, ticketID)
}

func (p *Project) WorkerByID(ctx context.Context, workerID uint) (*contracts.Worker, error) {
	if p == nil || p.worker == nil {
		return nil, fmt.Errorf("project worker service 为空")
	}
	return p.worker.WorkerByID(ctx, workerID)
}

func (p *Project) CaptureTicketTail(ctx context.Context, ticketID uint, lastLines int) (contracts.TailPreview, error) {
	if p == nil || p.preview == nil {
		return contracts.TailPreview{}, fmt.Errorf("project preview service 为空")
	}
	return p.preview.CaptureTicketTail(ctx, ticketID, lastLines)
}

func (p *Project) ListRunningWorkers(ctx context.Context) ([]contracts.Worker, error) {
	if p == nil || p.worker == nil {
		return nil, fmt.Errorf("project worker service 为空")
	}
	return p.worker.ListRunningWorkers(ctx)
}

func (p *Project) ListStoppableWorkers(ctx context.Context) ([]contracts.Worker, error) {
	if p == nil || p.worker == nil {
		return nil, fmt.Errorf("project worker service 为空")
	}
	return p.worker.ListStoppableWorkers(ctx)
}
