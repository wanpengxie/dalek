package contracts

import "time"

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
