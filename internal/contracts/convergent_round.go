package contracts

import (
	"time"

	"gorm.io/gorm"
)

// ConvergentRound 跟踪 convergent 模式每轮 batch + pm_run 的执行记录。
type ConvergentRound struct {
	gorm.Model
	FocusRunID  uint `gorm:"not null;index" json:"focus_run_id"`
	RoundNumber int  `gorm:"not null" json:"round_number"` // 1-based

	// batch 阶段
	BatchTicketIDs string `gorm:"type:text;not null;default:'[]'" json:"batch_ticket_ids"`
	BatchStatus    string `gorm:"type:varchar(16)" json:"batch_status"`
	// pending | running | completed | failed | blocked | canceled

	// pm_run 阶段
	PMRunTaskRunID *uint  `json:"pm_run_task_run_id,omitempty"` // subagent task_run_id
	PMRunStatus    string `gorm:"type:varchar(16)" json:"pm_run_status"`
	// pending | running | done | failed | canceled

	// 结果
	Verdict      string `gorm:"type:varchar(16)" json:"verdict"`
	FixTicketIDs string `gorm:"type:text;not null;default:'[]'" json:"fix_ticket_ids"`
	ReviewPath   string `gorm:"type:text" json:"review_path"`

	StartedAt  *time.Time `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at"`
}

func (ConvergentRound) TableName() string { return "convergent_rounds" }
