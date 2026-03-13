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

func (p *Project) GetPMHealthMetrics(ctx context.Context, opt PMHealthMetricsOptions) (PMHealthMetrics, error) {
	if p == nil || p.pm == nil {
		return PMHealthMetrics{}, fmt.Errorf("project pm service 为空")
	}
	return p.pm.CalculateHealthMetrics(ctx, opt)
}

