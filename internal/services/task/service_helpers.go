package task

import (
	"dalek/internal/contracts"
	"encoding/json"
	"strings"

	"dalek/internal/fsm"
)

func NextActionToSemanticPhase(nextAction string) contracts.TaskSemanticPhase {
	switch strings.TrimSpace(strings.ToLower(nextAction)) {
	case "done":
		return contracts.TaskPhaseDone
	case "wait_user":
		return contracts.TaskPhaseBlocked
	case "continue":
		return contracts.TaskPhaseImplementing
	default:
		return contracts.TaskPhaseImplementing
	}
}

func toJSON(v any) string {
	if v == nil {
		return ""
	}
	switch t := v.(type) {
	case string:
		return strings.TrimSpace(t)
	case []byte:
		return strings.TrimSpace(string(t))
	}
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func validOwnerType(v contracts.TaskOwnerType) bool {
	switch v {
	case contracts.TaskOwnerWorker, contracts.TaskOwnerPM, contracts.TaskOwnerSubagent:
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
