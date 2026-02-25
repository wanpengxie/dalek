package store

import "strings"

// CanonicalTicketWorkflowStatus 统一历史与当前的 workflow_status 枚举值。
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
