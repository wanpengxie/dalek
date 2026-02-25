package task

import (
	"context"
	"time"

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

func (r runtimeAdapter) FindRunByID(ctx context.Context, runID uint) (*store.TaskRun, error) {
	return r.svc.FindRunByID(ctx, runID)
}

func (r runtimeAdapter) FindRunByRequestID(ctx context.Context, requestID string) (*store.TaskRun, error) {
	return r.svc.FindRunByRequestID(ctx, requestID)
}

func (r runtimeAdapter) LatestActiveWorkerRun(ctx context.Context, workerID uint) (*store.TaskRun, error) {
	return r.svc.LatestActiveWorkerRun(ctx, workerID)
}

func (r runtimeAdapter) CreateRun(ctx context.Context, in core.TaskRuntimeCreateRunInput) (store.TaskRun, error) {
	return r.svc.CreateRun(ctx, CreateRunInput{
		OwnerType:          in.OwnerType,
		TaskType:           in.TaskType,
		ProjectKey:         in.ProjectKey,
		TicketID:           in.TicketID,
		WorkerID:           in.WorkerID,
		SubjectType:        in.SubjectType,
		SubjectID:          in.SubjectID,
		RequestID:          in.RequestID,
		OrchestrationState: in.OrchestrationState,
		RunnerID:           in.RunnerID,
		LeaseExpiresAt:     in.LeaseExpiresAt,
		Attempt:            in.Attempt,
		RequestPayloadJSON: in.RequestPayloadJSON,
		ResultPayloadJSON:  in.ResultPayloadJSON,
		ErrorCode:          in.ErrorCode,
		ErrorMessage:       in.ErrorMessage,
		StartedAt:          in.StartedAt,
		FinishedAt:         in.FinishedAt,
	})
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

func (r runtimeAdapter) AppendEvent(ctx context.Context, in core.TaskRuntimeEventInput) error {
	return r.svc.AppendEvent(ctx, TaskEventInput{
		TaskRunID: in.TaskRunID,
		EventType: in.EventType,
		FromState: in.FromState,
		ToState:   in.ToState,
		Note:      in.Note,
		Payload:   in.Payload,
		CreatedAt: in.CreatedAt,
	})
}

func (r runtimeAdapter) AppendRuntimeSample(ctx context.Context, in core.TaskRuntimeRuntimeSampleInput) error {
	return r.svc.AppendRuntimeSample(ctx, RuntimeSampleInput{
		TaskRunID:  in.TaskRunID,
		State:      in.State,
		NeedsUser:  in.NeedsUser,
		Summary:    in.Summary,
		Source:     in.Source,
		ObservedAt: in.ObservedAt,
		Metrics:    in.Metrics,
	})
}

func (r runtimeAdapter) AppendSemanticReport(ctx context.Context, in core.TaskRuntimeSemanticReportInput) error {
	return r.svc.AppendSemanticReport(ctx, SemanticReportInput{
		TaskRunID:  in.TaskRunID,
		Phase:      in.Phase,
		Milestone:  in.Milestone,
		NextAction: in.NextAction,
		Summary:    in.Summary,
		ReportedAt: in.ReportedAt,
		Payload:    in.Payload,
	})
}

func (r runtimeAdapter) ListStatus(ctx context.Context, opt core.TaskRuntimeListStatusOptions) ([]store.TaskStatusView, error) {
	return r.svc.ListStatus(ctx, ListStatusOptions{
		OwnerType:       opt.OwnerType,
		TaskType:        opt.TaskType,
		TicketID:        opt.TicketID,
		WorkerID:        opt.WorkerID,
		IncludeTerminal: opt.IncludeTerminal,
		Limit:           opt.Limit,
	})
}

func (r runtimeAdapter) ListEventsAfterID(ctx context.Context, afterID uint, limit int) ([]core.TaskRuntimeEventScopeRow, error) {
	events, err := r.svc.ListEventsAfterID(ctx, afterID, limit)
	if err != nil {
		return nil, err
	}
	out := make([]core.TaskRuntimeEventScopeRow, 0, len(events))
	for _, ev := range events {
		out = append(out, core.TaskRuntimeEventScopeRow{
			TaskEvent: ev.TaskEvent,
			TicketID:  ev.TicketID,
			WorkerID:  ev.WorkerID,
			OwnerType: ev.OwnerType,
			TaskType:  ev.TaskType,
		})
	}
	return out, nil
}

var _ core.TaskRuntimeFactory = runtimeFactory{}
var _ core.TaskRuntime = runtimeAdapter{}
