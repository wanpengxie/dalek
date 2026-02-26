package contracts

import (
	"strings"
	"time"
)

// TaskRunCreateInput 定义创建 task run 的跨层输入。
type TaskRunCreateInput struct {
	OwnerType TaskOwnerType
	TaskType  string

	ProjectKey  string
	TicketID    uint
	WorkerID    uint
	SubjectType string
	SubjectID   string

	RequestID string

	OrchestrationState TaskOrchestrationState
	RunnerID           string
	LeaseExpiresAt     *time.Time
	Attempt            int

	RequestPayloadJSON string
	ResultPayloadJSON  string

	ErrorCode    string
	ErrorMessage string

	StartedAt  *time.Time
	FinishedAt *time.Time
}

// TaskEventInput 定义 task 事件写入输入。
type TaskEventInput struct {
	TaskRunID uint
	EventType string
	FromState any
	ToState   any
	Note      string
	Payload   any
	CreatedAt time.Time
}

// TaskRuntimeSampleInput 定义 runtime 采样输入。
type TaskRuntimeSampleInput struct {
	TaskRunID  uint
	State      TaskRuntimeHealthState
	NeedsUser  bool
	Summary    string
	Source     string
	ObservedAt time.Time
	Metrics    any
}

// TaskSemanticReportInput 定义语义报告输入。
type TaskSemanticReportInput struct {
	TaskRunID  uint
	Phase      TaskSemanticPhase
	Milestone  string
	NextAction string
	Summary    string
	ReportedAt time.Time
	Payload    any
}

// TaskListStatusOptions 定义 task 状态查询选项。
type TaskListStatusOptions struct {
	OwnerType       TaskOwnerType
	TaskType        string
	TicketID        uint
	WorkerID        uint
	IncludeTerminal bool
	Limit           int
}

// TaskEventScopeRow 定义带 scope 信息的事件行。
type TaskEventScopeRow struct {
	TaskEvent
	TicketID  uint
	WorkerID  uint
	OwnerType string
	TaskType  string
}

var nextActionSemanticPhaseTable = map[string]TaskSemanticPhase{
	string(NextDone):     TaskPhaseDone,
	string(NextWaitUser): TaskPhaseBlocked,
	string(NextContinue): TaskPhaseImplementing,
}

// NextActionToSemanticPhase 将 next_action 映射到语义阶段。
func NextActionToSemanticPhase(nextAction string) TaskSemanticPhase {
	normalized := strings.TrimSpace(strings.ToLower(nextAction))
	if phase, ok := nextActionSemanticPhaseTable[normalized]; ok {
		return phase
	}
	return TaskPhaseImplementing
}
