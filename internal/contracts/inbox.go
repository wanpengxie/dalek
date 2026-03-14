package contracts

import "time"

// InboxItem 是跨层复用的待办通知模型。
type InboxItem struct {
	ID        uint      `gorm:"primaryKey"`
	CreatedAt time.Time `gorm:"not null"`
	UpdatedAt time.Time `gorm:"not null"`

	Key string `gorm:"type:text;not null;default:'';index"`

	Status   InboxStatus   `gorm:"type:text;not null"`
	Severity InboxSeverity `gorm:"type:text;not null"`
	Reason   InboxReason   `gorm:"type:text;not null"`

	Title string `gorm:"type:text;not null"`
	Body  string `gorm:"type:text;not null;default:''"`

	TicketID    uint `gorm:"not null;default:0;index"`
	WorkerID    uint `gorm:"not null;default:0;index"`
	MergeItemID uint `gorm:"not null;default:0;index"`

	// needs_user 链的 durable 元数据。
	OriginTaskRunID  uint       `gorm:"not null;default:0;index" json:"origin_task_run_id"`
	CurrentTaskRunID uint       `gorm:"not null;default:0;index" json:"current_task_run_id"`
	WaitRoundCount   int        `gorm:"not null;default:0" json:"wait_round_count"`
	ChainResolvedAt  *time.Time `json:"chain_resolved_at,omitempty"`

	// reply intent：单 ticket CLI 与 focus controller 共享这一份状态。
	ReplyAction     InboxReplyAction `gorm:"type:text;not null;default:''" json:"reply_action"`
	ReplyMarkdown   string           `gorm:"type:text;not null;default:''" json:"reply_markdown"`
	ReplyReceivedAt *time.Time       `json:"reply_received_at,omitempty"`
	ReplyConsumedAt *time.Time       `json:"reply_consumed_at,omitempty"`

	SnoozedUntil *time.Time `gorm:""`
	ClosedAt     *time.Time `gorm:""`
}
