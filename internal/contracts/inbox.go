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

	SnoozedUntil *time.Time `gorm:""`
	ClosedAt     *time.Time `gorm:""`
}
