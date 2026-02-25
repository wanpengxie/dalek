package app

import (
	"time"

	"dalek/internal/contracts"
	"dalek/internal/store"
)

// 说明：
// app 层对外暴露的类型尽量是“稳定 API”，不要通过 type alias 伪重构把下层符号搬上来。
// 这里定义的是 UI/CLI 需要用到的最小数据结构（非业务实现）。

type DispatchResult struct {
	TicketID  uint
	WorkerID  uint
	TaskRunID uint

	TmuxSocket  string
	TmuxSession string

	WorkerCommand string
	InjectedCmd   string
}

type StartOptions struct {
	BaseBranch string
}

type DispatchOptions struct {
	EntryPrompt string
	AutoStart   *bool
}

type DispatchSubmitOptions struct {
	RequestID string
	AutoStart *bool
}

type DispatchSubmission struct {
	JobID      uint
	TaskRunID  uint
	RequestID  string
	TicketID   uint
	WorkerID   uint
	JobStatus  string
	Dispatched bool
}

type DispatchRunOptions struct {
	RunnerID    string
	EntryPrompt string
}

type DirectDispatchOptions struct {
	EntryPrompt string
	AutoStart   *bool
}

type DirectDispatchResult struct {
	TicketID       uint
	WorkerID       uint
	Stages         int
	LastNextAction string
	LastRunID      uint
}

type InterruptResult struct {
	TicketID uint
	WorkerID uint

	TmuxSocket  string
	TmuxSession string
	TargetPane  string
}

type WorktreeCleanupOptions struct {
	Force  bool
	DryRun bool
}

type WorktreeCleanupResult struct {
	TicketID    uint
	WorkerID    uint
	Worktree    string
	Branch      string
	RequestedAt *time.Time
	CleanedAt   *time.Time
	DryRun      bool
	Pending     bool
	Cleaned     bool
	Dirty       bool
	SessionLive bool
	Message     string
}

type TicketView struct {
	Ticket       Ticket
	LatestWorker *Worker
	SessionAlive bool
	// SessionProbeFailed=true 表示 tmux 会话探测失败（非离线）。
	SessionProbeFailed bool

	DerivedStatus TicketWorkflowStatus

	Capability contracts.TicketCapability

	TaskRunID uint

	RuntimeHealthState TaskRuntimeHealthState
	RuntimeNeedsUser   bool
	RuntimeSummary     string
	RuntimeObservedAt  *time.Time

	SemanticPhase      TaskSemanticPhase
	SemanticNextAction string
	SemanticSummary    string
	SemanticReportedAt *time.Time

	LastEventType string
	LastEventNote string
	LastEventAt   *time.Time
}

type WatchResult struct {
	TicketID    uint
	WorkerID    uint
	TmuxSession string

	ObservedAt time.Time
	Duration   time.Duration

	State     TaskRuntimeHealthState
	NeedsUser bool
	Summary   string
	Source    string

	StreamBytes      int64
	StreamBytesDelta int64
	StreamAgeSec     float64
	AnyScreenChanged bool
	InMode           bool
}

type ListInboxOptions struct {
	Status InboxStatus
	Limit  int
}

type ListMergeOptions struct {
	Status MergeStatus
	Limit  int
}

type ListNoteOptions struct {
	StatusOnly string
	ShapedOnly bool
	Limit      int
}

type NoteAddResult struct {
	NoteID       uint
	ShapedItemID uint
	Deduped      bool
}

type NoteView struct {
	ProjectKey     string
	ID             uint
	Status         string
	Text           string
	ContextJSON    string
	NormalizedHash string
	ShapedItemID   uint
	LastError      string
	CreatedAt      time.Time
	UpdatedAt      time.Time

	Shaped *ShapedView
}

