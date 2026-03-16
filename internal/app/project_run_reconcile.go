package app

import (
	"context"
	"fmt"

	runsvc "dalek/internal/services/run"
)

type RunReconcileResult = runsvc.ReconcileResult

func (p *Project) ReconcileRun(ctx context.Context, fetcher runsvc.RunStatusFetcher, runID uint) (RunReconcileResult, error) {
	if p == nil || p.run == nil || p.task == nil {
		return RunReconcileResult{}, fmt.Errorf("project run reconcile 依赖未初始化")
	}
	db, err := p.OpenDBForTest()
	if err != nil {
		return RunReconcileResult{}, err
	}
	reconciler := runsvc.NewReconciler(db, p.task, fetcher)
	return reconciler.ReconcileByRunID(ctx, runID)
}

func (p *Project) ReconcileRunByRequestID(ctx context.Context, fetcher runsvc.RunStatusFetcher, requestID string) (RunReconcileResult, error) {
	if p == nil || p.run == nil || p.task == nil {
		return RunReconcileResult{}, fmt.Errorf("project run reconcile 依赖未初始化")
	}
	db, err := p.OpenDBForTest()
	if err != nil {
		return RunReconcileResult{}, err
	}
	reconciler := runsvc.NewReconciler(db, p.task, fetcher)
	return reconciler.ReconcileByRequestID(ctx, requestID)
}
