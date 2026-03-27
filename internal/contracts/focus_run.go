package contracts

import (
	"time"

	"gorm.io/gorm"
)

// FocusRun 是 daemon-owned focus 控制面的持久化运行态。
type FocusRun struct {
	gorm.Model
	ProjectKey string `gorm:"index;not null" json:"project_key"`

	Mode         string `gorm:"type:varchar(16);not null" json:"mode"`
	RequestID    string `gorm:"type:text;not null;default:'';index" json:"request_id"`
	DesiredState string `gorm:"column:desired_state;type:varchar(16);not null;default:'running'" json:"desired_state"`
	Status       string `gorm:"type:varchar(16);not null;index" json:"status"`

	ScopeTicketIDs string `gorm:"type:text;not null;default:'[]'" json:"scope_ticket_ids"`

	AgentBudget    int `json:"agent_budget"`
	AgentBudgetMax int `json:"agent_budget_max"`

	// convergent 模式扩展字段
	MaxPMRuns       int    `gorm:"default:5" json:"max_pm_runs"`
	PMRunCount      int    `gorm:"default:0" json:"pm_run_count"`
	ConvergentPhase string `gorm:"type:varchar(32);default:''" json:"convergent_phase"` // "batch" | "pm_run" | ""
	ReviewScope     string `gorm:"type:text;default:''" json:"review_scope"`            // review-first 模式的审查范围描述

	StartedAt  *time.Time `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at"`

	// 以下字段只为兼容旧 focus v0 的未清理代码路径而保留，不再参与数据库映射。
	ActiveSeq      *int   `gorm:"-" json:"-"`
	ActiveTicketID *uint  `gorm:"-" json:"-"`
	CompletedCount int    `gorm:"-" json:"-"`
	TotalCount     int    `gorm:"-" json:"-"`
	Summary        string `gorm:"-" json:"-"`
	LastError      string `gorm:"-" json:"-"`
}

func (FocusRun) TableName() string { return "focus_runs" }

// FocusRunItem 是 focus run 中按顺序推进的最小 item。
type FocusRunItem struct {
	gorm.Model
	FocusRunID uint `gorm:"not null;uniqueIndex:idx_focus_run_items_run_seq" json:"focus_run_id"`
	Seq        int  `gorm:"not null;uniqueIndex:idx_focus_run_items_run_seq" json:"seq"`
	TicketID   uint `gorm:"not null;index" json:"ticket_id"`

	Status string `gorm:"type:varchar(32);not null;index" json:"status"`

	CurrentAttempt   int   `json:"current_attempt"`
	CurrentWorkerID  *uint `gorm:"index" json:"current_worker_id,omitempty"`
	CurrentTaskRunID *uint `gorm:"index" json:"current_task_run_id,omitempty"`
	HandoffTicketID  *uint `gorm:"index" json:"handoff_ticket_id,omitempty"`

	BlockedReason string `gorm:"type:text;not null;default:''" json:"blocked_reason"`
	LastError     string `gorm:"type:text;not null;default:''" json:"last_error"`

	StartedAt  *time.Time `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at"`
}

func (FocusRunItem) TableName() string { return "focus_run_items" }

// FocusEvent 是 focus 控制面的 append-only 审计事件。
type FocusEvent struct {
	ID          uint      `gorm:"primaryKey" json:"id"`
	FocusRunID  uint      `gorm:"not null;index" json:"focus_run_id"`
	FocusItemID *uint     `gorm:"index" json:"focus_item_id,omitempty"`
	Kind        string    `gorm:"type:varchar(64);not null;index" json:"kind"`
	Summary     string    `gorm:"type:text;not null;default:''" json:"summary"`
	PayloadJSON string    `gorm:"type:text;not null;default:'{}'" json:"payload_json"`
	CreatedAt   time.Time `gorm:"not null;index" json:"created_at"`
}

func (FocusEvent) TableName() string { return "focus_events" }

type FocusStartInput struct {
	Mode           string `json:"mode"`
	ScopeTicketIDs []uint `json:"scope_ticket_ids"`
	AgentBudget    int    `json:"agent_budget"`
	RequestID      string `json:"request_id"`

	// convergent 专属
	MaxPMRuns   int    `json:"max_pm_runs,omitempty"`   // 默认 5, 上限 10
	ReviewScope string `json:"review_scope,omitempty"` // review-first 模式：跳过 batch，直接进入 PM review
}

