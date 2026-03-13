package pm

import (
	"context"
	"fmt"
	"strings"
	"time"

	"dalek/internal/contracts"

	"gorm.io/gorm"
)

const (
	plannerRunSuccessCooldown = 30 * time.Second
	plannerRunFailureCooldown = 60 * time.Second
)

func (s *Service) getOrInitPMState(ctx context.Context) (*contracts.PMState, error) {
	_, db, err := s.require()
	if err != nil {
		return nil, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	var st contracts.PMState
	err = db.WithContext(ctx).Order("id asc").First(&st).Error
	if err == nil {
		if st.MaxRunningWorkers <= 0 {
			st.MaxRunningWorkers = 3
			_ = db.WithContext(ctx).Save(&st).Error
		}
		return &st, nil
	}
	if err != gorm.ErrRecordNotFound {
		return nil, err
	}
	st = contracts.PMState{
		AutopilotEnabled:  false,
		MaxRunningWorkers: 3,
		LastTickAt:        nil,
		LastEventID:       0,

		PlannerDirty:           false,
		PlannerWakeVersion:     0,
		PlannerActiveTaskRunID: nil,
		PlannerCooldownUntil:   nil,
		PlannerLastError:       "",
		PlannerLastRunAt:       nil,
	}
	if err := db.WithContext(ctx).Create(&st).Error; err != nil {
		return nil, err
	}
	return &st, nil
}

func (s *Service) GetState(ctx context.Context) (contracts.PMState, error) {
	st, err := s.getOrInitPMState(ctx)
	if err != nil {
		return contracts.PMState{}, err
	}
	return *st, nil
}

func (s *Service) SetAutopilotEnabled(ctx context.Context, enabled bool) (contracts.PMState, error) {
	st, err := s.getOrInitPMState(ctx)
	if err != nil {
		return contracts.PMState{}, err
	}
	if st.AutopilotEnabled == enabled {
		return *st, nil
	}
	st.AutopilotEnabled = enabled
	st.UpdatedAt = time.Now()
	_, db, _ := s.require()
	if err := db.WithContext(ctx).Save(st).Error; err != nil {
		return contracts.PMState{}, err
	}
	return *st, nil
}

func (s *Service) SetMaxRunningWorkers(ctx context.Context, n int) (contracts.PMState, error) {
	st, err := s.getOrInitPMState(ctx)
	if err != nil {
		return contracts.PMState{}, err
	}
	n = clampMaxRunning(n)
	if st.MaxRunningWorkers == n {
		return *st, nil
	}
	st.MaxRunningWorkers = n
	st.UpdatedAt = time.Now()
	_, db, _ := s.require()
	if err := db.WithContext(ctx).Save(st).Error; err != nil {
		return contracts.PMState{}, err
	}
	s.KickQueueConsumer()
	return *st, nil
}

func (s *Service) persistPlannerState(ctx context.Context, db *gorm.DB, st *contracts.PMState) error {
	if st == nil || st.ID == 0 || db == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	now := time.Now()
	return db.WithContext(ctx).Model(&contracts.PMState{}).
		Where("id = ?", st.ID).
		Updates(map[string]any{
			"planner_dirty":              st.PlannerDirty,
			"planner_wake_version":       st.PlannerWakeVersion,
			"planner_active_task_run_id": st.PlannerActiveTaskRunID,
			"planner_cooldown_until":     st.PlannerCooldownUntil,
			"planner_last_error":         strings.TrimSpace(st.PlannerLastError),
			"planner_last_run_at":        st.PlannerLastRunAt,
			"updated_at":                 now,
		}).Error
}

func (s *Service) markPlannerDirty(st *contracts.PMState) {
	if st == nil {
		return
	}
	dirtyBefore := st.PlannerDirty
	wakeVersionBefore := st.PlannerWakeVersion
	st.PlannerDirty = true
	st.PlannerWakeVersion++
	s.slog().Debug("pm planner marked dirty",
		"dirty_before", dirtyBefore,
		"dirty_after", st.PlannerDirty,
		"wake_version_before", wakeVersionBefore,
		"wake_version_after", st.PlannerWakeVersion,
	)
}

func (s *Service) clearPlannerRun(st *contracts.PMState, now time.Time) {
	if st == nil {
		return
	}
	if now.IsZero() {
		now = time.Now()
	}
	cooldown := now.Add(plannerRunSuccessCooldown)
	st.PlannerActiveTaskRunID = nil
	st.PlannerLastError = ""
	st.PlannerLastRunAt = &now
	st.PlannerCooldownUntil = &cooldown
}

func (s *Service) failPlannerRun(st *contracts.PMState, now time.Time, errMsg string) {
	if st == nil {
		return
	}
	if now.IsZero() {
		now = time.Now()
	}
	cooldown := now.Add(plannerRunFailureCooldown)
	msg := strings.TrimSpace(errMsg)
	if msg == "" {
		msg = "planner run failed"
	}
	st.PlannerActiveTaskRunID = nil
	st.PlannerDirty = true
	st.PlannerLastError = msg
	st.PlannerLastRunAt = &now
	st.PlannerCooldownUntil = &cooldown
}

func clampMaxRunning(n int) int {
	if n <= 0 {
		return 3
	}
	if n > 32 {
		return 32
	}
	return n
}

func (s *Service) requireCtx(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

func (s *Service) ensureWorkerService() error {
	if s == nil || s.worker == nil {
		return fmt.Errorf("pm service 缺少 worker service")
	}
	return nil
}
