package app

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"dalek/internal/contracts"
	workersvc "dalek/internal/services/worker"
)

func (p *Project) InterruptTicket(ctx context.Context, ticketID uint) (InterruptResult, error) {
	if p == nil || p.worker == nil {
		return InterruptResult{}, fmt.Errorf("project worker service 为空")
	}
	r, err := p.worker.InterruptTicket(ctx, ticketID)
	if err != nil {
		return InterruptResult{}, err
	}
	return InterruptResult{
		TicketID:    r.TicketID,
		WorkerID:    r.WorkerID,
		TmuxSocket:  r.TmuxSocket,
		TmuxSession: r.TmuxSession,
		TargetPane:  r.TargetPane,
	}, nil
}

func (p *Project) InterruptWorker(ctx context.Context, workerID uint) (InterruptResult, error) {
	if p == nil || p.worker == nil {
		return InterruptResult{}, fmt.Errorf("project worker service 为空")
	}
	r, err := p.worker.InterruptWorker(ctx, workerID)
	if err != nil {
		return InterruptResult{}, err
	}
	return InterruptResult{
		TicketID:    r.TicketID,
		WorkerID:    r.WorkerID,
		TmuxSocket:  r.TmuxSocket,
		TmuxSession: r.TmuxSession,
		TargetPane:  r.TargetPane,
	}, nil
}

func (p *Project) StopWorker(ctx context.Context, workerID uint) error {
	if p == nil || p.worker == nil {
		return fmt.Errorf("project worker service 为空")
	}
	return p.worker.StopWorker(ctx, workerID)
}

func (p *Project) StopTicket(ctx context.Context, ticketID uint) error {
	if p == nil || p.worker == nil {
		return fmt.Errorf("project worker service 为空")
	}
	stopErr := p.worker.StopTicket(ctx, ticketID)

	var dispatchErr error
	if p.pm != nil {
		_, dispatchErr = p.pm.ForceFailActiveDispatchesForTicket(ctx, ticketID, "ticket stop: force fail active dispatch")
	} else if stopErr == nil {
		dispatchErr = fmt.Errorf("project pm service 为空")
	}

	if stopErr != nil && dispatchErr != nil {
		return fmt.Errorf("%w；另外 dispatch 终结失败: %v", stopErr, dispatchErr)
	}
	if stopErr != nil {
		return stopErr
	}
	return dispatchErr
}

func (p *Project) CleanupTicketWorktree(ctx context.Context, ticketID uint, opt WorktreeCleanupOptions) (WorktreeCleanupResult, error) {
	if p == nil || p.worker == nil {
		return WorktreeCleanupResult{}, fmt.Errorf("project worker service 为空")
	}
	r, err := p.worker.CleanupTicketWorktree(ctx, ticketID, workersvc.CleanupWorktreeOptions{
		Force:  opt.Force,
		DryRun: opt.DryRun,
	})
	if err != nil {
		return WorktreeCleanupResult{}, err
	}
	return WorktreeCleanupResult{
		TicketID:    r.TicketID,
		WorkerID:    r.WorkerID,
		Worktree:    strings.TrimSpace(r.Worktree),
		Branch:      strings.TrimSpace(r.Branch),
		RequestedAt: r.RequestedAt,
		CleanedAt:   r.CleanedAt,
		DryRun:      r.DryRun,
		Pending:     r.Pending,
		Cleaned:     r.Cleaned,
		Dirty:       r.Dirty,
		SessionLive: r.SessionLive,
		Message:     strings.TrimSpace(r.Message),
	}, nil
}

func (p *Project) CountPendingWorktreeCleanup(ctx context.Context) (int64, error) {
	if p == nil || p.worker == nil {
		return 0, fmt.Errorf("project worker service 为空")
	}
	return p.worker.CountPendingWorktreeCleanup(ctx)
}

func (p *Project) KillAllTmuxSessions(ctx context.Context) error {
	if p == nil || p.worker == nil {
		return fmt.Errorf("project worker service 为空")
	}
	return p.worker.KillAllTmuxSessions(ctx)
}

func (p *Project) ReconcileRunningWorkersAfterKillAll(ctx context.Context, socket string) (int64, error) {
	if p == nil || p.worker == nil {
		return 0, fmt.Errorf("project worker service 为空")
	}
	return p.worker.ReconcileRunningWorkersAfterKillAll(ctx, socket)
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
