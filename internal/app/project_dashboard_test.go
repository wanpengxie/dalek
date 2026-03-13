package app

import (
	"context"
	"testing"
	"time"

	"dalek/internal/contracts"
)

func TestIntegration_Dashboard_EmptyProjectReturnsZeroCounts(t *testing.T) {
	p := newIntegrationProject(t)
	ctx := context.Background()

	pmState, err := p.GetPMState(ctx)
	if err != nil {
		t.Fatalf("GetPMState failed: %v", err)
	}

	got, err := p.Dashboard(ctx)
	if err != nil {
		t.Fatalf("Dashboard failed: %v", err)
	}

	assertDashboardCount(t, got.TicketCounts, string(contracts.TicketBacklog), 0)
	assertDashboardCount(t, got.TicketCounts, string(contracts.TicketQueued), 0)
	assertDashboardCount(t, got.TicketCounts, string(contracts.TicketActive), 0)
	assertDashboardCount(t, got.TicketCounts, string(contracts.TicketBlocked), 0)
	assertDashboardCount(t, got.TicketCounts, string(contracts.TicketDone), 0)
	assertDashboardCount(t, got.TicketCounts, string(contracts.TicketArchived), 0)

	assertDashboardCount(t, got.MergeCounts, string(contracts.MergeProposed), 0)
	assertDashboardCount(t, got.MergeCounts, string(contracts.MergeChecksRunning), 0)
	assertDashboardCount(t, got.MergeCounts, string(contracts.MergeReady), 0)
	assertDashboardCount(t, got.MergeCounts, string(contracts.MergeApproved), 0)
	assertDashboardCount(t, got.MergeCounts, string(contracts.MergeMerged), 0)
	assertDashboardCount(t, got.MergeCounts, string(contracts.MergeDiscarded), 0)
	assertDashboardCount(t, got.MergeCounts, string(contracts.MergeBlocked), 0)

	if got.WorkerStats.Running != 0 {
		t.Fatalf("unexpected worker running count: got=%d want=0", got.WorkerStats.Running)
	}
	if got.WorkerStats.Blocked != 0 {
		t.Fatalf("unexpected worker blocked count: got=%d want=0", got.WorkerStats.Blocked)
	}
	if got.WorkerStats.MaxRunning != pmState.MaxRunningWorkers {
		t.Fatalf("unexpected worker max_running: got=%d want=%d", got.WorkerStats.MaxRunning, pmState.MaxRunningWorkers)
	}

	if got.InboxCounts.Open != 0 || got.InboxCounts.Snoozed != 0 || got.InboxCounts.Blockers != 0 {
		t.Fatalf("unexpected inbox counts: %+v", got.InboxCounts)
	}
}

