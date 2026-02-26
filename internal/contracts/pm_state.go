package contracts

import "time"

// PMState 是 PM 运行时全局状态。
type PMState struct {
	ID        uint      `gorm:"primaryKey"`
	CreatedAt time.Time `gorm:"not null"`
	UpdatedAt time.Time `gorm:"not null"`

	AutopilotEnabled  bool `gorm:"not null;default:true"`
	MaxRunningWorkers int  `gorm:"not null;default:3"`

	LastTickAt  *time.Time `gorm:""`
	LastEventID uint       `gorm:"not null;default:0"`

	LastRecoveryAt           *time.Time `gorm:""`
	LastRecoveryDispatchJobs int        `gorm:"not null;default:0"`
	LastRecoveryTaskRuns     int        `gorm:"not null;default:0"`
	LastRecoveryNotes        int        `gorm:"not null;default:0"`
	LastRecoveryWorkers      int        `gorm:"not null;default:0"`
}

func (PMState) TableName() string {
	return "pm_states"
}
