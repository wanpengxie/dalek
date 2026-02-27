package contracts

import (
	"fmt"
	"strings"
	"time"
)

const (
	TicketPriorityNone   = 0
	TicketPriorityLow    = 1
	TicketPriorityMedium = 2
	TicketPriorityHigh   = 3
)

// Ticket 是跨层复用的工单领域模型。
type Ticket struct {
	ID             uint                 `gorm:"primaryKey"`
	CreatedAt      time.Time            `gorm:"not null"`
	UpdatedAt      time.Time            `gorm:"not null"`
	Title          string               `gorm:"type:text;not null"`
	Description    string               `gorm:"type:text;not null;default:''"`
	WorkflowStatus TicketWorkflowStatus `gorm:"column:workflow_status;type:text;not null;default:'backlog';index"`
	Priority       int                  `gorm:"not null;default:0"`
}

func (Ticket) TableName() string {
	return "tickets"
}

func ParseTicketPriority(raw string) (int, bool) {
	switch strings.TrimSpace(strings.ToLower(raw)) {
	case "none":
		return TicketPriorityNone, true
	case "low":
		return TicketPriorityLow, true
	case "medium":
		return TicketPriorityMedium, true
	case "high":
		return TicketPriorityHigh, true
	default:
		return 0, false
	}
}

func TicketPriorityLabel(priority int) string {
	switch priority {
	case TicketPriorityNone:
		return "none"
	case TicketPriorityLow:
		return "low"
	case TicketPriorityMedium:
		return "medium"
	case TicketPriorityHigh:
		return "high"
	default:
		return fmt.Sprintf("%d", priority)
	}
}

// TicketWorkflowEvent 记录 ticket.workflow_status 的状态迁移（append-only）。
type TicketWorkflowEvent struct {
	ID        uint      `gorm:"primaryKey"`
	CreatedAt time.Time `gorm:"not null;index"`

	TicketID uint `gorm:"not null;index"`

	FromStatus TicketWorkflowStatus `gorm:"column:from_workflow_status;type:text;not null;default:'';index"`
	ToStatus   TicketWorkflowStatus `gorm:"column:to_workflow_status;type:text;not null;default:'';index"`

	Source      string  `gorm:"type:text;not null;default:'';index"`
	Reason      string  `gorm:"type:text;not null;default:''"`
	PayloadJSON JSONMap `gorm:"type:text;not null;default:'{}'"`
}

func (TicketWorkflowEvent) TableName() string {
	return "ticket_workflow_events"
}
