package contracts

import (
	"time"

	"gorm.io/gorm"
)

// FocusRun 是 PM 控制面的唯一运行态：一次临时的、有界的 ticket 批次执行上下文。
// 当前最多一个 active（非终态）focus run per project。
type FocusRun struct {
	gorm.Model
	ProjectKey string `gorm:"index;not null"`

	// Mode: "batch" (后续扩展 "plan")
	Mode string `gorm:"type:varchar(16);not null"`
	// Status: queued | running | blocked | completed | failed | canceled
	Status string `gorm:"type:varchar(16);not null"`

	// Scope
	ScopeTicketIDs string `gorm:"type:text"` // JSON array: [1,2,3]
	ActiveTicketID *uint
	CompletedCount int
	TotalCount     int

	// PM agent 预算
	AgentBudget    int // 剩余 PM agent 调用次数
	AgentBudgetMax int // 初始预算

	// 生命周期
	Summary    string     `gorm:"type:text"`
	StartedAt  *time.Time
	FinishedAt *time.Time
}

func (FocusRun) TableName() string { return "focus_runs" }

// FocusRun 模式
const (
	FocusModeBatch = "batch"
	FocusModePlan  = "plan"
)

// FocusRun 状态
const (
	FocusQueued    = "queued"
	FocusRunning   = "running"
	FocusBlocked   = "blocked"
	FocusCompleted = "completed"
	FocusFailed    = "failed"
	FocusCanceled  = "canceled"
)

// IsTerminal 判断 focus run 是否已终结。
func (f FocusRun) IsTerminal() bool {
	switch f.Status {
	case FocusCompleted, FocusFailed, FocusCanceled:
		return true
	}
	return false
}
