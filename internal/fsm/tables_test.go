package fsm

import (
	"dalek/internal/contracts"
	"testing"
)

func TestDomainTablesValidate(t *testing.T) {
	tables := []struct {
		name  string
		check func() error
	}{
		{name: "ticket_workflow", check: TicketWorkflowTable.Validate},
		{name: "worker_lifecycle", check: WorkerLifecycleTable.Validate},
		{name: "task_run_orchestration", check: TaskRunOrchestrationTable.Validate},
	}
	for _, tc := range tables {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.check(); err != nil {
				t.Fatalf("unexpected validate error: %v", err)
			}
		})
	}
}

func TestTicketWorkflowTable(t *testing.T) {
	tests := []struct {
		name string
		from contracts.TicketWorkflowStatus
		to   contracts.TicketWorkflowStatus
		want bool
	}{
		{name: "backlog_to_active", from: contracts.TicketBacklog, to: contracts.TicketActive, want: true},
		{name: "backlog_to_queued", from: contracts.TicketBacklog, to: contracts.TicketQueued, want: true},
		{name: "queued_to_active", from: contracts.TicketQueued, to: contracts.TicketActive, want: true},
		{name: "active_to_done", from: contracts.TicketActive, to: contracts.TicketDone, want: true},
		{name: "done_to_archived", from: contracts.TicketDone, to: contracts.TicketArchived, want: true},
		{name: "done_to_active_forbidden", from: contracts.TicketDone, to: contracts.TicketActive, want: false},
		{name: "archived_to_queued_forbidden", from: contracts.TicketArchived, to: contracts.TicketQueued, want: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := CanTransition(TicketWorkflowTable, tc.from, tc.to)
			if got != tc.want {
				t.Fatalf("unexpected transition result: %s -> %s, got=%v want=%v", tc.from, tc.to, got, tc.want)
			}
		})
	}

	if !TicketWorkflowTable.IsTerminal(contracts.TicketArchived) {
		t.Fatalf("archived should be terminal")
	}
	if TicketWorkflowTable.IsTerminal(contracts.TicketDone) {
		t.Fatalf("done should not be terminal")
	}

	valid := ValidTransitions(TicketWorkflowTable, contracts.TicketQueued)
	if len(valid) != 3 || valid[0] != contracts.TicketActive || valid[1] != contracts.TicketBlocked || valid[2] != contracts.TicketArchived {
		t.Fatalf("unexpected valid transitions for queued: %v", valid)
	}
}

func TestWorkerLifecycleTable(t *testing.T) {
	tests := []struct {
		name string
		from contracts.WorkerStatus
		to   contracts.WorkerStatus
		want bool
	}{
		{name: "stopped_to_creating", from: contracts.WorkerStopped, to: contracts.WorkerCreating, want: true},
		{name: "creating_to_running", from: contracts.WorkerCreating, to: contracts.WorkerRunning, want: true},
		{name: "running_to_failed", from: contracts.WorkerRunning, to: contracts.WorkerFailed, want: true},
		{name: "failed_to_creating", from: contracts.WorkerFailed, to: contracts.WorkerCreating, want: true},
		{name: "creating_to_stopped_forbidden", from: contracts.WorkerCreating, to: contracts.WorkerStopped, want: false},
		{name: "running_to_creating_forbidden", from: contracts.WorkerRunning, to: contracts.WorkerCreating, want: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := CanTransition(WorkerLifecycleTable, tc.from, tc.to)
			if got != tc.want {
				t.Fatalf("unexpected transition result: %s -> %s, got=%v want=%v", tc.from, tc.to, got, tc.want)
			}
		})
	}
}

func TestTaskRunOrchestrationTable(t *testing.T) {
	tests := []struct {
		name string
		from contracts.TaskOrchestrationState
		to   contracts.TaskOrchestrationState
		want bool
	}{
		{name: "pending_to_running", from: contracts.TaskPending, to: contracts.TaskRunning, want: true},
		{name: "pending_to_failed", from: contracts.TaskPending, to: contracts.TaskFailed, want: true},
		{name: "pending_to_canceled", from: contracts.TaskPending, to: contracts.TaskCanceled, want: true},
		{name: "running_to_succeeded", from: contracts.TaskRunning, to: contracts.TaskSucceeded, want: true},
		{name: "running_to_failed", from: contracts.TaskRunning, to: contracts.TaskFailed, want: true},
		{name: "running_to_canceled", from: contracts.TaskRunning, to: contracts.TaskCanceled, want: true},
		{name: "succeeded_to_canceled_forbidden", from: contracts.TaskSucceeded, to: contracts.TaskCanceled, want: false},
		{name: "failed_to_canceled_forbidden", from: contracts.TaskFailed, to: contracts.TaskCanceled, want: false},
		{name: "canceled_to_running_forbidden", from: contracts.TaskCanceled, to: contracts.TaskRunning, want: false},
		{name: "pending_to_succeeded_forbidden", from: contracts.TaskPending, to: contracts.TaskSucceeded, want: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := CanTransition(TaskRunOrchestrationTable, tc.from, tc.to)
			if got != tc.want {
				t.Fatalf("unexpected transition result: %s -> %s, got=%v want=%v", tc.from, tc.to, got, tc.want)
			}
		})
	}

	if !TaskRunOrchestrationTable.IsTerminal(contracts.TaskCanceled) {
		t.Fatalf("canceled should be terminal")
	}
	if !TaskRunOrchestrationTable.IsTerminal(contracts.TaskSucceeded) || !TaskRunOrchestrationTable.IsTerminal(contracts.TaskFailed) {
		t.Fatalf("succeeded/failed should be terminal in this table")
	}
}
