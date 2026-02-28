package contracts

import "time"

// Worker 是跨层复用的 worker 领域模型。
type Worker struct {
	ID        uint         `gorm:"primaryKey"`
	CreatedAt time.Time    `gorm:"not null"`
	UpdatedAt time.Time    `gorm:"not null"`
	TicketID  uint         `gorm:"uniqueIndex;not null"`
	Status    WorkerStatus `gorm:"type:text;not null;index"`

	WorktreePath string `gorm:"type:text;not null"`
	Branch       string `gorm:"type:text;not null"`
	ProcessPID   int    `gorm:"not null;default:0"`
	LogPath      string `gorm:"type:text;not null;default:''"`
	TmuxSocket   string `gorm:"type:text;not null"`
	TmuxSession  string `gorm:"type:text;not null;default:''"`

	StartedAt *time.Time `gorm:""`
	StoppedAt *time.Time `gorm:""`
	LastError string     `gorm:"type:text;not null;default:''"`

	RetryCount    int        `gorm:"not null;default:0"`
	LastRetryAt   *time.Time `gorm:""`
	LastErrorHash string     `gorm:"type:text;not null;default:''"`

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

func (Worker) TableName() string {
	return "workers"
}

// WorkerStatusEvent 记录 workers.status 的状态迁移（append-only）。
type WorkerStatusEvent struct {
	ID        uint      `gorm:"primaryKey"`
	CreatedAt time.Time `gorm:"not null;index"`

	WorkerID uint `gorm:"not null;index"`
	TicketID uint `gorm:"not null;default:0;index"`

	FromStatus WorkerStatus `gorm:"column:from_worker_status;type:text;not null;default:'';index"`
	ToStatus   WorkerStatus `gorm:"column:to_worker_status;type:text;not null;default:'';index"`

	Source      string  `gorm:"type:text;not null;default:'';index"`
	Reason      string  `gorm:"type:text;not null;default:''"`
	PayloadJSON JSONMap `gorm:"type:text;not null;default:'{}'"`
}

func (WorkerStatusEvent) TableName() string {
	return "worker_status_events"
}
