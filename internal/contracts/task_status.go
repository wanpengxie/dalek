package contracts

type TaskOwnerType string

const (
	TaskOwnerWorker   TaskOwnerType = "worker"
	TaskOwnerPM       TaskOwnerType = "pm"
	TaskOwnerSubagent TaskOwnerType = "subagent"
	TaskOwnerChannel  TaskOwnerType = "channel"
)

const (
	TaskTypeDispatchTicket  = "dispatch_ticket"
	TaskTypeDeliverTicket   = "deliver_ticket"
	TaskTypePMDispatchAgent = "pm_dispatch_agent"
	TaskTypePMPlannerRun    = "pm_planner_run"
	TaskTypeSubagentRun     = "subagent_run"
	TaskTypeChannelTurn     = "channel_turn"
)

type TaskOrchestrationState string

const (
	TaskPending   TaskOrchestrationState = "pending"
	TaskRunning   TaskOrchestrationState = "running"
	TaskSucceeded TaskOrchestrationState = "succeeded"
	TaskFailed    TaskOrchestrationState = "failed"
	TaskCanceled  TaskOrchestrationState = "canceled"
)

type TaskRuntimeHealthState string

const (
	TaskHealthUnknown     TaskRuntimeHealthState = "unknown"
	TaskHealthAlive       TaskRuntimeHealthState = "alive"
	TaskHealthIdle        TaskRuntimeHealthState = "idle"
	TaskHealthBusy        TaskRuntimeHealthState = "busy"
	TaskHealthStalled     TaskRuntimeHealthState = "stalled"
	TaskHealthWaitingUser TaskRuntimeHealthState = "waiting_user"
	TaskHealthDead        TaskRuntimeHealthState = "dead"
)

type TaskSemanticPhase string

const (
	TaskPhaseInit         TaskSemanticPhase = "init"
	TaskPhasePlanning     TaskSemanticPhase = "planning"
	TaskPhaseImplementing TaskSemanticPhase = "implementing"
	TaskPhaseTesting      TaskSemanticPhase = "testing"
	TaskPhaseReviewing    TaskSemanticPhase = "reviewing"
	TaskPhaseDone         TaskSemanticPhase = "done"
	TaskPhaseBlocked      TaskSemanticPhase = "blocked"
)
