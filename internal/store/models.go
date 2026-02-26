package store

import (
	"time"

	"dalek/internal/contracts"
)

type Ticket struct {
	ID             uint                           `gorm:"primaryKey"`
	CreatedAt      time.Time                      `gorm:"not null"`
	UpdatedAt      time.Time                      `gorm:"not null"`
	Title          string                         `gorm:"type:text;not null"`
	Description    string                         `gorm:"type:text;not null;default:''"`
	WorkflowStatus contracts.TicketWorkflowStatus `gorm:"column:workflow_status;type:text;not null;default:'backlog';index"`
	Priority       int                            `gorm:"not null;default:0"`
}

type Worker struct {
	ID        uint                   `gorm:"primaryKey"`
	CreatedAt time.Time              `gorm:"not null"`
	UpdatedAt time.Time              `gorm:"not null"`
	TicketID  uint                   `gorm:"uniqueIndex;not null"`
	Status    contracts.WorkerStatus `gorm:"type:text;not null;index"`

	WorktreePath string `gorm:"type:text;not null"`
	Branch       string `gorm:"type:text;not null"`
	TmuxSocket   string `gorm:"type:text;not null"`
	TmuxSession  string `gorm:"type:text;not null;default:''"`

	StartedAt *time.Time `gorm:""`
	StoppedAt *time.Time `gorm:""`
	LastError string     `gorm:"type:text;not null;default:''"`

	WorktreeGCRequestedAt *time.Time `gorm:""`
	WorktreeGCCleanedAt   *time.Time `gorm:""`
	WorktreeCleanupError  string     `gorm:"type:text;not null;default:''"`

	RuntimeUpdatedAt         *time.Time `gorm:""`
	RuntimeSemanticUpdatedAt *time.Time `gorm:""`
	RuntimeWatchRequestedAt  *time.Time `gorm:""`

	RuntimeStreamBytes     int64      `gorm:"not null;default:0"`
	RuntimeVisiblePlainSHA string     `gorm:"type:text;not null;default:''"`
	RuntimeAltPlainSHA     string     `gorm:"type:text;not null;default:''"`
	RuntimeLastChangeAt    *time.Time `gorm:""`

	RuntimePaneCommand string `gorm:"type:text;not null;default:''"`
	RuntimePaneInMode  bool   `gorm:"not null;default:false"`
	RuntimePaneMode    string `gorm:"type:text;not null;default:''"`
}

type PMState struct {
	ID        uint      `gorm:"primaryKey"`
	CreatedAt time.Time `gorm:"not null"`
	UpdatedAt time.Time `gorm:"not null"`

	AutopilotEnabled  bool `gorm:"not null;default:true"`
	MaxRunningWorkers int  `gorm:"not null;default:3"`

	LastTickAt  *time.Time `gorm:""`
	LastEventID uint       `gorm:"not null;default:0"`

	LastRecoveryAt           *time.Time `gorm:""`
	LastRecoveryDispatchJobs int        `gorm:"not null;default:0"`
	LastRecoveryTaskRuns     int        `gorm:"not null;default:0"`
	LastRecoveryNotes        int        `gorm:"not null;default:0"`
	LastRecoveryWorkers      int        `gorm:"not null;default:0"`
}

func (PMState) TableName() string {
	return "pm_states"
}

type InboxItem struct {
	ID        uint      `gorm:"primaryKey"`
	CreatedAt time.Time `gorm:"not null"`
	UpdatedAt time.Time `gorm:"not null"`

	Key string `gorm:"type:text;not null;default:'';index"`

	Status   contracts.InboxStatus   `gorm:"type:text;not null"`
	Severity contracts.InboxSeverity `gorm:"type:text;not null"`
	Reason   contracts.InboxReason   `gorm:"type:text;not null"`

	Title string `gorm:"type:text;not null"`
	Body  string `gorm:"type:text;not null;default:''"`

	TicketID    uint `gorm:"not null;default:0;index"`
	WorkerID    uint `gorm:"not null;default:0;index"`
	MergeItemID uint `gorm:"not null;default:0;index"`

	SnoozedUntil *time.Time `gorm:""`
	ClosedAt     *time.Time `gorm:""`
}

