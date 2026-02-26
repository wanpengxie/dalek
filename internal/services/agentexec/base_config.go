package agentexec

import (
	"dalek/internal/contracts"
	"dalek/internal/services/core"
	"strings"
)

// BaseConfig 收敛 executor 共享的任务跟踪与执行环境字段。
type BaseConfig struct {
	Runtime core.TaskRuntime

	OwnerType contracts.TaskOwnerType
	TaskType  string

	ProjectKey  string
	TicketID    uint
	WorkerID    uint
	SubjectType string
	SubjectID   string

	WorkDir string
	Env     map[string]string
}

func (c BaseConfig) createRunInput(payload string) contracts.TaskRunCreateInput {
	return contracts.TaskRunCreateInput{
		OwnerType:          c.OwnerType,
		TaskType:           strings.TrimSpace(c.TaskType),
		ProjectKey:         strings.TrimSpace(c.ProjectKey),
		TicketID:           c.TicketID,
		WorkerID:           c.WorkerID,
		SubjectType:        strings.TrimSpace(c.SubjectType),
		SubjectID:          strings.TrimSpace(c.SubjectID),
		RequestID:          newRequestID("arun"),
		OrchestrationState: contracts.TaskPending,
		RequestPayloadJSON: strings.TrimSpace(payload),
	}
}
