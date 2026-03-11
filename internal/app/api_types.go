package app

import (
	"time"

	"dalek/internal/contracts"
	notebooksvc "dalek/internal/services/notebook"
	pmsvc "dalek/internal/services/pm"
	subagentsvc "dalek/internal/services/subagent"
	tasksvc "dalek/internal/services/task"
	ticketsvc "dalek/internal/services/ticket"
	workersvc "dalek/internal/services/worker"
)

// 说明：
// app 层对外暴露的类型尽量是“稳定 API”。
// notebook 域类型已迁移到 services/notebook，app 层保留兼容别名避免上层调用回退。

type StartOptions = pmsvc.StartOptions
type InterruptResult = workersvc.InterruptResult
type WorktreeCleanupOptions = workersvc.CleanupWorktreeOptions
type WorktreeCleanupResult = workersvc.CleanupWorktreeResult
type TicketView = ticketsvc.TicketView

type ListInboxOptions struct {
	Status contracts.InboxStatus
	Limit  int
}

type ListMergeOptions struct {
	Status contracts.MergeStatus
	Limit  int
}

type ListNoteOptions = notebooksvc.ListNoteOptions
type NoteAddResult = notebooksvc.NoteAddResult
type NoteView = notebooksvc.NoteView
type ShapedView = notebooksvc.ShapedView

type ManagerTickOptions = pmsvc.ManagerTickOptions
type ManagerTickResult = pmsvc.ManagerTickResult
type PlannerRunOptions = pmsvc.PlannerRunOptions
type PMHealthMetricsOptions = pmsvc.HealthMetricsOptions
type PMHealthMetrics = pmsvc.HealthMetrics

type ListTaskOptions struct {
	OwnerType       contracts.TaskOwnerType
	TaskType        string
	TicketID        uint
	WorkerID        uint
	IncludeTerminal bool
	Limit           int
}

type SubagentRun = contracts.SubagentRun

type CreateSubagentRunOptions struct {
	TaskRunID  uint
	RequestID  string
	Provider   string
	Model      string
	Prompt     string
	CWD        string
	RuntimeDir string
}

type SubagentSubmitOptions = subagentsvc.SubmitInput

type SubagentSubmission = subagentsvc.SubmitResult

type SubagentRunOptions = subagentsvc.RunInput

type TaskStatus = contracts.TaskStatusView

type TaskEvent struct {
	ID        uint
	TaskRunID uint
	EventType string

	FromStateJSON string
	ToStateJSON   string
	Note          string
	PayloadJSON   string

	CreatedAt time.Time
}

type TaskCancelResult = tasksvc.CancelRunResult
