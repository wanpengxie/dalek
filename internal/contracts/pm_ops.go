package contracts

import "time"

type PMOpKind string

const (
	PMOpWriteRequirementDoc  PMOpKind = "write_requirement_doc"
	PMOpWriteDesignDoc       PMOpKind = "write_design_doc"
	PMOpCreateTicket         PMOpKind = "create_ticket"
	PMOpDispatchTicket       PMOpKind = "dispatch_ticket"
	PMOpApproveMerge         PMOpKind = "approve_merge"
	PMOpDiscardMerge         PMOpKind = "discard_merge"
	PMOpCreateIntegration    PMOpKind = "create_integration_ticket"
	PMOpCloseInbox           PMOpKind = "close_inbox"
	PMOpRunAcceptance        PMOpKind = "run_acceptance"
	PMOpSetFeatureStatus     PMOpKind = "set_feature_status"
	PMOpSetFeatureStatusDone string   = "done"
)

type PMOp struct {
	OpID           string          `json:"op_id"`
	FeatureID      string          `json:"feature_id,omitempty"`
	RequestID      string          `json:"request_id,omitempty"`
	Kind           PMOpKind        `json:"kind"`
	Arguments      JSONMap         `json:"arguments,omitempty"`
	Preconditions  JSONStringSlice `json:"preconditions,omitempty"`
	IdempotencyKey string          `json:"idempotency_key,omitempty"`
	Critical       bool            `json:"critical,omitempty"`
}

type PMOpJournalStatus string

const (
	PMOpStatusPlanned    PMOpJournalStatus = "planned"
	PMOpStatusRunning    PMOpJournalStatus = "running"
	PMOpStatusSucceeded  PMOpJournalStatus = "succeeded"
	PMOpStatusFailed     PMOpJournalStatus = "failed"
	PMOpStatusSuperseded PMOpJournalStatus = "superseded"
)

// PMOpJournalEntry 持久化 planner 执行出的 PMOps 状态机。
type PMOpJournalEntry struct {
	ID        uint      `gorm:"primaryKey"`
	CreatedAt time.Time `gorm:"not null;index"`
	UpdatedAt time.Time `gorm:"not null;index"`

	InstanceID   string `gorm:"type:text;not null;index"`
	PlannerRunID uint   `gorm:"not null;default:0;index"`

	OpID           string          `gorm:"type:text;not null;index:idx_loop_op_instance_op,priority:2"`
	FeatureID      string          `gorm:"type:text;not null;default:''"`
	RequestID      string          `gorm:"type:text;not null;default:'';index"`
	Kind           PMOpKind        `gorm:"type:text;not null;index"`
	IdempotencyKey string          `gorm:"type:text;not null;default:'';index"`
	ArgumentsJSON  JSONMap         `gorm:"type:text;not null;default:'{}'"`
	PrecondsJSON   JSONStringSlice `gorm:"type:text;not null;default:'[]'"`
	Critical       bool            `gorm:"not null;default:false"`

	Status       PMOpJournalStatus `gorm:"type:text;not null;index"`
	ResultJSON   JSONMap           `gorm:"type:text;not null;default:'{}'"`
	ErrorText    string            `gorm:"type:text;not null;default:''"`
	SupersededBy string            `gorm:"type:text;not null;default:''"`
	StartedAt    *time.Time        `gorm:""`
	FinishedAt   *time.Time        `gorm:""`
}

func (PMOpJournalEntry) TableName() string {
	return "loop_op_journal"
}

type PMCheckpoint struct {
	ID        uint      `gorm:"primaryKey"`
	CreatedAt time.Time `gorm:"not null;index"`
	UpdatedAt time.Time `gorm:"not null;index"`

	InstanceID   string `gorm:"type:text;not null;index:idx_loop_cp_instance_rev,priority:1"`
	PlannerRunID uint   `gorm:"not null;default:0;index"`
	Revision     int    `gorm:"not null;default:1;index:idx_loop_cp_instance_rev,priority:2"`

	GraphVersion     string          `gorm:"type:text;not null;default:''"`
	CompletedOpsJSON JSONStringSlice `gorm:"type:text;not null;default:'[]'"`
	RemainingOpsJSON JSONStringSlice `gorm:"type:text;not null;default:'[]'"`
	NextAction       string          `gorm:"type:text;not null;default:''"`
	FailureContext   JSONMap         `gorm:"type:text;not null;default:'{}'"`
	SnapshotJSON     JSONMap         `gorm:"type:text;not null;default:'{}'"`
}

func (PMCheckpoint) TableName() string {
	return "loop_checkpoints"
}
