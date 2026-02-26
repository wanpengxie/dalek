package contracts

import "time"

// SubagentRun 是子代理运行记录。
type SubagentRun struct {
	ID        uint      `gorm:"primaryKey"`
	CreatedAt time.Time `gorm:"not null"`
	UpdatedAt time.Time `gorm:"not null"`

	ProjectKey string `gorm:"type:text;not null;index;uniqueIndex:idx_subagent_runs_project_request,priority:1"`
	TaskRunID  uint   `gorm:"not null;index;uniqueIndex"`

	Provider string `gorm:"type:text;not null;default:''"`
	Model    string `gorm:"type:text;not null;default:''"`
	Prompt   string `gorm:"type:text;not null;default:''"`
	CWD      string `gorm:"type:text;not null;default:''"`

	RuntimeDir string `gorm:"type:text;not null;default:''"`
	RequestID  string `gorm:"type:text;not null;default:'';index;uniqueIndex:idx_subagent_runs_project_request,priority:2"`
}

func (SubagentRun) TableName() string {
	return "subagent_runs"
}
