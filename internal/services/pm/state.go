package pm

import (
	"context"
	"fmt"
	"time"

	"dalek/internal/contracts"

	"gorm.io/gorm"
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
		AutopilotEnabled:  true,
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
	return *st, nil
}

func (s *Service) markPlannerDirty(st *contracts.PMState) {
	if st == nil {
		return
	}
	st.PlannerDirty = true
	st.PlannerWakeVersion++
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
