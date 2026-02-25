package task

import (
	"encoding/json"
	"strings"

	"dalek/internal/store"
)

func NextActionToSemanticPhase(nextAction string) store.TaskSemanticPhase {
	switch strings.TrimSpace(strings.ToLower(nextAction)) {
	case "done":
		return store.TaskPhaseDone
	case "wait_user":
		return store.TaskPhaseBlocked
	case "continue":
		return store.TaskPhaseImplementing
	default:
		return store.TaskPhaseImplementing
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

func validOwnerType(v store.TaskOwnerType) bool {
	switch v {
	case store.TaskOwnerWorker, store.TaskOwnerPM, store.TaskOwnerSubagent:
		return true
	default:
		return false
	}
}

func validOrchestrationState(v store.TaskOrchestrationState) bool {
	switch v {
	case store.TaskPending, store.TaskRunning, store.TaskSucceeded, store.TaskFailed, store.TaskCanceled:
		return true
	default:
		return false
	}
}

func validHealthState(v store.TaskRuntimeHealthState) bool {
	switch v {
	case store.TaskHealthUnknown, store.TaskHealthAlive, store.TaskHealthIdle, store.TaskHealthBusy, store.TaskHealthStalled, store.TaskHealthWaitingUser, store.TaskHealthDead:
		return true
	default:
		return false
	}
}

func validSemanticPhase(v store.TaskSemanticPhase) bool {
	switch v {
	case store.TaskPhaseInit, store.TaskPhasePlanning, store.TaskPhaseImplementing, store.TaskPhaseTesting, store.TaskPhaseReviewing, store.TaskPhaseDone, store.TaskPhaseBlocked:
		return true
	default:
		return false
	}
}
