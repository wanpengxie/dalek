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

type InternalNodeProjectResolver interface {
	OpenNodeProject(name string) (InternalNodeProject, error)
}

type ExecutionHostProject interface {
	SubmitDispatchTicket(ctx context.Context, ticketID uint, opt DispatchSubmitOptions) (DispatchSubmission, error)
	RunDispatchJob(ctx context.Context, jobID uint, opt DispatchRunOptions) error
	DirectDispatchWorker(ctx context.Context, ticketID uint, opt WorkerRunOptions) (WorkerRunResult, error)
	SubmitRun(ctx context.Context, opt NodeRunSubmitOptions) (NodeRunSubmission, error)
	GetRun(ctx context.Context, runID uint) (*NodeRunView, error)
	GetRunLogs(ctx context.Context, runID uint, lines int) (NodeRunLogs, error)
	ListRunArtifacts(ctx context.Context, runID uint) (NodeRunArtifacts, error)
	SubmitSubagentRun(ctx context.Context, opt SubagentSubmitOptions) (SubagentSubmission, error)
	RunSubagentJob(ctx context.Context, taskRunID uint, opt SubagentRunOptions) error
	FindLatestWorkerRun(ctx context.Context, ticketID uint, afterRunID uint) (*RunStatus, error)
	AddNote(ctx context.Context, rawText string) (NoteAddResult, error)
	GetTaskStatus(ctx context.Context, runID uint) (*RunStatus, error)
	ListTaskEvents(ctx context.Context, runID uint, limit int) ([]RunEvent, error)
}

type InternalNodeProject interface {
	RegisterNode(ctx context.Context, opt NodeRegisterOptions) (NodeRegistration, error)
	BeginNodeSession(ctx context.Context, name string, observedAt *time.Time) (NodeSessionLease, error)
	HeartbeatNodeWithEpoch(ctx context.Context, name string, sessionEpoch int, observedAt *time.Time) error
	SubmitRun(ctx context.Context, opt NodeRunSubmitOptions) (NodeRunSubmission, error)
	GetRun(ctx context.Context, runID uint) (*NodeRunView, error)
	GetRunByRequestID(ctx context.Context, requestID string) (*NodeRunView, error)
	CancelRun(ctx context.Context, runID uint) (NodeRunCancelResult, error)
	GetRunLogs(ctx context.Context, runID uint, lines int) (NodeRunLogs, error)
	ListRunArtifacts(ctx context.Context, runID uint) (NodeRunArtifacts, error)
	UploadSnapshot(ctx context.Context, opt NodeSnapshotUploadOptions) (NodeSnapshotUploadResult, error)
	UploadSnapshotChunk(ctx context.Context, opt NodeSnapshotChunkUploadOptions) (NodeSnapshotChunkUploadResult, error)
	DownloadSnapshot(ctx context.Context, snapshotID string) (NodeSnapshotDownloadResult, error)
}

type NodeRegisterOptions struct {
	Name                 string
	Endpoint             string
	AuthMode             string
	Status               string
	Version              string
	ProtocolVersion      string
	RoleCapabilities     []string
	ProviderModes        []string
	DefaultProvider      string
	ProviderCapabilities map[string]any
	SessionAffinity      string
	LastSeenAt           *time.Time
}

type NodeRegistration struct {
	ID                   uint
	Name                 string
	Endpoint             string
	AuthMode             string
	Status               string
	Version              string
	ProtocolVersion      string
	RoleCapabilities     []string
	ProviderModes        []string
	DefaultProvider      string
	ProviderCapabilities map[string]any
	SessionAffinity      string
	SessionEpoch         int
	LastSeenAt           *time.Time
}

type NodeSessionLease struct {
	Name         string
	SessionEpoch int
	LastSeenAt   time.Time
}

type NodeRunView struct {
	RunID          uint
	TaskRunID      uint
	ProjectKey     string
	RequestID      string
	TicketID       uint
	WorkerID       uint
	RunStatus      string
	LifecycleStage string
	VerifyTarget   string
	SnapshotID     string
	BaseCommit     string
	Summary        string
	LastEventType  string
	LastEventNote  string
	ArtifactCount  int
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type NodeRunSubmitOptions struct {
	RequestID    string
	TicketID     uint
	VerifyTarget string
	SnapshotID   string
	BaseCommit   string
}

type NodeRunSubmission struct {
	Accepted     bool
	RunID        uint
	TaskRunID    uint
	RequestID    string
	RunStatus    string
	VerifyTarget string
	SnapshotID   string
	BaseCommit   string
}

type NodeRunCancelResult struct {
	Found    bool
	Canceled bool
	Reason   string
}

type NodeRunLogs struct {
	Found bool
	RunID uint
	Tail  string
}

type NodeRunArtifacts struct {
	Found     bool
	RunID     uint
	Artifacts []NodeRunArtifact
	Issues    []NodeRunArtifactIssue
}

type NodeRunArtifact struct {
	Name string
	Kind string
	Size int64
	Ref  string
}

type NodeRunArtifactIssue struct {
	Name   string
	Status string
	Reason string
}

type NodeSnapshotUploadOptions struct {
	SnapshotID          string
	NodeName            string
	BaseCommit          string
	WorkspaceGeneration string
	ManifestJSON        string
	ExpiresAt           *time.Time
}

type NodeSnapshotUploadResult struct {
	SnapshotID          string
	Status              string
	ManifestDigest      string
	ManifestJSON        string
	ArtifactPath        string
	BaseCommit          string
	WorkspaceGeneration string
}

type NodeSnapshotChunkUploadOptions struct {
	SnapshotID          string
	NodeName            string
	BaseCommit          string
	WorkspaceGeneration string
	ChunkIndex          int
	ChunkData           []byte
	IsFinal             bool
	ExpiresAt           *time.Time
}

type NodeSnapshotChunkUploadResult struct {
	Accepted            bool
	SnapshotID          string
	Status              string
	NextIndex           int
	ManifestDigest      string
	ManifestJSON        string
	ArtifactPath        string
	BaseCommit          string
	WorkspaceGeneration string
}

type NodeSnapshotDownloadResult struct {
	Found               bool
	SnapshotID          string
	Status              string
	ManifestDigest      string
	ManifestJSON        string
	ArtifactPath        string
	BaseCommit          string
	WorkspaceGeneration string
}

type DispatchSubmitOptions = pmsvc.DispatchSubmitOptions
type DispatchSubmission = pmsvc.DispatchSubmission
type DispatchRunOptions = pmsvc.DispatchRunOptions

type DispatchSubmitRequest struct {
	Project   string
	TicketID  uint
	RequestID string
	Prompt    string
	AutoStart *bool
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

type NoteAddResult = notebooksvc.NoteAddResult

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