type MergeItem struct {
	ID        uint      `gorm:"primaryKey"`
	CreatedAt time.Time `gorm:"not null"`
	UpdatedAt time.Time `gorm:"not null"`

	Status   contracts.MergeStatus `gorm:"type:text;not null"`
	TicketID uint                  `gorm:"not null;index"`
	WorkerID uint                  `gorm:"not null;default:0;index"`

	Branch     string `gorm:"type:text;not null;default:''"`
	ChecksJSON string `gorm:"type:text;not null;default:''"`

	ApprovedBy string     `gorm:"type:text;not null;default:''"`
	ApprovedAt *time.Time `gorm:""`

	MergedAt *time.Time `gorm:""`
}

type PMDispatchJob struct {
	ID        uint      `gorm:"primaryKey"`
	CreatedAt time.Time `gorm:"not null"`
	UpdatedAt time.Time `gorm:"not null"`

	RequestID string `gorm:"type:text;not null;uniqueIndex"`

	TicketID  uint `gorm:"not null;index"`
	WorkerID  uint `gorm:"not null;index"`
	TaskRunID uint `gorm:"not null;default:0;index"`
	// 同 ticket 同时最多一个 active dispatch job（pending/running）。
	ActiveTicketKey *uint `gorm:"uniqueIndex"`

	Status contracts.PMDispatchJobStatus `gorm:"type:text;not null;index"`

	RunnerID       string     `gorm:"type:text;not null;default:''"`
	LeaseExpiresAt *time.Time `gorm:""`
	Attempt        int        `gorm:"not null;default:0"`

	ResultJSON string `gorm:"type:text;not null;default:''"`
	Error      string `gorm:"type:text;not null;default:''"`

	StartedAt  *time.Time `gorm:""`
	FinishedAt *time.Time `gorm:""`
}

func (PMDispatchJob) TableName() string {
	return "pm_dispatch_jobs"
}

type TaskRun struct {
	ID        uint      `gorm:"primaryKey"`
	CreatedAt time.Time `gorm:"not null"`
	UpdatedAt time.Time `gorm:"not null"`

	OwnerType contracts.TaskOwnerType `gorm:"type:text;not null;index"`
	TaskType  string                  `gorm:"type:text;not null;index"`

	ProjectKey string `gorm:"type:text;not null;index"`
	TicketID   uint   `gorm:"not null;default:0;index"`
	WorkerID   uint   `gorm:"not null;default:0;index"`

	SubjectType string `gorm:"type:text;not null;default:''"`
	SubjectID   string `gorm:"type:text;not null;default:''"`

	RequestID string `gorm:"type:text;not null;uniqueIndex"`

	OrchestrationState contracts.TaskOrchestrationState `gorm:"type:text;not null;index"`
	RunnerID           string                           `gorm:"type:text;not null;default:''"`
	LeaseExpiresAt     *time.Time                       `gorm:""`
	Attempt            int                              `gorm:"not null;default:0"`

	RequestPayloadJSON string `gorm:"type:text;not null;default:''"`
	ResultPayloadJSON  string `gorm:"type:text;not null;default:''"`

	ErrorCode    string `gorm:"type:text;not null;default:''"`
	ErrorMessage string `gorm:"type:text;not null;default:''"`

	StartedAt  *time.Time `gorm:""`
	FinishedAt *time.Time `gorm:""`
}

func (TaskRun) TableName() string {
	return "task_runs"
}

type SubagentRun struct {
	ID        uint      `gorm:"primaryKey"`
	CreatedAt time.Time `gorm:"not null"`
	UpdatedAt time.Time `gorm:"not null"`

	ProjectKey string `gorm:"type:text;not null;index;uniqueIndex:idx_subagent_runs_project_request,priority:1"`
	TaskRunID  uint   `gorm:"not null;index;uniqueIndex"`

	Provider string `gorm:"type:text;not null;default:''"`
	Model    string `gorm:"type:text;not null;default:''"`
	Prompt   string `gorm:"type:text;not null;default:''"`
	CWD      string `gorm:"type:text;not null;default:''"`

	RuntimeDir string `gorm:"type:text;not null;default:''"`
	RequestID  string `gorm:"type:text;not null;default:'';index;uniqueIndex:idx_subagent_runs_project_request,priority:2"`
}

