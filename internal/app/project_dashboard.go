package app

import (
	"context"
	"fmt"
	"time"
)

type DashboardResult struct {
	TicketCounts DashboardTicketCounts `json:"ticket_counts"`
	WorkerStats  DashboardWorkerStats  `json:"worker_stats"`
	PlannerState DashboardPlannerState `json:"planner_state"`
	MergeCounts  DashboardMergeCounts  `json:"merge_counts"`
	InboxCounts  DashboardInboxCounts  `json:"inbox_counts"`
}

type DashboardTicketCounts struct {
	Backlog  int `json:"backlog"`
	Queued   int `json:"queued"`
	Active   int `json:"active"`
	Blocked  int `json:"blocked"`
	Done     int `json:"done"`
	Archived int `json:"archived"`
}

type DashboardWorkerStats struct {
	Running    int `json:"running"`
	MaxRunning int `json:"max_running"`
	Blocked    int `json:"blocked"`
}

type DashboardPlannerState struct {
	Dirty         bool       `json:"dirty"`
	ActiveRunID   *uint      `json:"active_run_id,omitempty"`
	CooldownUntil *time.Time `json:"cooldown_until,omitempty"`
	LastRunAt     *time.Time `json:"last_run_at,omitempty"`
	LastError     string     `json:"last_error,omitempty"`
}

type DashboardMergeCounts struct {
	Proposed int `json:"proposed"`
	Ready    int `json:"ready"`
	Approved int `json:"approved"`
	Merged   int `json:"merged"`
}

type DashboardInboxCounts struct {
	Open     int `json:"open"`
	Snoozed  int `json:"snoozed"`
	Blockers int `json:"blockers"`
}

func (p *Project) Dashboard(ctx context.Context) (DashboardResult, error) {
	if p == nil || p.pm == nil {
		return DashboardResult{}, fmt.Errorf("project pm service 为空")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	_ = ctx
	return DashboardResult{
		WorkerStats: DashboardWorkerStats{
			MaxRunning: 3,
		},
	}, nil
}
