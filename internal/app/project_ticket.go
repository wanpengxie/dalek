package app

import (
	"context"
	"fmt"
	"strings"

	"dalek/internal/contracts"
	"dalek/internal/ticketatomic"
)

func (p *Project) ListTicketViews(ctx context.Context) ([]TicketView, error) {
	if p == nil || p.ticketQuery == nil {
		return nil, fmt.Errorf("project ticket query service 为空")
	}
	return p.ticketQuery.ListTicketViews(ctx)
}

func (p *Project) GetTicketViewByID(ctx context.Context, ticketID uint) (*TicketView, error) {
	if p == nil || p.ticketQuery == nil {
		return nil, fmt.Errorf("project ticket query service 为空")
	}
	return p.ticketQuery.GetTicketViewByID(ctx, ticketID)
}

func (p *Project) ListTickets(ctx context.Context, includeArchived bool) ([]contracts.Ticket, error) {
	if p == nil || p.ticket == nil {
		return nil, fmt.Errorf("project ticket service 为空")
	}
	return p.ticket.List(ctx, includeArchived)
}

func (p *Project) CreateTicket(ctx context.Context, title string) (*contracts.Ticket, error) {
	return p.CreateTicketWithDescriptionAndLabel(ctx, title, "", "")
}

func (p *Project) CreateTicketWithDescription(ctx context.Context, title, description string) (*contracts.Ticket, error) {
	return p.CreateTicketWithDescriptionAndLabel(ctx, title, description, "")
}

func (p *Project) CreateTicketWithDescriptionAndLabel(ctx context.Context, title, description, label string) (*contracts.Ticket, error) {
	return p.CreateTicketWithDescriptionAndLabelAndPriorityAndTarget(ctx, title, description, label, contracts.TicketPriorityNone, "")
}

func (p *Project) CreateTicketWithDescriptionAndLabelAndPriority(ctx context.Context, title, description, label string, priority int) (*contracts.Ticket, error) {
	return p.CreateTicketWithDescriptionAndLabelAndPriorityAndTarget(ctx, title, description, label, priority, "")
}

func (p *Project) CreateTicketWithDescriptionAndLabelAndPriorityAndTarget(ctx context.Context, title, description, label string, priority int, targetRef string) (*contracts.Ticket, error) {
	if p == nil || p.ticket == nil {
		return nil, fmt.Errorf("project ticket service 为空")
	}
	currentBranch := ""
	var currentErr error
	if strings.TrimSpace(targetRef) == "" && p != nil && p.core != nil && p.core.Git != nil {
		currentBranch, currentErr = p.core.Git.CurrentBranch(p.core.RepoRoot)
	}
	normalized, err := ticketatomic.ResolveCreateTargetRef(targetRef, currentBranch, currentErr)
	if err != nil {
		return nil, err
	}
	return p.ticket.CreateWithDescriptionAndLabelAndPriorityAndTarget(ctx, title, description, label, priority, normalized)
}

func (p *Project) ArchiveTicket(ctx context.Context, ticketID uint) error {
	if p == nil || p.pm == nil {
		return fmt.Errorf("project pm service 为空")
	}
	return p.pm.ArchiveTicket(ctx, ticketID)
}

func (p *Project) SetTicketWorkflowStatus(ctx context.Context, ticketID uint, status contracts.TicketWorkflowStatus) error {
	if p == nil || p.pm == nil {
		return fmt.Errorf("project pm service 为空")
	}
	return p.pm.SetTicketWorkflowStatus(ctx, ticketID, status)
}

func (p *Project) AbandonTicketIntegration(ctx context.Context, ticketID uint, reason string) error {
	if p == nil || p.pm == nil {
		return fmt.Errorf("project pm service 为空")
	}
	return p.pm.AbandonTicketIntegration(ctx, ticketID, reason)
}

func (p *Project) SyncMergeRef(ctx context.Context, ref, oldSHA, newSHA string) (MergeSyncRefResult, error) {
	if p == nil || p.pm == nil {
		return MergeSyncRefResult{}, fmt.Errorf("project pm service 为空")
	}
	return p.pm.SyncRef(ctx, ref, oldSHA, newSHA)
}

