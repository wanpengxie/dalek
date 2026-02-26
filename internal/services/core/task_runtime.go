package core

import (
	"context"
	"dalek/internal/contracts"
	"time"

	"gorm.io/gorm"
)

type TaskRuntimeFactory interface {
	ForDB(db *gorm.DB) TaskRuntime
}

type TaskRuntime interface {
	FindRunByID(ctx context.Context, runID uint) (*contracts.TaskRun, error)
	FindRunByRequestID(ctx context.Context, requestID string) (*contracts.TaskRun, error)
	LatestActiveWorkerRun(ctx context.Context, workerID uint) (*contracts.TaskRun, error)
	CreateRun(ctx context.Context, in contracts.TaskRunCreateInput) (contracts.TaskRun, error)
	CancelActiveWorkerRuns(ctx context.Context, workerID uint, reason string, now time.Time) error
	MarkRunRunning(ctx context.Context, runID uint, runnerID string, leaseExpiresAt *time.Time, now time.Time, bumpAttempt bool) error
	RenewLease(ctx context.Context, runID uint, runnerID string, leaseExpiresAt *time.Time) error
	MarkRunSucceeded(ctx context.Context, runID uint, resultPayloadJSON string, now time.Time) error
	MarkRunFailed(ctx context.Context, runID uint, errorCode string, errorMessage string, now time.Time) error
	MarkRunCanceled(ctx context.Context, runID uint, errorCode string, errorMessage string, now time.Time) error
	AppendEvent(ctx context.Context, in contracts.TaskEventInput) error
	AppendRuntimeSample(ctx context.Context, in contracts.TaskRuntimeSampleInput) error
	AppendSemanticReport(ctx context.Context, in contracts.TaskSemanticReportInput) error
	ListStatus(ctx context.Context, opt contracts.TaskListStatusOptions) ([]contracts.TaskStatusView, error)
	ListEventsAfterID(ctx context.Context, afterID uint, limit int) ([]contracts.TaskEventScopeRow, error)
}
