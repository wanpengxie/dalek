package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"dalek/internal/contracts"
)

const dashboardListLimit = 2000

type DashboardResult struct {
	TicketCounts map[string]int       `json:"ticket_counts"`
	WorkerStats  DashboardWorkerStats `json:"worker_stats"`
	PlannerState DashboardPlannerInfo `json:"planner_state"`
	MergeCounts  map[string]int       `json:"merge_counts"`
	InboxCounts  DashboardInboxCounts `json:"inbox_counts"`
}

type DashboardWorkerStats struct {
	Running    int `json:"running"`
	MaxRunning int `json:"max_running"`
	Blocked    int `json:"blocked"`
}

type DashboardPlannerInfo struct {
	Dirty           bool       `json:"dirty"`
	WakeVersion     uint       `json:"wake_version"`
	ActiveTaskRunID *uint      `json:"active_task_run_id,omitempty"`
	CooldownUntil   *time.Time `json:"cooldown_until,omitempty"`
	LastRunAt       *time.Time `json:"last_run_at,omitempty"`
	LastError       string     `json:"last_error,omitempty"`
}

type DashboardInboxCounts struct {
	Open     int `json:"open"`
	Snoozed  int `json:"snoozed"`
	Blockers int `json:"blockers"`
}

func (p *Project) Dashboard(ctx context.Context) (DashboardResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	result := DashboardResult{
		TicketCounts: newDashboardTicketCounts(),
		MergeCounts:  newDashboardMergeCounts(),
	}

	tickets, err := p.ListTickets(ctx, true)
	if err != nil {
		return DashboardResult{}, fmt.Errorf("list tickets: %w", err)
	}
	blockedTickets := 0
	for _, ticket := range tickets {
		key := dashboardTicketStatusKey(ticket.WorkflowStatus)
		result.TicketCounts[key]++
		if contracts.CanonicalTicketWorkflowStatus(ticket.WorkflowStatus) == contracts.TicketBlocked {
			blockedTickets++
		}
	}

	runningWorkers, err := p.ListRunningWorkers(ctx)
	if err != nil {
		return DashboardResult{}, fmt.Errorf("list running workers: %w", err)
	}

	pmState, err := p.GetPMState(ctx)
	if err != nil {
		return DashboardResult{}, fmt.Errorf("get pm state: %w", err)
	}
	result.WorkerStats = DashboardWorkerStats{
		Running:    len(runningWorkers),
		MaxRunning: pmState.MaxRunningWorkers,
		Blocked:    blockedTickets,
	}
	result.PlannerState = DashboardPlannerInfo{
		Dirty:           pmState.PlannerDirty,
		WakeVersion:     pmState.PlannerWakeVersion,
		ActiveTaskRunID: cloneUintPtr(pmState.PlannerActiveTaskRunID),
		CooldownUntil:   cloneTimePtr(pmState.PlannerCooldownUntil),
		LastRunAt:       cloneTimePtr(pmState.PlannerLastRunAt),
		LastError:       strings.TrimSpace(pmState.PlannerLastError),
	}

	mergeItems, err := p.ListMergeItems(ctx, ListMergeOptions{Limit: dashboardListLimit})
	if err != nil {
		return DashboardResult{}, fmt.Errorf("list merge items: %w", err)
	}
	for _, item := range mergeItems {
		key := dashboardMergeStatusKey(item.Status)
		result.MergeCounts[key]++
	}

	openInbox, err := p.ListInbox(ctx, ListInboxOptions{
		Status: contracts.InboxOpen,
		Limit:  dashboardListLimit,
	})
	if err != nil {
		return DashboardResult{}, fmt.Errorf("list open inbox: %w", err)
	}
	snoozedInbox, err := p.ListInbox(ctx, ListInboxOptions{
		Status: contracts.InboxSnoozed,
		Limit:  dashboardListLimit,
	})
	if err != nil {
		return DashboardResult{}, fmt.Errorf("list snoozed inbox: %w", err)
	}
	result.InboxCounts.Open = len(openInbox)
	result.InboxCounts.Snoozed = len(snoozedInbox)
	for _, item := range openInbox {
		if item.Severity == contracts.InboxBlocker {
			result.InboxCounts.Blockers++
		}
	}

	return result, nil
}

func newDashboardTicketCounts() map[string]int {
	return map[string]int{
		string(contracts.TicketBacklog):  0,
		string(contracts.TicketQueued):   0,
		string(contracts.TicketActive):   0,
		string(contracts.TicketBlocked):  0,
		string(contracts.TicketDone):     0,
		string(contracts.TicketArchived): 0,
	}
}

func newDashboardMergeCounts() map[string]int {
	return map[string]int{
		string(contracts.MergeProposed):      0,
		string(contracts.MergeChecksRunning): 0,
		string(contracts.MergeReady):         0,
		string(contracts.MergeApproved):      0,
		string(contracts.MergeMerged):        0,
		string(contracts.MergeDiscarded):     0,
		string(contracts.MergeBlocked):       0,
	}
}

func dashboardTicketStatusKey(status contracts.TicketWorkflowStatus) string {
	return normalizeDashboardStatusKey(string(contracts.CanonicalTicketWorkflowStatus(status)))
}

func dashboardMergeStatusKey(status contracts.MergeStatus) string {
	return normalizeDashboardStatusKey(string(status))
}

func normalizeDashboardStatusKey(raw string) string {
	key := strings.TrimSpace(strings.ToLower(raw))
	if key == "" {
		return "unknown"
	}
	return key
}

func cloneUintPtr(src *uint) *uint {
	if src == nil {
		return nil
	}
	v := *src
	return &v
}

func cloneTimePtr(src *time.Time) *time.Time {
	if src == nil {
		return nil
	}
	v := *src
	return &v
}