func TestIntegration_Dashboard_AggregatesServiceData(t *testing.T) {
	p := newIntegrationProject(t)
	ctx := context.Background()

	if _, err := p.SetMaxRunningWorkers(ctx, 7); err != nil {
		t.Fatalf("SetMaxRunningWorkers failed: %v", err)
	}

	backlogTicket, err := p.CreateTicketWithDescription(ctx, "dashboard backlog", "")
	if err != nil {
		t.Fatalf("Create backlog ticket failed: %v", err)
	}
	queuedTicket, err := p.CreateTicketWithDescription(ctx, "dashboard queued", "")
	if err != nil {
		t.Fatalf("Create queued ticket failed: %v", err)
	}
	if err := p.SetTicketWorkflowStatus(ctx, queuedTicket.ID, contracts.TicketQueued); err != nil {
		t.Fatalf("Set queued status failed: %v", err)
	}
	activeTicket, err := p.CreateTicketWithDescription(ctx, "dashboard active", "")
	if err != nil {
		t.Fatalf("Create active ticket failed: %v", err)
	}
	activeWorker, err := p.StartTicket(ctx, activeTicket.ID)
	if err != nil {
		t.Fatalf("Start active ticket failed: %v", err)
	}
	if err := p.SetTicketWorkflowStatus(ctx, activeTicket.ID, contracts.TicketActive); err != nil {
		t.Fatalf("Set active status failed: %v", err)
	}
	if err := mustProjectDB(t, p).WithContext(ctx).Model(&contracts.Worker{}).Where("id = ?", activeWorker.ID).Updates(map[string]any{
		"status":     contracts.WorkerRunning,
		"updated_at": time.Now(),
	}).Error; err != nil {
		t.Fatalf("set active worker running failed: %v", err)
	}
	blockedTicket, err := p.CreateTicketWithDescription(ctx, "dashboard blocked", "")
	if err != nil {
		t.Fatalf("Create blocked ticket failed: %v", err)
	}
	if err := p.SetTicketWorkflowStatus(ctx, blockedTicket.ID, contracts.TicketBlocked); err != nil {
		t.Fatalf("Set blocked status failed: %v", err)
	}
	doneTicket, err := p.CreateTicketWithDescription(ctx, "dashboard done", "")
	if err != nil {
		t.Fatalf("Create done ticket failed: %v", err)
	}
	if err := p.SetTicketWorkflowStatus(ctx, doneTicket.ID, contracts.TicketDone); err != nil {
		t.Fatalf("Set done status failed: %v", err)
	}
	archivedTicket, err := p.CreateTicketWithDescription(ctx, "dashboard archived", "")
	if err != nil {
		t.Fatalf("Create archived ticket failed: %v", err)
	}
	if err := p.ArchiveTicket(ctx, archivedTicket.ID); err != nil {
		t.Fatalf("Archive ticket failed: %v", err)
	}

	mergeItems := []contracts.MergeItem{
		{Status: contracts.MergeProposed, TicketID: activeTicket.ID, WorkerID: activeWorker.ID, Branch: "ts/demo/merge-proposed"},
		{Status: contracts.MergeApproved, TicketID: doneTicket.ID, Branch: "ts/demo/merge-approved"},
		{Status: contracts.MergeBlocked, TicketID: blockedTicket.ID, Branch: "ts/demo/merge-blocked"},
	}
	if err := mustProjectDB(t, p).WithContext(ctx).Create(&mergeItems).Error; err != nil {
		t.Fatalf("create merge items failed: %v", err)
	}

	inboxItems := []contracts.InboxItem{
		{
			Key:      "dashboard-open-blocker",
			Status:   contracts.InboxOpen,
			Severity: contracts.InboxBlocker,
			Reason:   contracts.InboxNeedsUser,
			Title:    "need user",
			Body:     "blocker",
			TicketID: blockedTicket.ID,
		},
		{
			Key:      "dashboard-open-info",
			Status:   contracts.InboxOpen,
			Severity: contracts.InboxInfo,
			Reason:   contracts.InboxQuestion,
			Title:    "question",
			Body:     "open",
			TicketID: backlogTicket.ID,
		},
		{
			Key:      "dashboard-snoozed",
			Status:   contracts.InboxSnoozed,
			Severity: contracts.InboxWarn,
			Reason:   contracts.InboxApprovalRequired,
			Title:    "snoozed",
			Body:     "snoozed",
			TicketID: queuedTicket.ID,
		},
		{
			Key:      "dashboard-done",
			Status:   contracts.InboxDone,
			Severity: contracts.InboxInfo,
			Reason:   contracts.InboxIncident,
			Title:    "done",
			Body:     "done",
			TicketID: doneTicket.ID,
		},
	}
	if err := mustProjectDB(t, p).WithContext(ctx).Create(&inboxItems).Error; err != nil {
		t.Fatalf("create inbox items failed: %v", err)
	}

	got, err := p.Dashboard(ctx)
	if err != nil {
		t.Fatalf("Dashboard failed: %v", err)
	}

	assertDashboardCount(t, got.TicketCounts, string(contracts.TicketBacklog), 1)
	assertDashboardCount(t, got.TicketCounts, string(contracts.TicketQueued), 1)
	assertDashboardCount(t, got.TicketCounts, string(contracts.TicketActive), 1)
	assertDashboardCount(t, got.TicketCounts, string(contracts.TicketBlocked), 1)
	assertDashboardCount(t, got.TicketCounts, string(contracts.TicketDone), 1)
	assertDashboardCount(t, got.TicketCounts, string(contracts.TicketArchived), 1)

	if got.WorkerStats.Running != 1 {
		t.Fatalf("unexpected worker running count: got=%d want=1", got.WorkerStats.Running)
	}
	if got.WorkerStats.Blocked != 1 {
		t.Fatalf("unexpected worker blocked count: got=%d want=1", got.WorkerStats.Blocked)
	}
	if got.WorkerStats.MaxRunning != 7 {
		t.Fatalf("unexpected worker max running: got=%d want=7", got.WorkerStats.MaxRunning)
	}

	assertDashboardCount(t, got.MergeCounts, string(contracts.MergeProposed), 1)
	assertDashboardCount(t, got.MergeCounts, string(contracts.MergeApproved), 1)
	assertDashboardCount(t, got.MergeCounts, string(contracts.MergeBlocked), 1)
	assertDashboardCount(t, got.MergeCounts, string(contracts.MergeChecksRunning), 0)
	assertDashboardCount(t, got.MergeCounts, string(contracts.MergeReady), 0)
	assertDashboardCount(t, got.MergeCounts, string(contracts.MergeMerged), 0)
	assertDashboardCount(t, got.MergeCounts, string(contracts.MergeDiscarded), 0)

	if got.InboxCounts.Open != 2 {
		t.Fatalf("unexpected inbox open count: got=%d want=2", got.InboxCounts.Open)
	}
	if got.InboxCounts.Snoozed != 1 {
		t.Fatalf("unexpected inbox snoozed count: got=%d want=1", got.InboxCounts.Snoozed)
	}
	if got.InboxCounts.Blockers != 1 {
		t.Fatalf("unexpected inbox blockers count: got=%d want=1", got.InboxCounts.Blockers)
	}
}

func assertDashboardCount(t *testing.T, m map[string]int, key string, want int) {
	t.Helper()
	got := m[key]
	if got != want {
		t.Fatalf("unexpected count[%s]: got=%d want=%d map=%v", key, got, want, m)
	}
}