func (SubagentRun) TableName() string {
	return "subagent_runs"
}

type TaskRuntimeSample struct {
	ID        uint      `gorm:"primaryKey"`
	CreatedAt time.Time `gorm:"not null"`

	TaskRunID uint                             `gorm:"not null;index"`
	State     contracts.TaskRuntimeHealthState `gorm:"column:runtime_health_state;type:text;not null;index"`
	NeedsUser bool                             `gorm:"not null;default:false"`
	Summary   string                           `gorm:"type:text;not null;default:''"`
	Source    string                           `gorm:"type:text;not null;default:''"`

	ObservedAt  time.Time `gorm:"not null;index"`
	MetricsJSON string    `gorm:"type:text;not null;default:''"`
}

func (TaskRuntimeSample) TableName() string {
	return "task_runtime_samples"
}

type TaskSemanticReport struct {
	ID        uint      `gorm:"primaryKey"`
	CreatedAt time.Time `gorm:"not null"`

	TaskRunID  uint                        `gorm:"not null;index"`
	Phase      contracts.TaskSemanticPhase `gorm:"column:semantic_phase;type:text;not null;index"`
	Milestone  string                      `gorm:"type:text;not null;default:''"`
	NextAction string                      `gorm:"type:text;not null;default:''"`
	Summary    string                      `gorm:"type:text;not null;default:''"`

	ReportPayloadJSON string    `gorm:"type:text;not null;default:''"`
	ReportedAt        time.Time `gorm:"not null;index"`
}

func (TaskSemanticReport) TableName() string {
	return "task_semantic_reports"
}

type TaskEvent struct {
	ID        uint      `gorm:"primaryKey"`
	CreatedAt time.Time `gorm:"not null;index"`

	TaskRunID uint   `gorm:"not null;index"`
	EventType string `gorm:"type:text;not null;index"`

	FromStateJSON string `gorm:"type:text;not null;default:''"`
	ToStateJSON   string `gorm:"type:text;not null;default:''"`
	Note          string `gorm:"type:text;not null;default:''"`
	PayloadJSON   string `gorm:"type:text;not null;default:''"`
}

func (TaskEvent) TableName() string {
	return "task_events"
}

// TicketWorkflowEvent 记录 ticket.workflow_status 的状态迁移（append-only）。
type TicketWorkflowEvent struct {
	ID        uint      `gorm:"primaryKey"`
	CreatedAt time.Time `gorm:"not null;index"`

	TicketID uint `gorm:"not null;index"`

	FromStatus contracts.TicketWorkflowStatus `gorm:"column:from_workflow_status;type:text;not null;default:'';index"`
	ToStatus   contracts.TicketWorkflowStatus `gorm:"column:to_workflow_status;type:text;not null;default:'';index"`

	Source      string `gorm:"type:text;not null;default:'';index"`
	Reason      string `gorm:"type:text;not null;default:''"`
	PayloadJSON string `gorm:"type:text;not null;default:''"`
}

func (TicketWorkflowEvent) TableName() string {
	return "ticket_workflow_events"
}

// WorkerStatusEvent 记录 workers.status 的状态迁移（append-only）。
type WorkerStatusEvent struct {
	ID        uint      `gorm:"primaryKey"`
	CreatedAt time.Time `gorm:"not null;index"`

	WorkerID uint `gorm:"not null;index"`
	TicketID uint `gorm:"not null;default:0;index"`

	FromStatus contracts.WorkerStatus `gorm:"column:from_worker_status;type:text;not null;default:'';index"`
	ToStatus   contracts.WorkerStatus `gorm:"column:to_worker_status;type:text;not null;default:'';index"`

	Source      string `gorm:"type:text;not null;default:'';index"`
	Reason      string `gorm:"type:text;not null;default:''"`
	PayloadJSON string `gorm:"type:text;not null;default:''"`
}