type ShapedView struct {
	ID             uint
	ProjectKey     string
	Status         string
	Title          string
	Description    string
	AcceptanceJSON string
	PMNotes        string
	ScopeEstimate  string
	DedupKey       string
	SourceNoteIDs  string
	TicketID       uint
	ReviewComment  string
	ReviewedAt     *time.Time
	ReviewedBy     string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type ManagerTickOptions struct {
	MaxRunningWorkers int
	DryRun            bool
	SyncDispatch      bool
	DispatchTimeout   time.Duration
}

type ManagerTickResult struct {
	At time.Time

	AutopilotEnabled bool
	MaxRunning       int
	Running          int
	RunningBlocked   int
	Capacity         int

	EventsConsumed int
	InboxUpserts   int

	StartedTickets    []uint
	DispatchedTickets []uint
	MergeProposed     []uint

	Errors []string
}

type ListTaskOptions struct {
	OwnerType       TaskOwnerType
	TaskType        string
	TicketID        uint
	WorkerID        uint
	IncludeTerminal bool
	Limit           int
}

type SubagentRun struct {
	ID        uint
	TaskRunID uint

	ProjectKey string
	RequestID  string
	Provider   string
	Model      string
	Prompt     string
	CWD        string
	RuntimeDir string

	CreatedAt time.Time
	UpdatedAt time.Time
}

type CreateSubagentRunOptions struct {
	TaskRunID  uint
	RequestID  string
	Provider   string
	Model      string
	Prompt     string
	CWD        string
	RuntimeDir string
}

type SubagentSubmitOptions struct {
	RequestID string
	Provider  string
	Model     string
	Prompt    string
}

type SubagentSubmission struct {
	Accepted bool

	TaskRunID  uint
	RequestID  string
	Provider   string
	Model      string
	RuntimeDir string
}

type SubagentRunOptions struct {
	RunnerID string
}

type TaskStatus struct {
	RunID uint

	OwnerType  string
	TaskType   string
	ProjectKey string

	TicketID uint
	WorkerID uint

	SubjectType string
	SubjectID   string

	OrchestrationState string
	RunnerID           string
	LeaseExpiresAt     *time.Time
	Attempt            int

	ErrorCode    string
	ErrorMessage string

	StartedAt  *time.Time
	FinishedAt *time.Time
	CreatedAt  time.Time
	UpdatedAt  time.Time

	RuntimeHealthState string
	RuntimeNeedsUser   bool
	RuntimeSummary     string
	RuntimeObservedAt  *time.Time

	SemanticPhase      string
	SemanticMilestone  string
	SemanticNextAction string
	SemanticSummary    string
	SemanticReportedAt *time.Time

	LastEventType string
	LastEventNote string
	LastEventAt   *time.Time
}

type TaskEvent struct {
	ID        uint
	TaskRunID uint
	EventType string

	FromStateJSON string
	ToStateJSON   string
	Note          string
	PayloadJSON   string

	CreatedAt time.Time
}

type TaskCancelResult struct {
	RunID     uint
	Found     bool
	Canceled  bool
	Reason    string
	FromState string
	ToState   string
}

type GatewayProcessResult struct {
	BindingID        uint
	ConversationID   uint
	InboundMessageID uint
	JobID            uint
	RunID            string
	JobStatus        store.ChannelTurnJobStatus
	JobError         string
	JobErrorType     string

	OutboundMessageID uint
	OutboxID          uint
	ReplyText         string
	AgentProvider     string
	AgentModel        string
	AgentOutputMode   string
	AgentCommand      string
	AgentStdout       string
	AgentStderr       string
	AgentEvents       []GatewayAgentEvent
	PendingActions    []GatewayPendingAction
}

type GatewayAgentEvent struct {
	RunID  string
	Seq    int
	Stream string
	Ts     int64
	Data   GatewayAgentEventData
}

type GatewayAgentEventData struct {
	Phase     string
	StartedAt int64
	EndedAt   int64
	Text      string
	RawJSON   string
	Error     string
	ErrorType string
	ToolName  string
	ToolInput string
}

type GatewayPendingAction struct {
	ID             uint
	ConversationID uint
	JobID          uint
	ActionName     string
	ActionArgs     map[string]any
	Status         store.ChannelPendingActionStatus
	Decider        string
	DecisionNote   string
	DecidedAt      *time.Time
	ExecutedAt     *time.Time
}
