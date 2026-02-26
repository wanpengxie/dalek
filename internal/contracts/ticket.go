package contracts

import "time"

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

// TicketWorkflowEvent 记录 ticket.workflow_status 的状态迁移（append-only）。
type TicketWorkflowEvent struct {
	ID        uint      `gorm:"primaryKey"`
	CreatedAt time.Time `gorm:"not null;index"`

	TicketID uint `gorm:"not null;index"`

	FromStatus TicketWorkflowStatus `gorm:"column:from_workflow_status;type:text;not null;default:'';index"`
	ToStatus   TicketWorkflowStatus `gorm:"column:to_workflow_status;type:text;not null;default:'';index"`

	Source      string `gorm:"type:text;not null;default:'';index"`
	Reason      string `gorm:"type:text;not null;default:''"`
	PayloadJSON string `gorm:"type:text;not null;default:''"`
}

func (TicketWorkflowEvent) TableName() string {
	return "ticket_workflow_events"
}