func (WorkerStatusEvent) TableName() string {
	return "worker_status_events"
}

// TaskStatusView 是只读聚合视图（task_status_view）的查询模型。
type TaskStatusView struct {
	RunID uint `gorm:"column:run_id"`

	OwnerType  string `gorm:"column:owner_type"`
	TaskType   string `gorm:"column:task_type"`
	ProjectKey string `gorm:"column:project_key"`

	TicketID uint `gorm:"column:ticket_id"`
	WorkerID uint `gorm:"column:worker_id"`

	SubjectType string `gorm:"column:subject_type"`
	SubjectID   string `gorm:"column:subject_id"`

	OrchestrationState string     `gorm:"column:orchestration_state"`
	RunnerID           string     `gorm:"column:runner_id"`
	LeaseExpiresAt     *time.Time `gorm:"column:lease_expires_at"`
	Attempt            int        `gorm:"column:attempt"`

	ErrorCode    string `gorm:"column:error_code"`
	ErrorMessage string `gorm:"column:error_message"`

	StartedAt  *time.Time `gorm:"column:started_at"`
	FinishedAt *time.Time `gorm:"column:finished_at"`
	CreatedAt  time.Time  `gorm:"column:created_at"`
	UpdatedAt  time.Time  `gorm:"column:updated_at"`

	RuntimeHealthState string     `gorm:"column:runtime_health_state"`
	RuntimeNeedsUser   bool       `gorm:"column:runtime_needs_user"`
	RuntimeSummary     string     `gorm:"column:runtime_summary"`
	RuntimeObservedAt  *time.Time `gorm:"column:runtime_observed_at"`

	SemanticPhase      string     `gorm:"column:semantic_phase"`
	SemanticMilestone  string     `gorm:"column:semantic_milestone"`
	SemanticNextAction string     `gorm:"column:semantic_next_action"`
	SemanticSummary    string     `gorm:"column:semantic_summary"`
	SemanticReportedAt *time.Time `gorm:"column:semantic_reported_at"`

	LastEventType string     `gorm:"column:last_event_type"`
	LastEventNote string     `gorm:"column:last_event_note"`
	LastEventAt   *time.Time `gorm:"column:last_event_at"`
}

func (TaskStatusView) TableName() string {
	return "task_status_view"
}

type ChannelBinding struct {
	ID        uint      `gorm:"primaryKey"`
	CreatedAt time.Time `gorm:"not null"`
	UpdatedAt time.Time `gorm:"not null"`

	ProjectName string                `gorm:"type:text;not null;default:'';index"`
	ChannelType contracts.ChannelType `gorm:"type:text;not null;index;uniqueIndex:idx_channel_binding_identity,priority:1"`
	Adapter     string                `gorm:"type:text;not null;index;uniqueIndex:idx_channel_binding_identity,priority:2"`

	PeerProjectKey string `gorm:"type:text;not null;default:'';uniqueIndex:idx_channel_binding_identity,priority:3"`
	RolePolicyJSON string `gorm:"type:text;not null;default:'{}'"`
	Enabled        bool   `gorm:"not null;default:true;index"`
}

type ChannelConversation struct {
	ID        uint      `gorm:"primaryKey"`
	CreatedAt time.Time `gorm:"not null"`
	UpdatedAt time.Time `gorm:"not null"`

	BindingID          uint   `gorm:"not null;index;uniqueIndex:idx_channel_conversation_peer,priority:1"`
	PeerConversationID string `gorm:"type:text;not null;default:'';uniqueIndex:idx_channel_conversation_peer,priority:2"`

	Title          string     `gorm:"type:text;not null;default:''"`
	Summary        string     `gorm:"type:text;not null;default:''"`
	AgentSessionID string     `gorm:"type:text;not null;default:''"`
	LastMessageAt  *time.Time `gorm:""`
}

