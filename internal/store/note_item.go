package store

import (
	"time"

	"dalek/internal/contracts"
)

type NoteStatus string

const (
	NoteOpen      NoteStatus = "open"
	NoteShaping   NoteStatus = "shaping"
	NoteShaped    NoteStatus = "shaped"
	NoteDiscarded NoteStatus = "discarded"

	// NotePendingReviewLegacy 兼容历史数据，禁止新写入。
	NotePendingReviewLegacy NoteStatus = "pending_review"
)

type NoteItem struct {
	ID        uint      `gorm:"primaryKey"`
	CreatedAt time.Time `gorm:"not null"`
	UpdatedAt time.Time `gorm:"not null"`

	ProjectKey     string            `gorm:"type:text;not null;default:'';index"`
	Status         NoteStatus        `gorm:"type:text;not null;index"`
	Source         string            `gorm:"type:text;not null;default:'cli'"`
	Text           string            `gorm:"column:text;type:text;not null;default:''"`
	ContextJSON    contracts.JSONMap `gorm:"type:text;not null;default:'{}'"`
	NormalizedHash string            `gorm:"type:text;not null;index"`

	ShapedItemID uint   `gorm:"not null;default:0;index"`
	LastError    string `gorm:"type:text;not null;default:''"`
}

func (NoteItem) TableName() string {
	return "note_items"
}
