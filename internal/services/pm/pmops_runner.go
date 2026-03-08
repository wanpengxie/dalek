package pm

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"dalek/internal/contracts"

	"gorm.io/gorm"
)

type plannerPMOpsRunSummary struct {
	Parsed         int
	Planned        int
	Succeeded      int
	Failed         int
	Superseded     int
	CompletedOps   []string
	RemainingOps   []string
	FailureContext contracts.JSONMap
	CheckpointID   uint
	CheckpointRev  int
}

func (s *Service) executePlannerPMOps(ctx context.Context, plannerRunID uint, ops []contracts.PMOp, now time.Time) (plannerPMOpsRunSummary, error) {
	_, db, err := s.require()
	if err != nil {
		return plannerPMOpsRunSummary{}, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if now.IsZero() {
		now = time.Now()
	}
	summary := plannerPMOpsRunSummary{
		Parsed:       len(ops),
		Planned:      len(ops),
		CompletedOps: []string{},
		RemainingOps: []string{},
	}
	if len(ops) == 0 {
		cp, err := s.persistPlannerPMOpsCheckpoint(ctx, db, plannerRunID, summary, now)
		if err != nil {
			return summary, err
		}
		summary.CheckpointID = cp.ID
		summary.CheckpointRev = cp.Revision
		return summary, nil
	}

	var entries []contracts.PMOpJournalEntry
	if err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		rows, err := s.createPMOpJournalEntriesTx(ctx, tx, plannerRunID, ops, now)
		if err != nil {
			return err
		}
		entries = rows
		return nil
	}); err != nil {
		return summary, err
	}

	var criticalErr error
	for idx := range entries {
		entry := entries[idx]
		op := buildPMOpFromJournal(entry)
		executor := s.plannerPMOpExecutor(op.Kind)
		if executor == nil {
			errText := fmt.Sprintf("unsupported pm op kind: %s", strings.TrimSpace(string(op.Kind)))
			failRes := contracts.JSONMap{
				"op_id": op.OpID,
				"kind":  strings.TrimSpace(string(op.Kind)),
			}
			if err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
				return s.markPMOpFailedTx(ctx, tx, entry.ID, errText, failRes, time.Now())
			}); err != nil {
				return summary, err
			}
			summary.Failed++
			summary.FailureContext = contracts.JSONMap{
				"op_id":    op.OpID,
				"kind":     strings.TrimSpace(string(op.Kind)),
				"error":    errText,
				"critical": op.Critical,
			}
			if op.Critical {
				criticalErr = errors.New(errText)
				for j := idx + 1; j < len(entries); j++ {
					remaining := entries[j]
					if err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
						return s.markPMOpSupersededTx(ctx, tx, remaining.ID, op.OpID, time.Now())
					}); err != nil {
						return summary, err
					}
					summary.Superseded++
					summary.RemainingOps = append(summary.RemainingOps, strings.TrimSpace(remaining.OpID))
				}
				break
			}
			summary.RemainingOps = append(summary.RemainingOps, strings.TrimSpace(op.OpID))
			continue
		}

		reconciled, reconcileResult, reconcileErr := executor.Reconcile(ctx, op)
		if reconcileErr != nil {
			if err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
				return s.markPMOpFailedTx(ctx, tx, entry.ID, reconcileErr.Error(), contracts.JSONMap{
					"phase": "reconcile",
				}, time.Now())
			}); err != nil {
				return summary, err
			}
			summary.Failed++
			summary.FailureContext = contracts.JSONMap{
				"op_id":    op.OpID,
				"kind":     strings.TrimSpace(string(op.Kind)),
				"error":    strings.TrimSpace(reconcileErr.Error()),
				"phase":    "reconcile",
				"critical": op.Critical,
			}
			if op.Critical {
				criticalErr = fmt.Errorf("critical pm op reconcile failed: %w", reconcileErr)
				for j := idx + 1; j < len(entries); j++ {
					remaining := entries[j]
					if err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
						return s.markPMOpSupersededTx(ctx, tx, remaining.ID, op.OpID, time.Now())
					}); err != nil {
						return summary, err
					}
					summary.Superseded++
					summary.RemainingOps = append(summary.RemainingOps, strings.TrimSpace(remaining.OpID))
				}
				break
			}
			summary.RemainingOps = append(summary.RemainingOps, strings.TrimSpace(op.OpID))
			continue
		}
		if reconciled {
			res := contracts.JSONMapFromAny(reconcileResult)
			res["reconciled"] = true
			if err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
				return s.markPMOpSucceededTx(ctx, tx, entry.ID, res, time.Now())
			}); err != nil {
				return summary, err
			}
			summary.Succeeded++
			summary.CompletedOps = append(summary.CompletedOps, strings.TrimSpace(op.OpID))
			continue
		}

		if err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			return s.markPMOpRunningTx(ctx, tx, entry.ID, time.Now())
		}); err != nil {
			return summary, err
		}
		execResult, execErr := executor.Execute(ctx, op)
		if execErr != nil {
			if err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
				return s.markPMOpFailedTx(ctx, tx, entry.ID, execErr.Error(), contracts.JSONMap{}, time.Now())
			}); err != nil {
				return summary, err
			}
			summary.Failed++
			summary.FailureContext = contracts.JSONMap{
				"op_id":    op.OpID,
				"kind":     strings.TrimSpace(string(op.Kind)),
				"error":    strings.TrimSpace(execErr.Error()),
				"phase":    "execute",
				"critical": op.Critical,
			}
			if op.Critical {
				criticalErr = fmt.Errorf("critical pm op execute failed: %w", execErr)
				for j := idx + 1; j < len(entries); j++ {
					remaining := entries[j]
					if err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
						return s.markPMOpSupersededTx(ctx, tx, remaining.ID, op.OpID, time.Now())
					}); err != nil {
						return summary, err
					}
					summary.Superseded++
					summary.RemainingOps = append(summary.RemainingOps, strings.TrimSpace(remaining.OpID))
				}
				break
			}
			summary.RemainingOps = append(summary.RemainingOps, strings.TrimSpace(op.OpID))
			continue
		}
		if err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			return s.markPMOpSucceededTx(ctx, tx, entry.ID, contracts.JSONMapFromAny(execResult), time.Now())
		}); err != nil {
			return summary, err
		}
		summary.Succeeded++
		summary.CompletedOps = append(summary.CompletedOps, strings.TrimSpace(op.OpID))
	}

	if len(summary.RemainingOps) == 0 {
		for idx := range entries {
			opID := strings.TrimSpace(entries[idx].OpID)
			if opID == "" {
				continue
			}
			if !containsString(summary.CompletedOps, opID) {
				summary.RemainingOps = append(summary.RemainingOps, opID)
			}
		}
	}

	cp, err := s.persistPlannerPMOpsCheckpoint(ctx, db, plannerRunID, summary, time.Now())
	if err != nil {
		return summary, err
	}
	summary.CheckpointID = cp.ID
	summary.CheckpointRev = cp.Revision

	if criticalErr != nil {
		return summary, criticalErr
	}
	return summary, nil
}

