package fsm

import (
	"dalek/internal/contracts"
	"testing"
)

func TestCanStartTicket(t *testing.T) {
	tests := []struct {
		name   string
		status contracts.TicketWorkflowStatus
		want   bool
	}{
		{name: "backlog", status: contracts.TicketBacklog, want: true},
		{name: "queued", status: contracts.TicketQueued, want: true},
		{name: "active", status: contracts.TicketActive, want: true},
		{name: "blocked", status: contracts.TicketBlocked, want: true},
		{name: "done", status: contracts.TicketDone, want: false},
		{name: "archived", status: contracts.TicketArchived, want: false},
		{name: "alias_completed", status: "completed", want: false},
		{name: "alias_archive", status: "archive", want: false},
		{name: "unknown", status: "mystery", want: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := CanStartTicket(tc.status); got != tc.want {
				t.Fatalf("CanStartTicket(%q)=%v, want=%v", tc.status, got, tc.want)
			}
		})
	}
}

func TestCanDispatchTicket(t *testing.T) {
	tests := []struct {
		name   string
		status contracts.TicketWorkflowStatus
		want   bool
	}{
		{name: "queued", status: contracts.TicketQueued, want: true},
		{name: "active", status: contracts.TicketActive, want: true},
		{name: "done", status: contracts.TicketDone, want: false},
		{name: "archived", status: contracts.TicketArchived, want: false},
		{name: "unknown", status: "legacy", want: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := CanDispatchTicket(tc.status); got != tc.want {
				t.Fatalf("CanDispatchTicket(%q)=%v, want=%v", tc.status, got, tc.want)
			}
		})
	}
}

func TestCanArchiveTicket(t *testing.T) {
	tests := []struct {
		name   string
		status contracts.TicketWorkflowStatus
		want   bool
	}{
		{name: "backlog", status: contracts.TicketBacklog, want: true},
		{name: "queued", status: contracts.TicketQueued, want: true},
		{name: "active", status: contracts.TicketActive, want: true},
		{name: "blocked", status: contracts.TicketBlocked, want: true},
		{name: "done", status: contracts.TicketDone, want: true},
		{name: "archived", status: contracts.TicketArchived, want: false},
		{name: "alias_archive", status: "archive", want: false},
		{name: "unknown", status: "legacy", want: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := CanArchiveTicket(tc.status); got != tc.want {
				t.Fatalf("CanArchiveTicket(%q)=%v, want=%v", tc.status, got, tc.want)
			}
		})
	}
}

func TestCanManualSetWorkflowStatus(t *testing.T) {
	tests := []struct {
		name   string
		status contracts.TicketWorkflowStatus
		want   bool
	}{
		{name: "backlog", status: contracts.TicketBacklog, want: true},
		{name: "done", status: contracts.TicketDone, want: true},
		{name: "archived", status: contracts.TicketArchived, want: false},
		{name: "alias_archive", status: "archive", want: false},
		{name: "unknown", status: "legacy", want: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := CanManualSetWorkflowStatus(tc.status); got != tc.want {
				t.Fatalf("CanManualSetWorkflowStatus(%q)=%v, want=%v", tc.status, got, tc.want)
			}
		})
	}
}

func TestShouldPromoteOnDispatchClaim(t *testing.T) {
	tests := []struct {
		name   string
		status contracts.TicketWorkflowStatus
		want   bool
	}{
		{name: "backlog_legacy", status: contracts.TicketBacklog, want: true},
		{name: "queued", status: contracts.TicketQueued, want: true},
		{name: "blocked", status: contracts.TicketBlocked, want: true},
		{name: "active", status: contracts.TicketActive, want: false},
		{name: "done", status: contracts.TicketDone, want: false},
		{name: "archived", status: contracts.TicketArchived, want: false},
		{name: "alias_running", status: "running", want: false},
		{name: "unknown", status: "legacy", want: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := ShouldPromoteOnDispatchClaim(tc.status); got != tc.want {
				t.Fatalf("ShouldPromoteOnDispatchClaim(%q)=%v, want=%v", tc.status, got, tc.want)
			}
		})
	}
}

func TestShouldDemoteOnDispatchFailed(t *testing.T) {
	tests := []struct {
		name   string
		status contracts.TicketWorkflowStatus
		want   bool
	}{
		{name: "backlog_legacy", status: contracts.TicketBacklog, want: true},
		{name: "queued", status: contracts.TicketQueued, want: true},
		{name: "active", status: contracts.TicketActive, want: true},
		{name: "blocked", status: contracts.TicketBlocked, want: false},
		{name: "done", status: contracts.TicketDone, want: false},
		{name: "archived", status: contracts.TicketArchived, want: false},
		{name: "alias_wait_user", status: "wait_user", want: false},
		{name: "unknown", status: "legacy", want: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := ShouldDemoteOnDispatchFailed(tc.status); got != tc.want {
				t.Fatalf("ShouldDemoteOnDispatchFailed(%q)=%v, want=%v", tc.status, got, tc.want)
			}
		})
	}
}

func TestShouldApplyWorkerReport(t *testing.T) {
	tests := []struct {
		name   string
		status contracts.TicketWorkflowStatus
		want   bool
	}{
		{name: "backlog", status: contracts.TicketBacklog, want: true},
		{name: "done", status: contracts.TicketDone, want: true},
		{name: "archived", status: contracts.TicketArchived, want: false},
		{name: "alias_archive", status: "archive", want: false},
		{name: "unknown", status: "legacy", want: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := ShouldApplyWorkerReport(tc.status); got != tc.want {
				t.Fatalf("ShouldApplyWorkerReport(%q)=%v, want=%v", tc.status, got, tc.want)
			}
		})
	}
}

func TestCanReportPromoteTo(t *testing.T) {
	tests := []struct {
		name    string
		current contracts.TicketWorkflowStatus
		target  contracts.TicketWorkflowStatus
		want    bool
	}{
		{name: "archived_to_active", current: contracts.TicketArchived, target: contracts.TicketActive, want: false},
		{name: "alias_archive_to_done", current: "archive", target: contracts.TicketDone, want: false},
		{name: "done_to_active_blocked", current: contracts.TicketDone, target: contracts.TicketActive, want: false},
		{name: "alias_completed_to_running_blocked", current: "completed", target: "running", want: false},
		{name: "done_to_blocked", current: contracts.TicketDone, target: contracts.TicketBlocked, want: true},
		{name: "active_to_done", current: contracts.TicketActive, target: contracts.TicketDone, want: true},
		{name: "queued_to_active", current: contracts.TicketQueued, target: contracts.TicketActive, want: true},
		{name: "queued_to_done_legacy", current: contracts.TicketQueued, target: contracts.TicketDone, want: true},
		{name: "same_state", current: contracts.TicketActive, target: contracts.TicketActive, want: true},
		{name: "unknown_to_active", current: "legacy", target: contracts.TicketActive, want: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := CanReportPromoteTo(tc.current, tc.target); got != tc.want {
				t.Fatalf("CanReportPromoteTo(%q,%q)=%v, want=%v", tc.current, tc.target, got, tc.want)
			}
		})
	}
}
