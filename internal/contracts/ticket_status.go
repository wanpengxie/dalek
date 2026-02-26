package contracts

import "strings"

type TicketWorkflowStatus string

const (
	TicketBacklog  TicketWorkflowStatus = "backlog"
	TicketQueued   TicketWorkflowStatus = "queued"
	TicketActive   TicketWorkflowStatus = "active"
	TicketBlocked  TicketWorkflowStatus = "blocked"
	TicketDone     TicketWorkflowStatus = "done"
	TicketArchived TicketWorkflowStatus = "archived"
)

// CanonicalTicketWorkflowStatus normalizes historical and current workflow values.
func CanonicalTicketWorkflowStatus(st TicketWorkflowStatus) TicketWorkflowStatus {
	v := TicketWorkflowStatus(strings.TrimSpace(strings.ToLower(string(st))))
	switch v {
	case "", "backlog":
		return TicketBacklog
	case "queued", "queue":
		return TicketQueued
	case "active", "running", "in_progress", "in-progress", "inprogress":
		return TicketActive
	case "blocked", "wait_user", "waiting_user", "wait-user":
		return TicketBlocked
	case "done", "completed":
		return TicketDone
	case "archived", "archive":
		return TicketArchived
	default:
		return v
	}
}