func (s *Service) persistPlannerPMOpsCheckpoint(ctx context.Context, db *gorm.DB, plannerRunID uint, summary plannerPMOpsRunSummary, now time.Time) (*contracts.PMCheckpoint, error) {
	if db == nil {
		return nil, fmt.Errorf("db 不能为空")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if now.IsZero() {
		now = time.Now()
	}
	nextAction := "continue"
	snapshot := contracts.JSONMap{
		"parsed":        summary.Parsed,
		"planned":       summary.Planned,
		"succeeded":     summary.Succeeded,
		"failed":        summary.Failed,
		"superseded":    summary.Superseded,
		"completed":     contracts.JSONStringSliceFromAny(summary.CompletedOps),
		"remaining":     contracts.JSONStringSliceFromAny(summary.RemainingOps),
		"checkpoint_at": now.UTC().Format(time.RFC3339),
	}
	var cp *contracts.PMCheckpoint
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		row, err := s.savePMCheckpointTx(ctx, tx, plannerRunID, plannerCheckpointState{
			GraphVersion: "",
			CompletedOps: summary.CompletedOps,
			RemainingOps: summary.RemainingOps,
			NextAction:   nextAction,
			FailureCtx:   contracts.JSONMapFromAny(summary.FailureContext),
			Snapshot:     snapshot,
		}, now)
		if err != nil {
			return err
		}
		cp = row
		return nil
	})
	return cp, err
}

func containsString(items []string, target string) bool {
	target = strings.TrimSpace(target)
	if target == "" {
		return false
	}
	for _, item := range items {
		if strings.TrimSpace(item) == target {
			return true
		}
	}
	return false
}
