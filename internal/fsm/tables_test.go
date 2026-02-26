package fsm

import (
	"testing"

	"dalek/internal/store"
)

func TestDomainTablesValidate(t *testing.T) {
	tables := []struct {
		name  string
		check func() error
	}{
		{name: "ticket_workflow", check: TicketWorkflowTable.Validate},
		{name: "worker_lifecycle", check: WorkerLifecycleTable.Validate},
		{name: "pm_dispatch_job", check: PMDispatchJobTable.Validate},
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
		from store.TicketWorkflowStatus
		to   store.TicketWorkflowStatus
		want bool
	}{
		{name: "backlog_to_queued", from: store.TicketBacklog, to: store.TicketQueued, want: true},
		{name: "queued_to_active", from: store.TicketQueued, to: store.TicketActive, want: true},
		{name: "active_to_done", from: store.TicketActive, to: store.TicketDone, want: true},
		{name: "done_to_archived", from: store.TicketDone, to: store.TicketArchived, want: true},
		{name: "done_to_active_forbidden", from: store.TicketDone, to: store.TicketActive, want: false},
		{name: "archived_to_queued_forbidden", from: store.TicketArchived, to: store.TicketQueued, want: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := CanTransition(TicketWorkflowTable, tc.from, tc.to)
			if got != tc.want {
				t.Fatalf("unexpected transition result: %s -> %s, got=%v want=%v", tc.from, tc.to, got, tc.want)
			}
		})
	}

	if !TicketWorkflowTable.IsTerminal(store.TicketArchived) {
		t.Fatalf("archived should be terminal")
	}
	if TicketWorkflowTable.IsTerminal(store.TicketDone) {
		t.Fatalf("done should not be terminal")
	}

	valid := ValidTransitions(TicketWorkflowTable, store.TicketQueued)
	if len(valid) != 3 || valid[0] != store.TicketActive || valid[1] != store.TicketBlocked || valid[2] != store.TicketArchived {
		t.Fatalf("unexpected valid transitions for queued: %v", valid)
	}
}

func TestWorkerLifecycleTable(t *testing.T) {
	tests := []struct {
		name string
		from store.WorkerStatus
		to   store.WorkerStatus
		want bool
	}{
		{name: "stopped_to_creating", from: store.WorkerStopped, to: store.WorkerCreating, want: true},
		{name: "creating_to_running", from: store.WorkerCreating, to: store.WorkerRunning, want: true},
		{name: "running_to_failed", from: store.WorkerRunning, to: store.WorkerFailed, want: true},
		{name: "failed_to_creating", from: store.WorkerFailed, to: store.WorkerCreating, want: true},
		{name: "creating_to_stopped_forbidden", from: store.WorkerCreating, to: store.WorkerStopped, want: false},
		{name: "running_to_creating_forbidden", from: store.WorkerRunning, to: store.WorkerCreating, want: false},
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

func TestPMDispatchJobTable(t *testing.T) {
	tests := []struct {
		name string
		from store.PMDispatchJobStatus
		to   store.PMDispatchJobStatus
		want bool
	}{
		{name: "pending_to_running", from: store.PMDispatchPending, to: store.PMDispatchRunning, want: true},
		{name: "pending_to_failed", from: store.PMDispatchPending, to: store.PMDispatchFailed, want: true},
		{name: "running_to_succeeded", from: store.PMDispatchRunning, to: store.PMDispatchSucceeded, want: true},
		{name: "running_to_failed", from: store.PMDispatchRunning, to: store.PMDispatchFailed, want: true},
		{name: "succeeded_to_running_forbidden", from: store.PMDispatchSucceeded, to: store.PMDispatchRunning, want: false},
		{name: "failed_to_pending_forbidden", from: store.PMDispatchFailed, to: store.PMDispatchPending, want: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := CanTransition(PMDispatchJobTable, tc.from, tc.to)
			if got != tc.want {
				t.Fatalf("unexpected transition result: %s -> %s, got=%v want=%v", tc.from, tc.to, got, tc.want)
			}
		})
	}

	if !PMDispatchJobTable.IsTerminal(store.PMDispatchSucceeded) || !PMDispatchJobTable.IsTerminal(store.PMDispatchFailed) {
		t.Fatalf("succeeded/failed should be terminal")
	}
}

func TestTaskRunOrchestrationTable(t *testing.T) {
	tests := []struct {
		name string
		from store.TaskOrchestrationState
		to   store.TaskOrchestrationState
		want bool
	}{
		{name: "pending_to_running", from: store.TaskPending, to: store.TaskRunning, want: true},
		{name: "pending_to_failed", from: store.TaskPending, to: store.TaskFailed, want: true},
		{name: "pending_to_canceled", from: store.TaskPending, to: store.TaskCanceled, want: true},
		{name: "running_to_succeeded", from: store.TaskRunning, to: store.TaskSucceeded, want: true},
		{name: "running_to_failed", from: store.TaskRunning, to: store.TaskFailed, want: true},
		{name: "running_to_canceled", from: store.TaskRunning, to: store.TaskCanceled, want: true},
		{name: "succeeded_to_canceled", from: store.TaskSucceeded, to: store.TaskCanceled, want: true},
		{name: "failed_to_canceled", from: store.TaskFailed, to: store.TaskCanceled, want: true},
		{name: "canceled_to_running_forbidden", from: store.TaskCanceled, to: store.TaskRunning, want: false},
		{name: "pending_to_succeeded_forbidden", from: store.TaskPending, to: store.TaskSucceeded, want: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := CanTransition(TaskRunOrchestrationTable, tc.from, tc.to)
			if got != tc.want {
				t.Fatalf("unexpected transition result: %s -> %s, got=%v want=%v", tc.from, tc.to, got, tc.want)
			}
		})
	}

	if !TaskRunOrchestrationTable.IsTerminal(store.TaskCanceled) {
		t.Fatalf("canceled should be terminal")
	}
	if TaskRunOrchestrationTable.IsTerminal(store.TaskSucceeded) || TaskRunOrchestrationTable.IsTerminal(store.TaskFailed) {
		t.Fatalf("succeeded/failed should not be terminal in this table")
	}
}
