package contracts

import (
	"strings"
	"time"
)

type TicketLifecycleEventType string

const (
	TicketLifecycleCreated          TicketLifecycleEventType = "ticket.created"
	TicketLifecycleStartRequested   TicketLifecycleEventType = "ticket.start_requested"
	TicketLifecycleActivated        TicketLifecycleEventType = "ticket.activated"
	TicketLifecycleWaitUserReported TicketLifecycleEventType = "ticket.wait_user_reported"
	TicketLifecycleDoneReported     TicketLifecycleEventType = "ticket.done_reported"
	TicketLifecycleMergeObserved    TicketLifecycleEventType = "ticket.merge_observed"
	TicketLifecycleMergeAbandoned   TicketLifecycleEventType = "ticket.merge_abandoned"
	TicketLifecycleArchived         TicketLifecycleEventType = "ticket.archived"
	TicketLifecycleRepaired         TicketLifecycleEventType = "ticket.repaired"
)

func CanonicalTicketLifecycleEventType(raw TicketLifecycleEventType) TicketLifecycleEventType {
	switch v := TicketLifecycleEventType(strings.TrimSpace(strings.ToLower(string(raw)))); v {
	case TicketLifecycleCreated,
		TicketLifecycleStartRequested,
		TicketLifecycleActivated,
		TicketLifecycleWaitUserReported,
		TicketLifecycleDoneReported,
		TicketLifecycleMergeObserved,
		TicketLifecycleMergeAbandoned,
		TicketLifecycleArchived,
		TicketLifecycleRepaired:
		return v
	default:
		return v
	}
}

type TicketLifecycleActorType string

const (
	TicketLifecycleActorPM     TicketLifecycleActorType = "pm"
	TicketLifecycleActorWorker TicketLifecycleActorType = "worker"
	TicketLifecycleActorSystem TicketLifecycleActorType = "system"
	TicketLifecycleActorUser   TicketLifecycleActorType = "user"
)

func CanonicalTicketLifecycleActorType(raw TicketLifecycleActorType) TicketLifecycleActorType {
	switch v := TicketLifecycleActorType(strings.TrimSpace(strings.ToLower(string(raw)))); v {
	case TicketLifecycleActorPM,
		TicketLifecycleActorWorker,
		TicketLifecycleActorSystem,
		TicketLifecycleActorUser:
		return v
	default:
		return v
	}
}

// TicketLifecycleEvent 是 ticket 生命周期事实事件账本。
type TicketLifecycleEvent struct {
	ID        uint      `gorm:"primaryKey"`
	CreatedAt time.Time `gorm:"not null;index"`

	TicketID uint `gorm:"not null;index;uniqueIndex:idx_ticket_lifecycle_ticket_sequence,priority:1"`
	Sequence uint `gorm:"not null;uniqueIndex:idx_ticket_lifecycle_ticket_sequence,priority:2"`

	EventType TicketLifecycleEventType `gorm:"column:event_type;type:text;not null;index"`
	Source    string                   `gorm:"type:text;not null;default:'';index"`
	ActorType TicketLifecycleActorType `gorm:"column:actor_type;type:text;not null;default:'';index"`

	WorkerID  *uint `gorm:"column:worker_id;index"`
	TaskRunID *uint `gorm:"column:task_run_id;index"`

	IdempotencyKey string  `gorm:"column:idempotency_key;type:text;not null;uniqueIndex"`
	PayloadJSON    JSONMap `gorm:"type:text;not null;default:'{}'"`
}

func (TicketLifecycleEvent) TableName() string {
	return "ticket_lifecycle_events"
}
