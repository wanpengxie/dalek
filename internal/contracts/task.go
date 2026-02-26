package contracts

import "time"

// TaskRun 是跨层复用的任务运行模型。
type TaskRun struct {
	ID        uint      `gorm:"primaryKey"`
	CreatedAt time.Time `gorm:"not null"`
	UpdatedAt time.Time `gorm:"not null"`

	OwnerType TaskOwnerType `gorm:"type:text;not null;index"`
	TaskType  string        `gorm:"type:text;not null;index"`

	ProjectKey string `gorm:"type:text;not null;index"`
	TicketID   uint   `gorm:"not null;default:0;index"`
	WorkerID   uint   `gorm:"not null;default:0;index"`

	SubjectType string `gorm:"type:text;not null;default:''"`
	SubjectID   string `gorm:"type:text;not null;default:''"`

	RequestID string `gorm:"type:text;not null;uniqueIndex"`

	OrchestrationState TaskOrchestrationState `gorm:"type:text;not null;index"`
	RunnerID           string                 `gorm:"type:text;not null;default:''"`
	LeaseExpiresAt     *time.Time             `gorm:""`
	Attempt            int                    `gorm:"not null;default:0"`

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

// TaskRuntimeSample 是 task runtime 健康状态采样记录。
type TaskRuntimeSample struct {
	ID        uint      `gorm:"primaryKey"`
	CreatedAt time.Time `gorm:"not null"`

	TaskRunID uint                   `gorm:"not null;index"`
	State     TaskRuntimeHealthState `gorm:"column:runtime_health_state;type:text;not null;index"`
	NeedsUser bool                   `gorm:"not null;default:false"`
	Summary   string                 `gorm:"type:text;not null;default:''"`
	Source    string                 `gorm:"type:text;not null;default:''"`

	ObservedAt  time.Time `gorm:"not null;index"`
	MetricsJSON string    `gorm:"type:text;not null;default:''"`
}

func (TaskRuntimeSample) TableName() string {
	return "task_runtime_samples"
}

// TaskSemanticReport 是 task semantic 报告快照。
type TaskSemanticReport struct {
	ID        uint      `gorm:"primaryKey"`
	CreatedAt time.Time `gorm:"not null"`

	TaskRunID  uint              `gorm:"not null;index"`
	Phase      TaskSemanticPhase `gorm:"column:semantic_phase;type:text;not null;index"`
	Milestone  string            `gorm:"type:text;not null;default:''"`
	NextAction string            `gorm:"type:text;not null;default:''"`
	Summary    string            `gorm:"type:text;not null;default:''"`

	ReportPayloadJSON string    `gorm:"type:text;not null;default:''"`
	ReportedAt        time.Time `gorm:"not null;index"`
}

func (TaskSemanticReport) TableName() string {
	return "task_semantic_reports"
}

// TaskEvent 是 task 运行事件日志。
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
