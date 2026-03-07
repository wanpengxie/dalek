package app

import (
	"context"
	"fmt"
	"strings"

	"dalek/internal/contracts"
)

func (p *Project) GetPMState(ctx context.Context) (contracts.PMState, error) {
	if p == nil || p.pm == nil {
		return contracts.PMState{}, fmt.Errorf("project pm service 为空")
	}
	return p.pm.GetState(ctx)
}

func (p *Project) SetAutopilotEnabled(ctx context.Context, enabled bool) (contracts.PMState, error) {
	if p == nil || p.pm == nil {
		return contracts.PMState{}, fmt.Errorf("project pm service 为空")
	}
	return p.pm.SetAutopilotEnabled(ctx, enabled)
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

func (p *Project) RunPlannerJob(ctx context.Context, taskRunID uint, opt PlannerRunOptions) error {
	if p == nil || p.pm == nil {
		return fmt.Errorf("project pm service 为空")
	}
	opt.RunnerID = strings.TrimSpace(opt.RunnerID)
	return p.pm.RunPlannerJob(ctx, taskRunID, opt)
}
