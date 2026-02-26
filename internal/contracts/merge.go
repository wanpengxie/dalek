package contracts

import "time"

// MergeItem 是跨层复用的合并流程模型。
type MergeItem struct {
	ID        uint      `gorm:"primaryKey"`
	CreatedAt time.Time `gorm:"not null"`
	UpdatedAt time.Time `gorm:"not null"`

	Status   MergeStatus `gorm:"type:text;not null"`
	TicketID uint        `gorm:"not null;index"`
	WorkerID uint        `gorm:"not null;default:0;index"`

	Branch     string  `gorm:"type:text;not null;default:''"`
	ChecksJSON JSONMap `gorm:"type:text;not null;default:'{}'"`

	ApprovedBy string     `gorm:"type:text;not null;default:''"`
	ApprovedAt *time.Time `gorm:""`

	MergedAt *time.Time `gorm:""`
}
