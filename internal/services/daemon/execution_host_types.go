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
	RunTicketWorker(ctx context.Context, ticketID uint, opt WorkerRunOptions) (WorkerRunResult, error)
	SubmitSubagentRun(ctx context.Context, opt SubagentSubmitOptions) (SubagentSubmission, error)
	RunSubagentJob(ctx context.Context, taskRunID uint, opt SubagentRunOptions) error
	FindLatestWorkerRun(ctx context.Context, ticketID uint, afterRunID uint) (*RunStatus, error)
	AddNote(ctx context.Context, rawText string) (NoteAddResult, error)
	GetTaskStatus(ctx context.Context, runID uint) (*RunStatus, error)
	ListTaskEvents(ctx context.Context, runID uint, limit int) ([]RunEvent, error)
	ListTicketViews(ctx context.Context) ([]TicketView, error)
	GetTicketViewByID(ctx context.Context, ticketID uint) (*TicketView, error)
	FocusStart(ctx context.Context, in contracts.FocusStartInput) (contracts.FocusStartResult, error)
	FocusGet(ctx context.Context, focusID uint) (contracts.FocusRunView, error)
	FocusPoll(ctx context.Context, focusID, sinceEventID uint) (contracts.FocusPollResult, error)
	FocusStop(ctx context.Context, focusID uint, requestID string) error
	FocusCancel(ctx context.Context, focusID uint, requestID string) error
}

// DashboardProject 暴露 web dashboard 聚合查询能力。
// 这是可选扩展接口，ExecutionHostProject 可以不实现。
type DashboardProject interface {
	Dashboard(ctx context.Context) (DashboardResult, error)
	ListMergeItems(ctx context.Context, opt ListMergeItemsOptions) ([]contracts.MergeItem, error)
	ListInbox(ctx context.Context, opt ListInboxOptions) ([]contracts.InboxItem, error)
}

type StartTicketOptions = pmsvc.StartOptions

type DashboardResult struct {
	TicketCounts map[string]int       `json:"ticket_counts"`
	WorkerStats  DashboardWorkerStats `json:"worker_stats"`
	MergeCounts  map[string]int       `json:"merge_counts"`
	InboxCounts  DashboardInboxCounts `json:"inbox_counts"`
}

type DashboardWorkerStats struct {
	Running    int `json:"running"`
	MaxRunning int `json:"max_running"`
	Blocked    int `json:"blocked"`
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

type WorkerRunOptions struct {
	EntryPrompt string
	AutoStart   *bool
	BaseBranch  string
}

type WorkerRunResult struct {
	TicketID uint
	WorkerID uint
	RunID    uint
}

type TicketLoopSubmitRequest struct {
	Project    string
	TicketID   uint
	RequestID  string
	Prompt     string
	AutoStart  *bool
	BaseBranch string
}

type TicketLoopSubmitReceipt struct {
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
	TicketID  uint
	RequestID string
	Reason    string
}

type TicketLoopProbeResult struct {
	Found                bool
	OwnedByCurrentDaemon bool
	Phase                string
	Project              string
	TicketID             uint
	RequestID            string
	RunID                uint
	WorkerID             uint
	CancelRequestedAt    *time.Time
	LastError            string
}

type TaskRunCancelResult struct {
	RunID     uint
	Found     bool
	Canceled  bool
	Reason    string
	FromState string
	ToState   string
}

type executionHostTaskRunCanceler interface {
	CancelTaskRun(ctx context.Context, runID uint) (TaskRunCancelResult, error)
}

type ExecutionHostOptions struct {
	Logger        *slog.Logger
	MaxConcurrent int
	OnRunSettled  func(project string)
	OnNoteAdded   func(project string)
}

type executionRunKind string

const (
	runKindWorker   executionRunKind = "ticket_loop"
	runKindSubagent executionRunKind = "subagent"
)

const (
	ticketLoopPhaseQueued  = "queued"
	ticketLoopPhaseClaimed = "claimed"
	ticketLoopPhaseErrored = "errored"
	ticketLoopPhaseRunning = pmsvc.WorkerLoopPhaseRunning
	ticketLoopPhaseRepair  = pmsvc.WorkerLoopPhaseRepairing
	ticketLoopPhaseClosing = pmsvc.WorkerLoopPhaseClosing
	ticketLoopPhaseCancel  = pmsvc.WorkerLoopPhaseCanceling
)

type executionRunHandle struct {
	kind executionRunKind

	project       string
	requestID     string
	retainRequest bool
	runID         uint
	ticketID      uint
	workerID      uint
	requestAlias  map[string]struct{}

	runnerID    string
	entryPrompt string
	autoStart   *bool
	baseBranch  string
	provider    string
	model       string

	ctx    context.Context
	cancel context.CancelFunc

	phase             string
	cancelRequestedAt *time.Time
	lastError         string

	ready     chan struct{}
	readyOnce sync.Once
	done      chan struct{}
	doneOnce  sync.Once
}