type ChannelMessage struct {
	ID        uint      `gorm:"primaryKey"`
	CreatedAt time.Time `gorm:"not null"`

	ConversationID uint                              `gorm:"not null;index;uniqueIndex:idx_channel_message_dedup,priority:2"`
	Direction      contracts.ChannelMessageDirection `gorm:"type:text;not null;index;uniqueIndex:idx_channel_message_dedup,priority:1"`

	Adapter       string  `gorm:"type:text;not null;default:'';index;uniqueIndex:idx_channel_message_dedup,priority:3"`
	PeerMessageID *string `gorm:"type:text;uniqueIndex:idx_channel_message_dedup,priority:4"`

	SenderID    string                         `gorm:"type:text;not null;default:''"`
	SenderName  string                         `gorm:"type:text;not null;default:''"`
	ContentText string                         `gorm:"type:text;not null;default:''"`
	PayloadJSON string                         `gorm:"type:text;not null;default:'{}'"`
	Status      contracts.ChannelMessageStatus `gorm:"type:text;not null;index"`
}

type ChannelTurnJob struct {
	ID        uint      `gorm:"primaryKey"`
	CreatedAt time.Time `gorm:"not null"`
	UpdatedAt time.Time `gorm:"not null"`

	ConversationID   uint `gorm:"not null;index"`
	InboundMessageID uint `gorm:"not null;uniqueIndex"`

	Status         contracts.ChannelTurnJobStatus `gorm:"type:text;not null;index"`
	RunnerID       string                         `gorm:"type:text;not null;default:''"`
	LeaseExpiresAt *time.Time                     `gorm:""`
	Attempt        int                            `gorm:"not null;default:0"`
	Error          string                         `gorm:"type:text;not null;default:''"`
	ResultJSON     string                         `gorm:"type:text;not null;default:''"`

	StartedAt  *time.Time `gorm:""`
	FinishedAt *time.Time `gorm:""`
}

type ChannelPendingAction struct {
	ID        uint      `gorm:"primaryKey"`
	CreatedAt time.Time `gorm:"not null"`
	UpdatedAt time.Time `gorm:"not null"`

	ConversationID uint `gorm:"not null;index"`
	JobID          uint `gorm:"not null;index"`

	ActionJSON   string                               `gorm:"type:text;not null;default:'{}'"`
	Status       contracts.ChannelPendingActionStatus `gorm:"type:text;not null;index"`
	Decider      string                               `gorm:"type:text;not null;default:''"`
	DecisionNote string                               `gorm:"type:text;not null;default:''"`

	DecidedAt  *time.Time `gorm:""`
	ExecutedAt *time.Time `gorm:""`
}

type EventBusLog struct {
	ID        uint      `gorm:"primaryKey"`
	CreatedAt time.Time `gorm:"not null;index"`

	Project        string `gorm:"type:text;not null;default:'';index"`
	ConversationID string `gorm:"type:text;not null;default:''"`
	PeerMessageID  string `gorm:"type:text;not null;default:'';index"`

	Type      string `gorm:"type:text;not null;default:''"`
	RunID     string `gorm:"type:text;not null;default:''"`
	Seq       int    `gorm:"not null;default:0"`
	Stream    string `gorm:"type:text;not null;default:''"`
	EventType string `gorm:"type:text;not null;default:''"`
	Text      string `gorm:"type:text;not null;default:''"`

	AgentProvider string `gorm:"type:text;not null;default:''"`
	AgentModel    string `gorm:"type:text;not null;default:''"`
	JobStatus     string `gorm:"type:text;not null;default:''"`
	JobError      string `gorm:"type:text;not null;default:''"`
	JobErrorType  string `gorm:"type:text;not null;default:''"`
}

type ChannelOutbox struct {
	ID        uint      `gorm:"primaryKey"`
	CreatedAt time.Time `gorm:"not null"`
	UpdatedAt time.Time `gorm:"not null"`

	MessageID uint   `gorm:"not null;uniqueIndex"`
	Adapter   string `gorm:"type:text;not null;default:'';index"`

	PayloadJSON string                        `gorm:"type:text;not null;default:'{}'"`
	Status      contracts.ChannelOutboxStatus `gorm:"type:text;not null;index"`
	RetryCount  int                           `gorm:"not null;default:0"`
	NextRetryAt *time.Time                    `gorm:""`
	LastError   string                        `gorm:"type:text;not null;default:''"`
}
