package task

import (
	"context"
	"time"

	"dalek/internal/contracts"
	"dalek/internal/services/core"
	"dalek/internal/store"

	"gorm.io/gorm"
)

type runtimeFactory struct{}

type runtimeAdapter struct {
	svc *Service
}

func NewRuntimeFactory() core.TaskRuntimeFactory {
	return runtimeFactory{}
}

func (runtimeFactory) ForDB(db *gorm.DB) core.TaskRuntime {
	return runtimeAdapter{svc: New(db)}
}

func (r runtimeAdapter) FindRunByID(ctx context.Context, runID uint) (*contracts.TaskRun, error) {
	return r.svc.FindRunByID(ctx, runID)
}

func (r runtimeAdapter) FindRunByRequestID(ctx context.Context, requestID string) (*contracts.TaskRun, error) {
	return r.svc.FindRunByRequestID(ctx, requestID)
}

func (r runtimeAdapter) LatestActiveWorkerRun(ctx context.Context, workerID uint) (*contracts.TaskRun, error) {
	return r.svc.LatestActiveWorkerRun(ctx, workerID)
}

func (r runtimeAdapter) CreateRun(ctx context.Context, in contracts.TaskRunCreateInput) (contracts.TaskRun, error) {
	return r.svc.CreateRun(ctx, in)
}

func (r runtimeAdapter) CancelActiveWorkerRuns(ctx context.Context, workerID uint, reason string, now time.Time) error {
	return r.svc.CancelActiveWorkerRuns(ctx, workerID, reason, now)
}

func (r runtimeAdapter) MarkRunRunning(ctx context.Context, runID uint, runnerID string, leaseExpiresAt *time.Time, now time.Time, bumpAttempt bool) error {
	return r.svc.MarkRunRunning(ctx, runID, runnerID, leaseExpiresAt, now, bumpAttempt)
}

func (r runtimeAdapter) RenewLease(ctx context.Context, runID uint, runnerID string, leaseExpiresAt *time.Time) error {
	return r.svc.RenewLease(ctx, runID, runnerID, leaseExpiresAt)
}

func (r runtimeAdapter) MarkRunSucceeded(ctx context.Context, runID uint, resultPayloadJSON string, now time.Time) error {
	return r.svc.MarkRunSucceeded(ctx, runID, resultPayloadJSON, now)
}

func (r runtimeAdapter) MarkRunFailed(ctx context.Context, runID uint, errorCode string, errorMessage string, now time.Time) error {
	return r.svc.MarkRunFailed(ctx, runID, errorCode, errorMessage, now)
}

func (r runtimeAdapter) MarkRunCanceled(ctx context.Context, runID uint, errorCode string, errorMessage string, now time.Time) error {
	return r.svc.MarkRunCanceled(ctx, runID, errorCode, errorMessage, now)
}

func (r runtimeAdapter) AppendEvent(ctx context.Context, in contracts.TaskEventInput) error {
	return r.svc.AppendEvent(ctx, in)
}

func (r runtimeAdapter) AppendRuntimeSample(ctx context.Context, in contracts.TaskRuntimeSampleInput) error {
	return r.svc.AppendRuntimeSample(ctx, in)
}

func (r runtimeAdapter) AppendSemanticReport(ctx context.Context, in contracts.TaskSemanticReportInput) error {
	return r.svc.AppendSemanticReport(ctx, in)
}

func (r runtimeAdapter) ListStatus(ctx context.Context, opt contracts.TaskListStatusOptions) ([]store.TaskStatusView, error) {
	return r.svc.ListStatus(ctx, opt)
}

func (r runtimeAdapter) ListEventsAfterID(ctx context.Context, afterID uint, limit int) ([]contracts.TaskEventScopeRow, error) {
	return r.svc.ListEventsAfterID(ctx, afterID, limit)
}

var _ core.TaskRuntimeFactory = runtimeFactory{}
var _ core.TaskRuntime = runtimeAdapter{}