func (p *Project) RetargetTicketIntegration(ctx context.Context, ticketID uint, targetRef string) (MergeRetargetResult, error) {
	if p == nil || p.pm == nil {
		return MergeRetargetResult{}, fmt.Errorf("project pm service 为空")
	}
	return p.pm.RetargetTicketIntegration(ctx, ticketID, targetRef)
}

func (p *Project) RescanTicketMergeStatus(ctx context.Context, targetRef string) (MergeRescanResult, error) {
	if p == nil || p.pm == nil {
		return MergeRescanResult{}, fmt.Errorf("project pm service 为空")
	}
	return p.pm.RescanMergeStatus(ctx, targetRef)
}

func (p *Project) RebuildTicketLifecycleSnapshot(ctx context.Context, ticketID uint) (TicketLifecycleSnapshot, error) {
	if p == nil || p.pm == nil {
		return TicketLifecycleSnapshot{}, fmt.Errorf("project pm service 为空")
	}
	return p.pm.RebuildTicketLifecycleSnapshot(ctx, ticketID)
}

func (p *Project) CheckTicketLifecycleConsistency(ctx context.Context, ticketID uint) (TicketLifecycleConsistency, error) {
	if p == nil || p.pm == nil {
		return TicketLifecycleConsistency{}, fmt.Errorf("project pm service 为空")
	}
	return p.pm.CheckTicketLifecycleConsistency(ctx, ticketID)
}

func (p *Project) BumpTicketPriority(ctx context.Context, ticketID uint, delta int) (int, error) {
	if p == nil || p.ticket == nil {
		return 0, fmt.Errorf("project ticket service 为空")
	}
	return p.ticket.BumpPriority(ctx, ticketID, delta)
}

func (p *Project) SetTicketPriority(ctx context.Context, ticketID uint, priority int) error {
	if p == nil || p.ticket == nil {
		return fmt.Errorf("project ticket service 为空")
	}
	return p.ticket.SetPriority(ctx, ticketID, priority)
}

func (p *Project) UpdateTicketText(ctx context.Context, ticketID uint, title, description string) error {
	if p == nil || p.ticket == nil {
		return fmt.Errorf("project ticket service 为空")
	}
	return p.ticket.UpdateText(ctx, ticketID, title, description)
}

func (p *Project) UpdateTicketTextAndLabel(ctx context.Context, ticketID uint, title, description, label string) error {
	if p == nil || p.ticket == nil {
		return fmt.Errorf("project ticket service 为空")
	}
	return p.ticket.UpdateTextAndLabel(ctx, ticketID, title, description, label)
}

func (p *Project) UpdateTicketTextAndPriority(ctx context.Context, ticketID uint, title, description string, priority int) error {
	if p == nil || p.ticket == nil {
		return fmt.Errorf("project ticket service 为空")
	}
	return p.ticket.UpdateTextAndPriority(ctx, ticketID, title, description, priority)
}

func (p *Project) UpdateTicketTextAndLabelAndPriority(ctx context.Context, ticketID uint, title, description, label string, priority int) error {
	if p == nil || p.ticket == nil {
		return fmt.Errorf("project ticket service 为空")
	}
	return p.ticket.UpdateTextAndLabelAndPriority(ctx, ticketID, title, description, label, priority)
}

func (p *Project) ApplyWorkerReport(ctx context.Context, r contracts.WorkerReport, source string) error {
	if p == nil || p.pm == nil {
		return fmt.Errorf("project pm service 为空")
	}
	if err := p.pm.ApplyWorkerReport(ctx, r, source); err != nil {
		return err
	}
	return p.pm.ApplyWorkerReportTerminalClosure(ctx, r, source)
}

func (p *Project) ApplyWorkerLoopReport(ctx context.Context, r contracts.WorkerReport, source string) error {
	if p == nil || p.pm == nil {
		return fmt.Errorf("project pm service 为空")
	}
	if err := p.pm.ApplyWorkerReport(ctx, r, source); err != nil {
		return err
	}
	return p.pm.ApplyWorkerLoopTerminalClosure(ctx, r, source)
}

func (p *Project) WaitStatusChangeHooks(ctx context.Context) error {
	if p == nil || p.pm == nil {
		return nil
	}
	return p.pm.WaitStatusChangeHooks(ctx)
}
