package worker

import (
	"context"
	"strings"
	"time"

	"dalek/internal/contracts"
	"dalek/internal/store"

	"gorm.io/gorm"
)

func reportToRuntimeHealth(r contracts.WorkerReport) contracts.TaskRuntimeHealthState {
	switch strings.TrimSpace(strings.ToLower(r.NextAction)) {
	case string(contracts.NextWaitUser):
		return contracts.TaskHealthWaitingUser
	case string(contracts.NextDone), string(contracts.NextContinue):
		return contracts.TaskHealthIdle
	default:
		// 默认：不瞎猜，保持更保守的 idle
		return contracts.TaskHealthIdle
	}
}

// ApplyWorkerReport 把 report 作为高权威信号写回 DB（report 为主，state.json 为辅）。
// 约束：不依赖 state.json；report 缺字段时用“最小可用”的保守策略。
func (s *Service) ApplyWorkerReport(ctx context.Context, r contracts.WorkerReport, source string) error {
	p, err := s.require()
	if err != nil {
		return err
	}
	db := p.DB
	if ctx == nil {
		ctx = context.Background()
	}
	r.Normalize()
	if err := r.Validate(); err != nil {
		return err
	}
	source = strings.TrimSpace(source)
	if source == "" {
		source = "report"
	}

	now := time.Now()
	runtimeHealth := reportToRuntimeHealth(r)
	needs := r.NeedsUser || runtimeHealth == contracts.TaskHealthWaitingUser
	summary := strings.TrimSpace(r.Summary)
	if summary == "" {
		summary = "-"
	}

	var workerSnapshot store.Worker
	if err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var w store.Worker
		if err := tx.WithContext(ctx).First(&w, r.WorkerID).Error; err != nil {
			return err
		}
		workerSnapshot = w
		return nil
	}); err != nil {
		return err
	}

	// task runtime 观测链路属于附加可观测性，不阻塞 report 主写入路径。
	// 使用独立事务确保失败时不会留下半写入的 task run/sample/report/event。
	_ = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		rt, terr := s.taskRuntimeForDB(tx)
		if terr != nil {
			return terr
		}
		return s.syncTaskRuntimeFromReportWithRuntime(ctx, rt, workerSnapshot, r, runtimeHealth, needs, summary, source, now)
	})
	return nil
}
