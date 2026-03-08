package pm

import (
	"context"
	"fmt"
	"strings"
	"time"

	"dalek/internal/contracts"

	"gorm.io/gorm"
)

func (s *Service) RecoverPlannerOps(ctx context.Context, now time.Time) (int, error) {
	return s.recoverPlannerOps(ctx, 0, now)
}

func (s *Service) RecoverPlannerOpsForRun(ctx context.Context, plannerRunID uint, now time.Time) (int, error) {
	return s.recoverPlannerOps(ctx, plannerRunID, now)
}

func (s *Service) recoverPlannerOps(ctx context.Context, plannerRunID uint, now time.Time) (int, error) {
	_, db, err := s.require()
	if err != nil {
		return 0, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if now.IsZero() {
		now = time.Now()
	}
	running, err := s.listRunningPMOpJournal(ctx, plannerRunID)
	if err != nil {
		return 0, err
	}
	if len(running) == 0 {
		return 0, nil
	}
	recovered := 0
	for _, entry := range running {
		runIsTerminal, runErr := plannerRunTerminalForRecovery(db.WithContext(ctx), entry.PlannerRunID)
		if runErr != nil {
			return recovered, runErr
		}
		if !runIsTerminal {
			continue
		}
		op := buildPMOpFromJournal(entry)
		executor := s.plannerPMOpExecutor(op.Kind)
		if executor == nil {
			errText := fmt.Sprintf("planner recovery: unsupported op kind %s", strings.TrimSpace(string(op.Kind)))
			if err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
				return s.markPMOpFailedTx(ctx, tx, entry.ID, errText, contracts.JSONMap{
					"source": "recovery",
				}, now)
			}); err != nil {
				return recovered, err
			}
			recovered++
			continue
		}

		reconciled, result, reconcileErr := executor.Reconcile(ctx, op)
		if reconcileErr != nil {
			if err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
				return s.markPMOpFailedTx(ctx, tx, entry.ID, fmt.Sprintf("planner recovery reconcile failed: %v", reconcileErr), contracts.JSONMap{
					"source": "recovery",
					"phase":  "reconcile",
				}, now)
			}); err != nil {
				return recovered, err
			}
			recovered++
			continue
		}
		if reconciled {
			res := contracts.JSONMapFromAny(result)
			res["source"] = "recovery"
			res["recovered"] = true
			if err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
				return s.markPMOpSucceededTx(ctx, tx, entry.ID, res, now)
			}); err != nil {
				return recovered, err
			}
			recovered++
			continue
		}
		if err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			return s.markPMOpFailedTx(ctx, tx, entry.ID, "planner recovery: running op interrupted and reconcile not satisfied", contracts.JSONMap{
				"source": "recovery",
			}, now)
		}); err != nil {
			return recovered, err
		}
		recovered++
	}
	return recovered, nil
}

func plannerRunTerminalForRecovery(db *gorm.DB, runID uint) (bool, error) {
	if db == nil {
		return false, fmt.Errorf("db 不能为空")
	}
	if runID == 0 {
		return true, nil
	}
	var run contracts.TaskRun
	err := db.Select("id", "orchestration_state").First(&run, runID).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return true, nil
		}
		return false, err
	}
	switch run.OrchestrationState {
	case contracts.TaskPending, contracts.TaskRunning:
		return false, nil
	default:
		return true, nil
	}
}
