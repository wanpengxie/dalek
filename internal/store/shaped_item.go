package store

import "time"

type ShapedItemStatus string

const (
	ShapedPendingReview ShapedItemStatus = "pending_review"
	ShapedApproved      ShapedItemStatus = "approved"
	ShapedRejected      ShapedItemStatus = "rejected"
	ShapedNeedsInfo     ShapedItemStatus = "needs_info"
)

type ShapedItem struct {
	ID        uint      `gorm:"primaryKey"`
	CreatedAt time.Time `gorm:"not null"`
	UpdatedAt time.Time `gorm:"not null"`

	// dedup_key 非空时，(project_key, dedup_key) 必须唯一；partial index 由 migration 兜底保持一致。
	ProjectKey string           `gorm:"type:text;not null;default:'';index;uniqueIndex:idx_shaped_items_project_dedup,priority:1,where:trim(dedup_key) <> ''"`
	Status     ShapedItemStatus `gorm:"type:text;not null;index"`

	Title          string `gorm:"type:text;not null;default:''"`
	Description    string `gorm:"type:text;not null;default:''"`
	AcceptanceJSON string `gorm:"type:text;not null;default:'[]'"`
	PMNotes        string `gorm:"type:text;not null;default:''"`
	ScopeEstimate  string `gorm:"type:text;not null;default:''"`
	DedupKey       string `gorm:"type:text;not null;default:'';index;uniqueIndex:idx_shaped_items_project_dedup,priority:2,where:trim(dedup_key) <> ''"`

	SourceNoteIDs string `gorm:"type:text;not null;default:'[]'"`
	TicketID      uint   `gorm:"not null;default:0;index"`

	ReviewComment string     `gorm:"type:text;not null;default:''"`
	ReviewedAt    *time.Time `gorm:""`
	ReviewedBy    string     `gorm:"type:text;not null;default:''"`
}

func (ShapedItem) TableName() string {
	return "shaped_items"
}
