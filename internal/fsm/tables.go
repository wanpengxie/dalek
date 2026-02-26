package fsm

import "dalek/internal/store"

var TicketWorkflowTable = TransitionTable[store.TicketWorkflowStatus]{
	Name:          "ticket_workflow",
	InitialStates: []store.TicketWorkflowStatus{store.TicketBacklog},
	TerminalStates: []store.TicketWorkflowStatus{
		store.TicketArchived,
	},
	Transitions: map[store.TicketWorkflowStatus][]store.TicketWorkflowStatus{
		store.TicketBacklog: {
			store.TicketQueued,
			store.TicketArchived,
		},
		store.TicketQueued: {
			store.TicketActive,
			store.TicketBlocked,
			store.TicketArchived,
		},
		store.TicketActive: {
			store.TicketDone,
			store.TicketBlocked,
			store.TicketArchived,
		},
		store.TicketBlocked: {
			store.TicketActive,
			store.TicketArchived,
		},
		store.TicketDone: {
			store.TicketArchived,
		},
		store.TicketArchived: {},
	},
}

var WorkerLifecycleTable = TransitionTable[store.WorkerStatus]{
	Name:          "worker_lifecycle",
	InitialStates: []store.WorkerStatus{store.WorkerStopped},
	Transitions: map[store.WorkerStatus][]store.WorkerStatus{
		store.WorkerStopped: {
			store.WorkerCreating,
		},
		store.WorkerCreating: {
			store.WorkerRunning,
			store.WorkerFailed,
		},
		store.WorkerRunning: {
			store.WorkerStopped,
			store.WorkerFailed,
		},
		store.WorkerFailed: {
			store.WorkerCreating,
		},
	},
}

var PMDispatchJobTable = TransitionTable[store.PMDispatchJobStatus]{
	Name:          "pm_dispatch_job",
	InitialStates: []store.PMDispatchJobStatus{store.PMDispatchPending},
	TerminalStates: []store.PMDispatchJobStatus{
		store.PMDispatchSucceeded,
		store.PMDispatchFailed,
	},
	Transitions: map[store.PMDispatchJobStatus][]store.PMDispatchJobStatus{
		store.PMDispatchPending: {
			store.PMDispatchRunning,
			store.PMDispatchFailed,
		},
		store.PMDispatchRunning: {
			store.PMDispatchSucceeded,
			store.PMDispatchFailed,
		},
		store.PMDispatchSucceeded: {},
		store.PMDispatchFailed:    {},
	},
}

var TaskRunOrchestrationTable = TransitionTable[store.TaskOrchestrationState]{
	Name:          "task_run_orchestration",
	InitialStates: []store.TaskOrchestrationState{store.TaskPending},
	TerminalStates: []store.TaskOrchestrationState{
		store.TaskCanceled,
	},
	Transitions: map[store.TaskOrchestrationState][]store.TaskOrchestrationState{
		store.TaskPending: {
			store.TaskRunning,
			store.TaskFailed,
			store.TaskCanceled,
		},
		store.TaskRunning: {
			store.TaskSucceeded,
			store.TaskFailed,
			store.TaskCanceled,
		},
		store.TaskSucceeded: {
			store.TaskCanceled,
		},
		store.TaskFailed: {
			store.TaskCanceled,
		},
		store.TaskCanceled: {},
	},
}

func CanTicketWorkflowTransition(from, to store.TicketWorkflowStatus) bool {
	return TicketWorkflowTable.CanTransition(from, to)
}

func CanWorkerLifecycleTransition(from, to store.WorkerStatus) bool {
	return WorkerLifecycleTable.CanTransition(from, to)
}

func CanPMDispatchJobTransition(from, to store.PMDispatchJobStatus) bool {
	return PMDispatchJobTable.CanTransition(from, to)
}

func CanTaskRunTransition(from, to store.TaskOrchestrationState) bool {
	return TaskRunOrchestrationTable.CanTransition(from, to)
}
