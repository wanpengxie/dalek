package task

import (
	"dalek/internal/contracts"

	"dalek/internal/fsm"
)

func toJSONMap(v any) contracts.JSONMap {
	return contracts.JSONMapFromAny(v)
}

func validOwnerType(v contracts.TaskOwnerType) bool {
	switch v {
	case contracts.TaskOwnerWorker, contracts.TaskOwnerPM, contracts.TaskOwnerSubagent, contracts.TaskOwnerChannel:
		return true
	default:
		return false
	}
}

func validOrchestrationState(v contracts.TaskOrchestrationState) bool {
	return fsm.TaskRunOrchestrationTable.IsKnownState(v)
}

func validHealthState(v contracts.TaskRuntimeHealthState) bool {
	switch v {
	case contracts.TaskHealthUnknown, contracts.TaskHealthAlive, contracts.TaskHealthIdle, contracts.TaskHealthBusy, contracts.TaskHealthStalled, contracts.TaskHealthWaitingUser, contracts.TaskHealthDead:
		return true
	default:
		return false
	}
}

func validSemanticPhase(v contracts.TaskSemanticPhase) bool {
	switch v {
	case contracts.TaskPhaseInit, contracts.TaskPhasePlanning, contracts.TaskPhaseImplementing, contracts.TaskPhaseTesting, contracts.TaskPhaseReviewing, contracts.TaskPhaseDone, contracts.TaskPhaseBlocked:
		return true
	default:
		return false
	}
}
