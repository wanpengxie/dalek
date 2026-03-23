package app

import (
	"context"
	"fmt"

	"dalek/internal/contracts"
)

func (p *Project) GetPMState(ctx context.Context) (contracts.PMState, error) {
	if p == nil || p.pm == nil {
		return contracts.PMState{}, fmt.Errorf("project pm service 为空")
	}
	return p.pm.GetState(ctx)
}

func (p *Project) SetMaxRunningWorkers(ctx context.Context, n int) (contracts.PMState, error) {
	if p == nil || p.pm == nil {
		return contracts.PMState{}, fmt.Errorf("project pm service 为空")
	}
	return p.pm.SetMaxRunningWorkers(ctx, n)
}

func (p *Project) ManagerTick(ctx context.Context, opt ManagerTickOptions) (ManagerTickResult, error) {
	if p == nil || p.pm == nil {
		return ManagerTickResult{}, fmt.Errorf("project pm service 为空")
	}
	return p.pm.ManagerTick(ctx, opt)
}

func (p *Project) FocusStart(ctx context.Context, in FocusStartInput) (FocusStartResult, error) {
	if p == nil || p.pm == nil {
		return FocusStartResult{}, fmt.Errorf("project pm service 为空")
	}
	return p.pm.FocusStart(ctx, in)
}

func (p *Project) FocusGet(ctx context.Context, focusID uint) (FocusRunView, error) {
	if p == nil || p.pm == nil {
		return FocusRunView{}, fmt.Errorf("project pm service 为空")
	}
	return p.pm.FocusGet(ctx, focusID)
}

func (p *Project) FocusPoll(ctx context.Context, focusID, sinceEventID uint) (FocusPollResult, error) {
	if p == nil || p.pm == nil {
		return FocusPollResult{}, fmt.Errorf("project pm service 为空")
	}
	return p.pm.FocusPoll(ctx, focusID, sinceEventID)
}

func (p *Project) FocusStop(ctx context.Context, focusID uint, requestID string) error {
	if p == nil || p.pm == nil {
		return fmt.Errorf("project pm service 为空")
	}
	return p.pm.FocusStop(ctx, focusID, requestID)
}

func (p *Project) FocusCancel(ctx context.Context, focusID uint, requestID string) error {
	if p == nil || p.pm == nil {
		return fmt.Errorf("project pm service 为空")
	}
	return p.pm.FocusCancel(ctx, focusID, requestID)
}

func (p *Project) FocusAddTickets(ctx context.Context, in FocusAddTicketsInput) (FocusAddTicketsResult, error) {
	if p == nil || p.pm == nil {
		return FocusAddTicketsResult{}, fmt.Errorf("project pm service 为空")
	}
	return p.pm.FocusAddTickets(ctx, in)
}

func (p *Project) CreateIntegrationTicket(ctx context.Context, in CreateIntegrationTicketInput) (CreateIntegrationTicketResult, error) {
	if p == nil || p.pm == nil {
		return CreateIntegrationTicketResult{}, fmt.Errorf("project pm service 为空")
	}
	return p.pm.CreateIntegrationTicket(ctx, in)
}

func (p *Project) FinalizeTicketSuperseded(ctx context.Context, sourceTicketID, replacementTicketID uint, reason string) error {
	if p == nil || p.pm == nil {
		return fmt.Errorf("project pm service 为空")
	}
	return p.pm.FinalizeTicketSuperseded(ctx, sourceTicketID, replacementTicketID, reason)
}

func (p *Project) GetPMHealthMetrics(ctx context.Context, opt PMHealthMetricsOptions) (PMHealthMetrics, error) {
	if p == nil || p.pm == nil {
		return PMHealthMetrics{}, fmt.Errorf("project pm service 为空")
	}
	return p.pm.CalculateHealthMetrics(ctx, opt)
}
