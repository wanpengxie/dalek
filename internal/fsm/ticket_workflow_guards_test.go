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
		{name: "unknown", status: "old", want: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := CanDispatchTicket(tc.status); got != tc.want {
				t.Fatalf("CanDispatchTicket(%q)=%v, want=%v", tc.status, got, tc.want)
			}
		})
	}
}

func TestCanActivateTicket(t *testing.T) {
	tests := []struct {
		name   string
		status contracts.TicketWorkflowStatus
		want   bool
	}{
		{name: "queued", status: contracts.TicketQueued, want: true},
		{name: "backlog", status: contracts.TicketBacklog, want: true},
		{name: "blocked", status: contracts.TicketBlocked, want: true},
		{name: "active", status: contracts.TicketActive, want: false},
		{name: "done", status: contracts.TicketDone, want: false},
		{name: "archived", status: contracts.TicketArchived, want: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := CanActivateTicket(tc.status); got != tc.want {
				t.Fatalf("CanActivateTicket(%q)=%v, want=%v", tc.status, got, tc.want)
			}
		})
	}
}

func TestCanArchiveTicket(t *testing.T) {
	tests := []struct {
		name        string
		status      contracts.TicketWorkflowStatus
		integration contracts.IntegrationStatus
		want        bool
	}{
		{name: "backlog", status: contracts.TicketBacklog, integration: contracts.IntegrationNone, want: true},
		{name: "queued", status: contracts.TicketQueued, integration: contracts.IntegrationNone, want: true},
		{name: "active", status: contracts.TicketActive, integration: contracts.IntegrationNone, want: true},
		{name: "blocked", status: contracts.TicketBlocked, integration: contracts.IntegrationNone, want: true},
		{name: "done_none", status: contracts.TicketDone, integration: contracts.IntegrationNone, want: true},
		{name: "done_merged", status: contracts.TicketDone, integration: contracts.IntegrationMerged, want: true},
		{name: "done_abandoned", status: contracts.TicketDone, integration: contracts.IntegrationAbandoned, want: true},
		{name: "done_needs_merge", status: contracts.TicketDone, integration: contracts.IntegrationNeedsMerge, want: false},
		{name: "archived", status: contracts.TicketArchived, integration: contracts.IntegrationNone, want: false},
		{name: "alias_archive", status: "archive", integration: contracts.IntegrationNone, want: false},
		{name: "unknown", status: "old", integration: contracts.IntegrationNone, want: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := CanArchiveTicket(tc.status, tc.integration); got != tc.want {
				t.Fatalf("CanArchiveTicket(%q,%q)=%v, want=%v", tc.status, tc.integration, got, tc.want)
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
		{name: "unknown", status: "old", want: true},
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
		{name: "backlog_old", status: contracts.TicketBacklog, want: true},
		{name: "queued", status: contracts.TicketQueued, want: true},
		{name: "blocked", status: contracts.TicketBlocked, want: true},
		{name: "active", status: contracts.TicketActive, want: false},
		{name: "done", status: contracts.TicketDone, want: false},
		{name: "archived", status: contracts.TicketArchived, want: false},
		{name: "alias_running", status: "running", want: false},
		{name: "unknown", status: "old", want: true},
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
		{name: "backlog_old", status: contracts.TicketBacklog, want: true},
		{name: "queued", status: contracts.TicketQueued, want: true},
		{name: "active", status: contracts.TicketActive, want: true},
		{name: "blocked", status: contracts.TicketBlocked, want: false},
		{name: "done", status: contracts.TicketDone, want: false},
		{name: "archived", status: contracts.TicketArchived, want: false},
		{name: "alias_wait_user", status: "wait_user", want: false},
		{name: "unknown", status: "old", want: true},
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
		{name: "unknown", status: "old", want: true},
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
		{name: "queued_to_done_old", current: contracts.TicketQueued, target: contracts.TicketDone, want: true},
		{name: "same_state", current: contracts.TicketActive, target: contracts.TicketActive, want: true},
		{name: "unknown_to_active", current: "old", target: contracts.TicketActive, want: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := CanReportPromoteTo(tc.current, tc.target); got != tc.want {
				t.Fatalf("CanReportPromoteTo(%q,%q)=%v, want=%v", tc.current, tc.target, got, tc.want)
			}
		})
	}
}

func TestCanFreezeIntegrationAnchor(t *testing.T) {
	tests := []struct {
		name        string
		workflow    contracts.TicketWorkflowStatus
		integration contracts.IntegrationStatus
		want        bool
	}{
		{name: "done_and_empty", workflow: contracts.TicketDone, integration: contracts.IntegrationNone, want: true},
		{name: "done_and_pending_alias", workflow: contracts.TicketDone, integration: "pending", want: true},
		{name: "done_and_needs_merge", workflow: contracts.TicketDone, integration: contracts.IntegrationNeedsMerge, want: false},
		{name: "active_and_empty", workflow: contracts.TicketActive, integration: contracts.IntegrationNone, want: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := CanFreezeIntegrationAnchor(tc.workflow, tc.integration); got != tc.want {
				t.Fatalf("CanFreezeIntegrationAnchor(%q,%q)=%v, want=%v", tc.workflow, tc.integration, got, tc.want)
			}
		})
	}
}

func TestCanObserveTicketMerged(t *testing.T) {
	tests := []struct {
		name        string
		workflow    contracts.TicketWorkflowStatus
		integration contracts.IntegrationStatus
		anchor      string
		target      string
		want        bool
	}{
		{name: "done_needs_merge_ready", workflow: contracts.TicketDone, integration: contracts.IntegrationNeedsMerge, anchor: "abc123", target: "main", want: true},
		{name: "done_missing_anchor", workflow: contracts.TicketDone, integration: contracts.IntegrationNeedsMerge, target: "main", want: false},
		{name: "done_missing_target", workflow: contracts.TicketDone, integration: contracts.IntegrationNeedsMerge, anchor: "abc123", want: false},
		{name: "done_but_merged", workflow: contracts.TicketDone, integration: contracts.IntegrationMerged, anchor: "abc123", target: "main", want: false},
		{name: "active_needs_merge", workflow: contracts.TicketActive, integration: contracts.IntegrationNeedsMerge, anchor: "abc123", target: "main", want: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := CanObserveTicketMerged(tc.workflow, tc.integration, tc.anchor, tc.target); got != tc.want {
				t.Fatalf("CanObserveTicketMerged(%q,%q,%q,%q)=%v, want=%v", tc.workflow, tc.integration, tc.anchor, tc.target, got, tc.want)
			}
		})
	}
}

func TestCanAbandonTicketIntegration(t *testing.T) {
	tests := []struct {
		name        string
		workflow    contracts.TicketWorkflowStatus
		integration contracts.IntegrationStatus
		want        bool
	}{
		{name: "done_needs_merge", workflow: contracts.TicketDone, integration: contracts.IntegrationNeedsMerge, want: true},
		{name: "done_merged", workflow: contracts.TicketDone, integration: contracts.IntegrationMerged, want: true},
		{name: "done_none", workflow: contracts.TicketDone, integration: contracts.IntegrationNone, want: false},
		{name: "done_abandoned", workflow: contracts.TicketDone, integration: contracts.IntegrationAbandoned, want: false},
		{name: "active_needs_merge", workflow: contracts.TicketActive, integration: contracts.IntegrationNeedsMerge, want: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := CanAbandonTicketIntegration(tc.workflow, tc.integration); got != tc.want {
				t.Fatalf("CanAbandonTicketIntegration(%q,%q)=%v, want=%v", tc.workflow, tc.integration, got, tc.want)
			}
		})
	}
}
