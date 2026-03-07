package pm

import (
	"context"
	"fmt"
	"strings"
	"time"

	"dalek/internal/contracts"

	"gorm.io/gorm"
)

type PlannerRunOptions struct {
	RunnerID string
}

func (s *Service) RunPlannerJob(ctx context.Context, taskRunID uint, opt PlannerRunOptions) error {
	if _, _, err := s.require(); err != nil {
		return err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if taskRunID == 0 {
		return fmt.Errorf("task_run_id 不能为空")
	}

	runnerID := strings.TrimSpace(opt.RunnerID)
	if runnerID == "" {
		runnerID = fmt.Sprintf("pm_planner_%d", taskRunID)
	}
	if err := s.completePlannerRunSuccess(ctx, taskRunID, runnerID); err != nil {
		failMsg := strings.TrimSpace(err.Error())
		if failMsg == "" {
			failMsg = "planner run failed"
		}
		if ferr := s.completePlannerRunFailed(context.WithoutCancel(ctx), taskRunID, failMsg); ferr != nil {
			return fmt.Errorf("planner run failed: %w (mark failed also failed: %v)", err, ferr)
		}
		return err
	}
	return nil
}

func (s *Service) completePlannerRunSuccess(ctx context.Context, taskRunID uint, runnerID string) error {
	_, db, err := s.require()
	if err != nil {
		return err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	now := time.Now()
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		taskRuntime, terr := s.taskRuntimeForDB(tx)
		if terr != nil {
			return terr
		}
		run, rerr := taskRuntime.FindRunByID(ctx, taskRunID)
		if rerr != nil {
			return rerr
		}
		if run == nil {
			return fmt.Errorf("planner task run 不存在: run_id=%d", taskRunID)
		}
		if run.OwnerType != contracts.TaskOwnerPM || run.TaskType != contracts.TaskTypePMPlannerRun {
			return fmt.Errorf("task run 类型不匹配: run_id=%d owner=%s type=%s", taskRunID, run.OwnerType, run.TaskType)
		}
		if err := taskRuntime.MarkRunRunning(ctx, taskRunID, runnerID, nil, now, true); err != nil {
			return err
		}
		if err := taskRuntime.AppendEvent(ctx, contracts.TaskEventInput{
			TaskRunID: taskRunID,
			EventType: "task_started",
			ToState: map[string]any{
				"orchestration_state": contracts.TaskRunning,
			},
			Note: "pm planner run started",
		}); err != nil {
			return err
		}

		// TODO: 在后续 ticket 中接入真实 planner prompt 构建与 agent 执行。
		if err := ctx.Err(); err != nil {
			return err
		}

		pmState, serr := s.loadPMStateForUpdateTx(ctx, tx)
		if serr != nil {
			return serr
		}
		if plannerRunMatchesState(pmState, taskRunID) {
			s.clearPlannerRun(pmState, now)
			if err := s.persistPlannerStateTx(ctx, tx, pmState, now); err != nil {
				return err
			}
		}
		if err := taskRuntime.MarkRunSucceeded(ctx, taskRunID, marshalJSON(map[string]any{
			"runner_id": runnerID,
			"mode":      "stub",
		}), now); err != nil {
			return err
		}
		if err := taskRuntime.AppendEvent(ctx, contracts.TaskEventInput{
			TaskRunID: taskRunID,
			EventType: "task_succeeded",
			FromState: map[string]any{
				"orchestration_state": contracts.TaskRunning,
			},
			ToState: map[string]any{
				"orchestration_state": contracts.TaskSucceeded,
			},
			Note: "pm planner run completed",
		}); err != nil {
			return err
		}
		return nil
	})
}

func (s *Service) completePlannerRunFailed(ctx context.Context, taskRunID uint, errMsg string) error {
	_, db, err := s.require()
	if err != nil {
		return err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	now := time.Now()
	errMsg = strings.TrimSpace(errMsg)
	if errMsg == "" {
		errMsg = "planner run failed"
	}
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		taskRuntime, terr := s.taskRuntimeForDB(tx)
		if terr != nil {
			return terr
		}
		pmState, serr := s.loadPMStateForUpdateTx(ctx, tx)
		if serr != nil {
			return serr
		}
		if plannerRunMatchesState(pmState, taskRunID) {
			s.failPlannerRun(pmState, now, errMsg)
			if err := s.persistPlannerStateTx(ctx, tx, pmState, now); err != nil {
				return err
			}
		}
		if err := taskRuntime.MarkRunFailed(ctx, taskRunID, "planner_failed", errMsg, now); err != nil {
			return err
		}
		if err := taskRuntime.AppendEvent(ctx, contracts.TaskEventInput{
			TaskRunID: taskRunID,
			EventType: "task_failed",
			FromState: map[string]any{
				"orchestration_state": contracts.TaskRunning,
			},
			ToState: map[string]any{
				"orchestration_state": contracts.TaskFailed,
				"error_code":          "planner_failed",
				"error_message":       errMsg,
			},
			Note: "pm planner run failed",
		}); err != nil {
			return err
		}
		return nil
	})
}

func plannerRunMatchesState(st *contracts.PMState, taskRunID uint) bool {
	if st == nil || taskRunID == 0 {
		return false
	}
	return st.PlannerActiveTaskRunID != nil && *st.PlannerActiveTaskRunID == taskRunID
}

func (s *Service) loadPMStateForUpdateTx(ctx context.Context, tx *gorm.DB) (*contracts.PMState, error) {
	if tx == nil {
		return nil, fmt.Errorf("db 不能为空")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	var st contracts.PMState
	err := tx.WithContext(ctx).Order("id asc").First(&st).Error
	if err == nil {
		return &st, nil
	}
	if err != gorm.ErrRecordNotFound {
		return nil, err
	}
	st = contracts.PMState{
		AutopilotEnabled:       true,
		MaxRunningWorkers:      3,
		PlannerDirty:           false,
		PlannerWakeVersion:     0,
		PlannerActiveTaskRunID: nil,
		PlannerCooldownUntil:   nil,
		PlannerLastError:       "",
		PlannerLastRunAt:       nil,
	}
	if err := tx.WithContext(ctx).Create(&st).Error; err != nil {
		return nil, err
	}
	return &st, nil
}

func (s *Service) persistPlannerStateTx(ctx context.Context, tx *gorm.DB, st *contracts.PMState, now time.Time) error {
	if tx == nil {
		return fmt.Errorf("db 不能为空")
	}
	if st == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return tx.WithContext(ctx).Model(&contracts.PMState{}).
		Where("id = ?", st.ID).
		Updates(map[string]any{
			"planner_dirty":              st.PlannerDirty,
			"planner_active_task_run_id": st.PlannerActiveTaskRunID,
			"planner_cooldown_until":     st.PlannerCooldownUntil,
			"planner_last_error":         strings.TrimSpace(st.PlannerLastError),
			"planner_last_run_at":        st.PlannerLastRunAt,
			"updated_at":                 now,
		}).Error
}
