package core

import (
	"context"
	"strings"
	"time"

	"dalek/internal/store"

	"gorm.io/gorm"
)

type TaskRuntimeFactory interface {
	ForDB(db *gorm.DB) TaskRuntime
}

type TaskRuntime interface {
	FindRunByID(ctx context.Context, runID uint) (*store.TaskRun, error)
	FindRunByRequestID(ctx context.Context, requestID string) (*store.TaskRun, error)
	LatestActiveWorkerRun(ctx context.Context, workerID uint) (*store.TaskRun, error)
	CreateRun(ctx context.Context, in TaskRuntimeCreateRunInput) (store.TaskRun, error)
	CancelActiveWorkerRuns(ctx context.Context, workerID uint, reason string, now time.Time) error
	MarkRunRunning(ctx context.Context, runID uint, runnerID string, leaseExpiresAt *time.Time, now time.Time, bumpAttempt bool) error
	RenewLease(ctx context.Context, runID uint, runnerID string, leaseExpiresAt *time.Time) error
	MarkRunSucceeded(ctx context.Context, runID uint, resultPayloadJSON string, now time.Time) error
	MarkRunFailed(ctx context.Context, runID uint, errorCode string, errorMessage string, now time.Time) error
	MarkRunCanceled(ctx context.Context, runID uint, errorCode string, errorMessage string, now time.Time) error
	AppendEvent(ctx context.Context, in TaskRuntimeEventInput) error
	AppendRuntimeSample(ctx context.Context, in TaskRuntimeRuntimeSampleInput) error
	AppendSemanticReport(ctx context.Context, in TaskRuntimeSemanticReportInput) error
	ListStatus(ctx context.Context, opt TaskRuntimeListStatusOptions) ([]store.TaskStatusView, error)
	ListEventsAfterID(ctx context.Context, afterID uint, limit int) ([]TaskRuntimeEventScopeRow, error)
}

type TaskRuntimeCreateRunInput struct {
	OwnerType store.TaskOwnerType
	TaskType  string

	ProjectKey  string
	TicketID    uint
	WorkerID    uint
	SubjectType string
	SubjectID   string

	RequestID string

	OrchestrationState store.TaskOrchestrationState
	RunnerID           string
	LeaseExpiresAt     *time.Time
	Attempt            int

	RequestPayloadJSON string
	ResultPayloadJSON  string

	ErrorCode    string
	ErrorMessage string

	StartedAt  *time.Time
	FinishedAt *time.Time
}

type TaskRuntimeEventInput struct {
	TaskRunID uint
	EventType string
	FromState any
	ToState   any
	Note      string
	Payload   any
	CreatedAt time.Time
}

type TaskRuntimeRuntimeSampleInput struct {
	TaskRunID  uint
	State      store.TaskRuntimeHealthState
	NeedsUser  bool
	Summary    string
	Source     string
	ObservedAt time.Time
	Metrics    any
}

type TaskRuntimeSemanticReportInput struct {
	TaskRunID  uint
	Phase      store.TaskSemanticPhase
	Milestone  string
	NextAction string
	Summary    string
	ReportedAt time.Time
	Payload    any
}

type TaskRuntimeListStatusOptions struct {
	OwnerType       store.TaskOwnerType
	TaskType        string
	TicketID        uint
	WorkerID        uint
	IncludeTerminal bool
	Limit           int
}

type TaskRuntimeEventScopeRow struct {
	store.TaskEvent
	TicketID  uint
	WorkerID  uint
	OwnerType string
	TaskType  string
}

func NextActionToSemanticPhase(nextAction string) store.TaskSemanticPhase {
	switch strings.TrimSpace(strings.ToLower(nextAction)) {
	case "done":
		return store.TaskPhaseDone
	case "wait_user":
		return store.TaskPhaseBlocked
	case "continue":
		return store.TaskPhaseImplementing
	default:
		return store.TaskPhaseImplementing
	}
}
