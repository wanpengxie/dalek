package app

import (
	"strings"
	"time"
)

func TaskStatusUpdatedAt(status TaskStatus) time.Time {
	latest := status.UpdatedAt
	for _, v := range []*time.Time{status.RuntimeObservedAt, status.SemanticReportedAt, status.LastEventAt} {
		if v != nil && v.After(latest) {
			latest = *v
		}
	}
	return latest
}

func DeriveRunStatus(orchestration, health string, needsUser bool) string {
	orch := strings.TrimSpace(strings.ToLower(orchestration))
	runtime := strings.TrimSpace(strings.ToLower(health))
	switch orch {
	case "pending":
		return "pending"
	case "succeeded":
		return "done"
	case "failed":
		return "failed"
	case "canceled":
		return "canceled"
	case "running":
		if needsUser || runtime == "waiting_user" {
			return "waiting_user"
		}
		switch runtime {
		case "stalled":
			return "stalled"
		case "dead":
			return "dead"
		case "alive", "idle", "busy", "unknown", "":
			return "running"
		default:
			return "running"
		}
	default:
		if needsUser || runtime == "waiting_user" {
			return "waiting_user"
		}
		switch runtime {
		case "stalled":
			return "stalled"
		case "dead":
			return "dead"
		case "alive", "idle", "busy":
			return "running"
		default:
			return "unknown"
		}
	}
}
