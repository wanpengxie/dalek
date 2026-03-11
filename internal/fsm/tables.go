package fsm

import (
	"dalek/internal/contracts"
)

var TicketWorkflowTable = TransitionTable[contracts.TicketWorkflowStatus]{
	Name:          "ticket_workflow",
	InitialStates: []contracts.TicketWorkflowStatus{contracts.TicketBacklog},
	TerminalStates: []contracts.TicketWorkflowStatus{
		contracts.TicketArchived,
	},
	Transitions: map[contracts.TicketWorkflowStatus][]contracts.TicketWorkflowStatus{
		contracts.TicketBacklog: {
			contracts.TicketActive,
			contracts.TicketQueued,
			contracts.TicketArchived,
		},
		contracts.TicketQueued: {
			contracts.TicketActive,
			contracts.TicketBlocked,
			contracts.TicketArchived,
		},
		contracts.TicketActive: {
			contracts.TicketDone,
			contracts.TicketBlocked,
			contracts.TicketArchived,
		},
		contracts.TicketBlocked: {
			contracts.TicketQueued,
			contracts.TicketActive,
			contracts.TicketArchived,
		},
		contracts.TicketDone: {
			contracts.TicketArchived,
		},
		contracts.TicketArchived: {},
	},
}

var WorkerLifecycleTable = TransitionTable[contracts.WorkerStatus]{
	Name:          "worker_lifecycle",
	InitialStates: []contracts.WorkerStatus{contracts.WorkerStopped},
	Transitions: map[contracts.WorkerStatus][]contracts.WorkerStatus{
		contracts.WorkerStopped: {
			contracts.WorkerCreating,
		},
		contracts.WorkerCreating: {
			contracts.WorkerRunning,
			contracts.WorkerFailed,
		},
		contracts.WorkerRunning: {
			contracts.WorkerStopped,
			contracts.WorkerFailed,
		},
		contracts.WorkerFailed: {
			contracts.WorkerCreating,
		},
	},
}

var TaskRunOrchestrationTable = TransitionTable[contracts.TaskOrchestrationState]{
	Name:          "task_run_orchestration",
	InitialStates: []contracts.TaskOrchestrationState{contracts.TaskPending},
	TerminalStates: []contracts.TaskOrchestrationState{
		contracts.TaskSucceeded,
		contracts.TaskFailed,
		contracts.TaskCanceled,
	},
	Transitions: map[contracts.TaskOrchestrationState][]contracts.TaskOrchestrationState{
		contracts.TaskPending: {
			contracts.TaskRunning,
			contracts.TaskFailed,
			contracts.TaskCanceled,
		},
		contracts.TaskRunning: {
			contracts.TaskSucceeded,
			contracts.TaskFailed,
			contracts.TaskCanceled,
		},
		contracts.TaskSucceeded: {},
		contracts.TaskFailed:    {},
		contracts.TaskCanceled:  {},
	},
}

func CanTicketWorkflowTransition(from, to contracts.TicketWorkflowStatus) bool {
	return TicketWorkflowTable.CanTransition(from, to)
}

func CanWorkerLifecycleTransition(from, to contracts.WorkerStatus) bool {
	return WorkerLifecycleTable.CanTransition(from, to)
}

func CanTaskRunTransition(from, to contracts.TaskOrchestrationState) bool {
	return TaskRunOrchestrationTable.CanTransition(from, to)
}
