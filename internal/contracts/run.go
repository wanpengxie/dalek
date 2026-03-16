package contracts

import "time"

type RunStatus string

const (
	RunRequested         RunStatus = "requested"
	RunQueued            RunStatus = "queued"
	RunSnapshotPreparing RunStatus = "snapshot_preparing"
	RunSnapshotReady     RunStatus = "snapshot_ready"
	RunDispatching       RunStatus = "dispatching"
	RunEnvPreparing      RunStatus = "env_preparing"
	RunReadyToRun        RunStatus = "ready_to_run"
	RunWaitingApproval   RunStatus = "waiting_approval"
	RunRunning           RunStatus = "running"
	RunCanceling         RunStatus = "canceling"
	RunNodeOffline       RunStatus = "node_offline"
	RunReconciling       RunStatus = "reconciling"
	RunTimedOut          RunStatus = "timed_out"
	RunSucceeded         RunStatus = "succeeded"
	RunFailed            RunStatus = "failed"
	RunCanceled          RunStatus = "canceled"
)

// RunView 是 run_verify 的专用读模型。当前最小实现与 task_run 1:1 绑定。
type RunView struct {
	RunID     uint `gorm:"column:run_id;primaryKey;autoIncrement:false"`
	TaskRunID uint `gorm:"column:task_run_id;not null;uniqueIndex"`

	ProjectKey string `gorm:"column:project_key;type:text;not null;index"`
	RequestID  string `gorm:"column:request_id;type:text;not null;uniqueIndex"`
	TicketID   uint   `gorm:"column:ticket_id;not null;default:0;index"`
	WorkerID   uint   `gorm:"column:worker_id;not null;default:0;index"`

	RunStatus RunStatus `gorm:"column:run_status;type:text;not null;index"`

	VerifyTarget string `gorm:"column:verify_target;type:text;not null;default:''"`
	SnapshotID   string `gorm:"column:snapshot_id;type:text;not null;default:'';index"`
	BaseCommit   string `gorm:"column:base_commit;type:text;not null;default:''"`

	// SourceWorkspaceGeneration 暂时保留为字符串，后续接入 workspace assignment 后再细化约束。
	SourceWorkspaceGeneration string `gorm:"column:source_workspace_generation;type:text;not null;default:''"`

	CreatedAt time.Time `gorm:"not null"`
	UpdatedAt time.Time `gorm:"not null"`
}

func (RunView) TableName() string {
	return "run_views"
}
