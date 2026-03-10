package daemon

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"dalek/internal/contracts"
	notebooksvc "dalek/internal/services/notebook"
	pmsvc "dalek/internal/services/pm"
	subagentsvc "dalek/internal/services/subagent"
	ticketsvc "dalek/internal/services/ticket"
)

const (
	defaultExecutionHostConcurrency = 4
	workerRunReadyTimeout           = 2 * time.Second
)

// StopTimeoutError 表示 ExecutionHost.Stop 在上下文截止前仍有运行未退出。
// 调用方可通过 errors.Is(err, context.DeadlineExceeded/Canceled) 判断超时原因，
// 也可读取 PendingCount/PendingSummary 获取未退出任务摘要。
type StopTimeoutError struct {
	Cause          error
	PendingCount   int
	PendingSummary []string
}

func (e *StopTimeoutError) Error() string {
	if e == nil {
		return ""
	}
	cause := strings.TrimSpace(fmt.Sprint(e.Cause))
	if cause == "" {
		cause = "unknown"
	}
	return fmt.Sprintf("execution host stop timeout: pending_count=%d cause=%s", e.PendingCount, cause)
}

func (e *StopTimeoutError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

type ExecutionHostResolver interface {
	OpenProject(name string) (ExecutionHostProject, error)
	ListProjects() ([]string, error)
}

type ExecutionHostProject interface {
	StartTicket(ctx context.Context, ticketID uint, opt StartTicketOptions) (*contracts.Worker, error)
	SubmitDispatchTicket(ctx context.Context, ticketID uint, opt DispatchSubmitOptions) (DispatchSubmission, error)
	RunDispatchJob(ctx context.Context, jobID uint, opt DispatchRunOptions) error
	DirectDispatchWorker(ctx context.Context, ticketID uint, opt WorkerRunOptions) (WorkerRunResult, error)
	SubmitSubagentRun(ctx context.Context, opt SubagentSubmitOptions) (SubagentSubmission, error)
	RunSubagentJob(ctx context.Context, taskRunID uint, opt SubagentRunOptions) error
	RunPlannerJob(ctx context.Context, taskRunID uint, opt PlannerRunOptions) error
	FindLatestWorkerRun(ctx context.Context, ticketID uint, afterRunID uint) (*RunStatus, error)
	AddNote(ctx context.Context, rawText string) (NoteAddResult, error)
	GetTaskStatus(ctx context.Context, runID uint) (*RunStatus, error)
	ListTaskEvents(ctx context.Context, runID uint, limit int) ([]RunEvent, error)
	ListTicketViews(ctx context.Context) ([]TicketView, error)
	GetTicketViewByID(ctx context.Context, ticketID uint) (*TicketView, error)
}

// DashboardProject 暴露 web dashboard 聚合查询能力。
// 这是可选扩展接口，ExecutionHostProject 可以不实现。
type DashboardProject interface {
	Dashboard(ctx context.Context) (DashboardResult, error)
	GetPMState(ctx context.Context) (contracts.PMState, error)
	ListMergeItems(ctx context.Context, opt ListMergeItemsOptions) ([]contracts.MergeItem, error)
	ListInbox(ctx context.Context, opt ListInboxOptions) ([]contracts.InboxItem, error)
}

type DispatchSubmitOptions = pmsvc.DispatchSubmitOptions
type DispatchSubmission = pmsvc.DispatchSubmission
type DispatchRunOptions = pmsvc.DispatchRunOptions
type StartTicketOptions = pmsvc.StartOptions

type DashboardResult struct {
	TicketCounts map[string]int       `json:"ticket_counts"`
	WorkerStats  DashboardWorkerStats `json:"worker_stats"`
	PlannerState DashboardPlannerInfo `json:"planner_state"`
	MergeCounts  map[string]int       `json:"merge_counts"`
	InboxCounts  DashboardInboxCounts `json:"inbox_counts"`
}

type DashboardWorkerStats struct {
	Running    int `json:"running"`
	MaxRunning int `json:"max_running"`
	Blocked    int `json:"blocked"`
}

type DashboardPlannerInfo struct {
	Dirty           bool       `json:"dirty"`
	WakeVersion     uint       `json:"wake_version"`
	ActiveTaskRunID *uint      `json:"active_task_run_id,omitempty"`
	CooldownUntil   *time.Time `json:"cooldown_until,omitempty"`
	LastRunAt       *time.Time `json:"last_run_at,omitempty"`
	LastError       string     `json:"last_error,omitempty"`
}

type DashboardInboxCounts struct {
	Open     int `json:"open"`
	Snoozed  int `json:"snoozed"`
	Blockers int `json:"blockers"`
}

type ListMergeItemsOptions struct {
	Status contracts.MergeStatus
	Limit  int
}

type ListInboxOptions struct {
	Status contracts.InboxStatus
	Limit  int
}

type DispatchSubmitRequest struct {
	Project    string
	TicketID   uint
	RequestID  string
	Prompt     string
	AutoStart  *bool
	BaseBranch string
}

type StartTicketRequest struct {
	Project    string
	TicketID   uint
	BaseBranch string
}

type StartTicketReceipt struct {
	Started        bool
	Project        string
	TicketID       uint
	WorkerID       uint
	WorkflowStatus contracts.TicketWorkflowStatus
	WorktreePath   string
	Branch         string
	LogPath        string
}

type DispatchSubmitReceipt struct {
	Accepted  bool
	Project   string
	RequestID string
	TaskRunID uint
	JobID     uint
	TicketID  uint
	WorkerID  uint
	JobStatus contracts.PMDispatchJobStatus
}

type WorkerRunOptions struct {
	EntryPrompt string
}

type WorkerRunResult struct {
	TicketID uint
	WorkerID uint
	RunID    uint
}

type WorkerRunSubmitRequest struct {
	Project   string
	TicketID  uint
	RequestID string
	Prompt    string
}

type WorkerRunSubmitReceipt struct {
	Accepted  bool
	Project   string
	RequestID string
	TaskRunID uint
	TicketID  uint
	WorkerID  uint
}

type SubagentSubmitOptions = subagentsvc.SubmitInput
type SubagentSubmission = subagentsvc.SubmitResult
type SubagentRunOptions = subagentsvc.RunInput

type SubagentSubmitRequest struct {
	Project string

	RequestID string
	Provider  string
	Model     string
	Prompt    string
}

type SubagentSubmitReceipt struct {
	Accepted bool

	Project    string
	TaskRunID  uint
	RequestID  string
	Provider   string
	Model      string
	RuntimeDir string
}

type PlannerSubmitRequest struct {
	Project string

	RequestID string
	TaskRunID uint
	Prompt    string
}

type PlannerSubmitReceipt struct {
	Accepted bool

	Project   string
	TaskRunID uint
	RequestID string
}

type PlannerRunOptions struct {
	RunnerID string
	Prompt   string
}

type NoteAddResult = notebooksvc.NoteAddResult
type TicketView = ticketsvc.TicketView

type NoteSubmitRequest struct {
	Project string
	Text    string
}

type NoteSubmitReceipt struct {
	Accepted     bool
	Project      string
	NoteID       uint
	ShapedItemID uint
	Deduped      bool
}

type RunStatus struct {
	RunID uint

	Project string

	OwnerType string
	TaskType  string

	TicketID uint
	WorkerID uint

	OrchestrationState string
	RuntimeHealthState string
	RuntimeNeedsUser   bool
	RuntimeSummary     string
	SemanticNextAction string
	SemanticSummary    string

	ErrorCode    string
	ErrorMessage string

	StartedAt  *time.Time
	FinishedAt *time.Time
	UpdatedAt  time.Time
}

type RunEvent struct {
	ID            uint
	TaskRunID     uint
	EventType     string
	FromStateJSON string
	ToStateJSON   string
	Note          string
	PayloadJSON   string
	CreatedAt     time.Time
}

type CancelResult struct {
	Found     bool
	Canceled  bool
	Project   string
	RequestID string
	Reason    string
}

type ExecutionHostOptions struct {
	Logger        *slog.Logger
	MaxConcurrent int
	OnRunSettled  func(project string)
	OnNoteAdded   func(project string)
}

type executionRunKind string

const (
	runKindDispatch executionRunKind = "dispatch"
	runKindWorker   executionRunKind = "worker_run"
	runKindSubagent executionRunKind = "subagent"
	runKindPlanner  executionRunKind = "planner_run"
)

type executionRunHandle struct {
	kind executionRunKind

	project   string
	requestID string
	runID     uint
	jobID     uint
	jobStatus contracts.PMDispatchJobStatus
	ticketID  uint
	workerID  uint

	runnerID    string
	entryPrompt string
	provider    string
	model       string

	ctx    context.Context
	cancel context.CancelFunc

	ready     chan struct{}
	readyOnce sync.Once
	done      chan struct{}
	doneOnce  sync.Once
}
