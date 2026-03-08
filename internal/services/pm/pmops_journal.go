package pm

import (
	"context"
	"fmt"
	"strings"
	"time"

	"dalek/internal/contracts"

	"gorm.io/gorm"
)

func plannerRunInstanceID(taskRunID uint) string {
	if taskRunID == 0 {
		return "planner_run_0"
	}
	return fmt.Sprintf("planner_run_%d", taskRunID)
}

func (s *Service) createPMOpJournalEntriesTx(ctx context.Context, tx *gorm.DB, taskRunID uint, ops []contracts.PMOp, now time.Time) ([]contracts.PMOpJournalEntry, error) {
	if tx == nil {
		return nil, fmt.Errorf("db 不能为空")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if now.IsZero() {
		now = time.Now()
	}
	instanceID := plannerRunInstanceID(taskRunID)
	out := make([]contracts.PMOpJournalEntry, 0, len(ops))
	for _, op := range ops {
		entry := contracts.PMOpJournalEntry{
			InstanceID:     instanceID,
			PlannerRunID:   taskRunID,
			OpID:           strings.TrimSpace(op.OpID),
			FeatureID:      strings.TrimSpace(op.FeatureID),
			RequestID:      strings.TrimSpace(op.RequestID),
			Kind:           contracts.PMOpKind(strings.TrimSpace(string(op.Kind))),
			IdempotencyKey: strings.TrimSpace(op.IdempotencyKey),
			ArgumentsJSON:  contracts.JSONMapFromAny(op.Arguments),
			PrecondsJSON:   contracts.JSONStringSliceFromAny(op.Preconditions),
			Critical:       op.Critical,
			Status:         contracts.PMOpStatusPlanned,
			ResultJSON:     contracts.JSONMap{},
			ErrorText:      "",
			SupersededBy:   "",
			CreatedAt:      now,
			UpdatedAt:      now,
		}
		if err := tx.WithContext(ctx).Create(&entry).Error; err != nil {
			return nil, err
		}
		out = append(out, entry)
	}
	return out, nil
}

func (s *Service) updatePMOpJournalStatusTx(ctx context.Context, tx *gorm.DB, entryID uint, status contracts.PMOpJournalStatus, now time.Time, patch map[string]any) error {
	if tx == nil {
		return fmt.Errorf("db 不能为空")
	}
	if entryID == 0 {
		return fmt.Errorf("entry_id 不能为空")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if now.IsZero() {
		now = time.Now()
	}
	updates := map[string]any{
		"status":     status,
		"updated_at": now,
	}
	for k, v := range patch {
		updates[k] = v
	}
	return tx.WithContext(ctx).Model(&contracts.PMOpJournalEntry{}).Where("id = ?", entryID).Updates(updates).Error
}

func (s *Service) markPMOpRunningTx(ctx context.Context, tx *gorm.DB, entryID uint, now time.Time) error {
	return s.updatePMOpJournalStatusTx(ctx, tx, entryID, contracts.PMOpStatusRunning, now, map[string]any{
		"started_at":  &now,
		"finished_at": nil,
		"error_text":  "",
	})
}

func (s *Service) markPMOpSucceededTx(ctx context.Context, tx *gorm.DB, entryID uint, result contracts.JSONMap, now time.Time) error {
	return s.updatePMOpJournalStatusTx(ctx, tx, entryID, contracts.PMOpStatusSucceeded, now, map[string]any{
		"result_json": contracts.JSONMapFromAny(result),
		"error_text":  "",
		"finished_at": &now,
	})
}

func (s *Service) markPMOpFailedTx(ctx context.Context, tx *gorm.DB, entryID uint, errText string, result contracts.JSONMap, now time.Time) error {
	return s.updatePMOpJournalStatusTx(ctx, tx, entryID, contracts.PMOpStatusFailed, now, map[string]any{
		"result_json": contracts.JSONMapFromAny(result),
		"error_text":  strings.TrimSpace(errText),
		"finished_at": &now,
	})
}

func (s *Service) markPMOpSupersededTx(ctx context.Context, tx *gorm.DB, entryID uint, supersededBy string, now time.Time) error {
	return s.updatePMOpJournalStatusTx(ctx, tx, entryID, contracts.PMOpStatusSuperseded, now, map[string]any{
		"superseded_by": strings.TrimSpace(supersededBy),
		"finished_at":   &now,
	})
}

func (s *Service) listPMOpJournalByPlannerRun(ctx context.Context, plannerRunID uint) ([]contracts.PMOpJournalEntry, error) {
	_, db, err := s.require()
	if err != nil {
		return nil, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if plannerRunID == 0 {
		return nil, nil
	}
	var out []contracts.PMOpJournalEntry
	if err := db.WithContext(ctx).
		Where("planner_run_id = ?", plannerRunID).
		Order("id asc").
		Find(&out).Error; err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Service) listRunningPMOpJournal(ctx context.Context, plannerRunID uint) ([]contracts.PMOpJournalEntry, error) {
	_, db, err := s.require()
	if err != nil {
		return nil, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	q := db.WithContext(ctx).Where("status = ?", contracts.PMOpStatusRunning)
	if plannerRunID != 0 {
		q = q.Where("planner_run_id = ?", plannerRunID)
	}
	var out []contracts.PMOpJournalEntry
	if err := q.Order("id asc").Find(&out).Error; err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Service) findLatestSucceededPMOpByIdempotency(ctx context.Context, kind contracts.PMOpKind, idempotencyKey string) (*contracts.PMOpJournalEntry, error) {
	_, db, err := s.require()
	if err != nil {
		return nil, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if idempotencyKey == "" {
		return nil, nil
	}
	var row contracts.PMOpJournalEntry
	err = db.WithContext(ctx).
		Where("kind = ? AND idempotency_key = ? AND status = ?", kind, idempotencyKey, contracts.PMOpStatusSucceeded).
		Order("id desc").
		First(&row).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &row, nil
}

type plannerCheckpointState struct {
	GraphVersion string
	CompletedOps []string
	RemainingOps []string
	NextAction   string
	FailureCtx   contracts.JSONMap
	Snapshot     contracts.JSONMap
}

func (s *Service) savePMCheckpointTx(ctx context.Context, tx *gorm.DB, plannerRunID uint, st plannerCheckpointState, now time.Time) (*contracts.PMCheckpoint, error) {
	if tx == nil {
		return nil, fmt.Errorf("db 不能为空")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if now.IsZero() {
		now = time.Now()
	}
	instanceID := plannerRunInstanceID(plannerRunID)
	revision := 1
	var latest contracts.PMCheckpoint
	err := tx.WithContext(ctx).
		Where("instance_id = ?", instanceID).
		Order("revision desc").
		First(&latest).Error
	if err == nil {
		revision = latest.Revision + 1
	} else if err != gorm.ErrRecordNotFound {
		return nil, err
	}

	cp := contracts.PMCheckpoint{
		InstanceID:       instanceID,
		PlannerRunID:     plannerRunID,
		Revision:         revision,
		GraphVersion:     strings.TrimSpace(st.GraphVersion),
		CompletedOpsJSON: contracts.JSONStringSliceFromAny(st.CompletedOps),
		RemainingOpsJSON: contracts.JSONStringSliceFromAny(st.RemainingOps),
		NextAction:       strings.TrimSpace(st.NextAction),
		FailureContext:   contracts.JSONMapFromAny(st.FailureCtx),
		SnapshotJSON:     contracts.JSONMapFromAny(st.Snapshot),
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	if err := tx.WithContext(ctx).Create(&cp).Error; err != nil {
		return nil, err
	}
	return &cp, nil
}

func (s *Service) latestPMCheckpoint(ctx context.Context) (*contracts.PMCheckpoint, error) {
	_, db, err := s.require()
	if err != nil {
		return nil, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	var cp contracts.PMCheckpoint
	err = db.WithContext(ctx).Order("id desc").First(&cp).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &cp, nil
}

func (s *Service) PlannerRecoverySnapshot(ctx context.Context, limit int) (contracts.JSONMap, error) {
	_, db, err := s.require()
	if err != nil {
		return contracts.JSONMap{}, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if limit <= 0 {
		limit = 20
	}
	if limit > 200 {
		limit = 200
	}

	out := contracts.JSONMap{
		"latest_checkpoint": contracts.JSONMap{},
		"recent_ops":        []contracts.JSONMap{},
	}

	cp, err := s.latestPMCheckpoint(ctx)
	if err != nil {
		return out, err
	}
	if cp != nil {
		out["latest_checkpoint"] = contracts.JSONMap{
			"id":             cp.ID,
			"instance_id":    strings.TrimSpace(cp.InstanceID),
			"planner_run_id": cp.PlannerRunID,
			"revision":       cp.Revision,
			"graph_version":  strings.TrimSpace(cp.GraphVersion),
			"completed_ops":  contracts.JSONStringSliceFromAny(cp.CompletedOpsJSON),
			"remaining_ops":  contracts.JSONStringSliceFromAny(cp.RemainingOpsJSON),
			"next_action":    strings.TrimSpace(cp.NextAction),
			"failure_ctx":    contracts.JSONMapFromAny(cp.FailureContext),
			"created_at":     cp.CreatedAt.UTC().Format(time.RFC3339),
		}
	}

	var entries []contracts.PMOpJournalEntry
	if err := db.WithContext(ctx).
		Order("id desc").
		Limit(limit).
		Find(&entries).Error; err != nil {
		return out, err
	}
	items := make([]contracts.JSONMap, 0, len(entries))
	for _, entry := range entries {
		item := contracts.JSONMap{
			"id":              entry.ID,
			"instance_id":     strings.TrimSpace(entry.InstanceID),
			"planner_run_id":  entry.PlannerRunID,
			"op_id":           strings.TrimSpace(entry.OpID),
			"kind":            strings.TrimSpace(string(entry.Kind)),
			"status":          strings.TrimSpace(string(entry.Status)),
			"idempotency_key": strings.TrimSpace(entry.IdempotencyKey),
			"request_id":      strings.TrimSpace(entry.RequestID),
			"error":           strings.TrimSpace(entry.ErrorText),
			"created_at":      entry.CreatedAt.UTC().Format(time.RFC3339),
		}
		items = append(items, item)
	}
	out["recent_ops"] = items
	return out, nil
}
