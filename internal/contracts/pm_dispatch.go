package contracts

import (
	"fmt"
	"strings"
	"time"
)

const PMDispatchJobResultSchemaV1 = "dalek.pm_dispatch_job_result.v1"

// PMDispatchJob 是 PM 调度任务队列模型。
type PMDispatchJob struct {
	ID        uint      `gorm:"primaryKey"`
	CreatedAt time.Time `gorm:"not null"`
	UpdatedAt time.Time `gorm:"not null"`

	RequestID string `gorm:"type:text;not null;uniqueIndex"`

	TicketID  uint `gorm:"not null;index"`
	WorkerID  uint `gorm:"not null;index"`
	TaskRunID uint `gorm:"not null;default:0;index"`
	// 同 ticket 同时最多一个 active dispatch job（pending/running）。
	ActiveTicketKey *uint `gorm:"uniqueIndex"`

	Status PMDispatchJobStatus `gorm:"type:text;not null;index"`

	RunnerID       string     `gorm:"type:text;not null;default:''"`
	LeaseExpiresAt *time.Time `gorm:""`
	Attempt        int        `gorm:"not null;default:0"`

	ResultJSON string `gorm:"type:text;not null;default:''"`
	Error      string `gorm:"type:text;not null;default:''"`

	StartedAt  *time.Time `gorm:""`
	FinishedAt *time.Time `gorm:""`
}

func (PMDispatchJob) TableName() string {
	return "pm_dispatch_jobs"
}

// PMDispatchJobResult 是 PM dispatch runner 的“最小审计输出”，会被写入 DB（ResultJSON）。
//
// 说明：
// - 该结构不用于 Go 解释策略，只用于 Go 侧最小结果记录与展示。
type PMDispatchJobResult struct {
	Schema      string `json:"schema"`
	InjectedCmd string `json:"injected_cmd"`

	// Worker loop 同步执行的结果字段（sdk 模式下有值）
	WorkerLoopStages     int    `json:"worker_loop_stages,omitempty"`
	WorkerLoopNextAction string `json:"worker_loop_next_action,omitempty"`
}

func (r PMDispatchJobResult) Validate() error {
	if strings.TrimSpace(r.Schema) != PMDispatchJobResultSchemaV1 {
		return fmt.Errorf("pm_dispatch_job_result schema 非法: %s", strings.TrimSpace(r.Schema))
	}
	return nil
}