type FocusStartResult struct {
	Created   bool         `json:"created"`
	FocusID   uint         `json:"focus_id"`
	RequestID string       `json:"request_id"`
	View      FocusRunView `json:"view"`
}

type FocusRunView struct {
	Run           FocusRun       `json:"run"`
	Items         []FocusRunItem `json:"items"`
	ActiveItem    *FocusRunItem  `json:"active_item,omitempty"`
	LatestEventID uint           `json:"latest_event_id"`
	ReadonlyStale bool           `json:"readonly_stale,omitempty"`

	// convergent 模式扩展（batch 模式下为 nil / empty）
	LatestRound *ConvergentRound  `json:"latest_round,omitempty"`
	Rounds      []ConvergentRound `json:"rounds,omitempty"`
}

type FocusPollResult struct {
	View   FocusRunView `json:"view"`
	Events []FocusEvent `json:"events"`
}

type FocusAddTicketsInput struct {
	TicketIDs []uint `json:"ticket_ids"`
	RequestID string `json:"request_id"`
}

type FocusAddTicketsResult struct {
	FocusID      uint         `json:"focus_id"`
	AddedCount   int          `json:"added_count"`
	SkippedCount int          `json:"skipped_count"`
	AddedIDs     []uint       `json:"added_ids"`
	SkippedIDs   []uint       `json:"skipped_ids"`
	View         FocusRunView `json:"view"`
}

const (
	FocusModeBatch      = "batch"
	FocusModePlan       = "plan"
	FocusModeConvergent = "convergent"
)

const (
	FocusDesiredRunning   = "running"
	FocusDesiredStopping  = "stopping"
	FocusDesiredCanceling = "canceling"
)

const (
	FocusQueued    = "queued"
	FocusRunning   = "running"
	FocusBlocked   = "blocked"
	FocusCompleted = "completed"
	FocusStopped   = "stopped"
	FocusFailed    = "failed"
	FocusCanceled  = "canceled"
	FocusConverged = "converged"
	FocusExhausted = "exhausted"
)

const (
	FocusItemPending                  = "pending"
	FocusItemQueued                   = "queued"
	FocusItemExecuting                = "executing"
	FocusItemMerging                  = "merging"
	FocusItemAwaitingMergeObservation = "awaiting_merge_observation"
	FocusItemBlocked                  = "blocked"
	FocusItemCompleted                = "completed"
	FocusItemStopped                  = "stopped"
	FocusItemFailed                   = "failed"
	FocusItemCanceled                 = "canceled"
)

const (
	FocusEventRunCreated             = "run.created"
	FocusEventRunDesiredStateChanged = "run.desired_state_changed"
	FocusEventItemSelected           = "item.selected"
	FocusEventItemStartRequested     = "item.start_requested"
	FocusEventItemAdopted            = "item.adopted"
	FocusEventInboxReplyAccepted     = "item.reply_received"
	FocusEventItemRestarted          = "item.restarted"
	FocusEventItemBlocked            = "item.blocked"
	FocusEventItemCompleted          = "item.completed"
	FocusEventMergeStarted           = "merge.started"
	FocusEventMergeAborted           = "merge.aborted"
	FocusEventMergeObserved          = "merge.observed"
	FocusEventIntegrationCreated     = "integration_ticket.created"
	FocusEventHandoffResolved        = "handoff.resolved"
	FocusEventScopeTicketsAdded      = "scope.tickets_added"

	// convergent 模式事件
	FocusEventConvergentRoundStarted = "convergent.round_started"
	FocusEventConvergentBatchDone    = "convergent.batch_done"
	FocusEventConvergentPMRunStarted = "convergent.pm_run_started"
	FocusEventConvergentPMRunDone    = "convergent.pm_run_done"
	FocusEventConvergentFixCreated   = "convergent.fix_created"
	FocusEventConvergentConverged    = "convergent.converged"
	FocusEventConvergentExhausted    = "convergent.exhausted"
)

// IsTerminal 判断 focus run 是否已终结。
func (f FocusRun) IsTerminal() bool {
	switch f.Status {
	case FocusCompleted, FocusStopped, FocusFailed, FocusCanceled, FocusConverged, FocusExhausted:
		return true
	}
	return false
}

func (f FocusRun) IsActive() bool {
	switch f.Status {
	case FocusQueued, FocusRunning, FocusBlocked:
		return true
	}
	return false
}
