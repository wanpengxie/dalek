package pm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"dalek/internal/contracts"

	"gorm.io/gorm"
)

const defaultAgentBudget = 10

// CreateFocusRun 创建一个新的 focus run。同一项目同时只允许一个非终态 focus。
func (s *Service) CreateFocusRun(ctx context.Context, mode string, ticketIDs []uint, budget int) (*contracts.FocusRun, error) {
	_, db, err := s.require()
	if err != nil {
		return nil, err
	}
	if mode != contracts.FocusModeBatch {
		return nil, fmt.Errorf("暂只支持 batch 模式")
	}
	if len(ticketIDs) == 0 {
		return nil, fmt.Errorf("scope ticket 不能为空")
	}
	if budget <= 0 {
		budget = defaultAgentBudget
	}

	scopeJSON, _ := json.Marshal(ticketIDs)
	now := time.Now()
	focus := contracts.FocusRun{
		ProjectKey:     strings.TrimSpace(s.p.Key),
		Mode:           mode,
		Status:         contracts.FocusQueued,
		ScopeTicketIDs: string(scopeJSON),
		TotalCount:     len(ticketIDs),
		AgentBudget:    budget,
		AgentBudgetMax: budget,
		StartedAt:      &now,
	}

	// 唯一性检查 + 创建放在同一事务中，避免并发创建
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var active contracts.FocusRun
		txErr := tx.Where("project_key = ? AND status NOT IN ?", strings.TrimSpace(s.p.Key),
			[]string{contracts.FocusCompleted, contracts.FocusFailed, contracts.FocusCanceled}).
			First(&active).Error
		if txErr == nil {
			return fmt.Errorf("已存在 active focus（id=%d status=%s），请先 stop", active.ID, active.Status)
		}
		if !errors.Is(txErr, gorm.ErrRecordNotFound) {
			return fmt.Errorf("检查 active focus 失败: %w", txErr)
		}
		return tx.Create(&focus).Error
	})
	if err != nil {
		return nil, err
	}
	return &focus, nil
}

// ActiveFocusRun 返回当前项目的 active focus run（如果有）。
func (s *Service) ActiveFocusRun(ctx context.Context) (*contracts.FocusRun, error) {
	_, db, err := s.require()
	if err != nil {
		return nil, err
	}
	var focus contracts.FocusRun
	err = db.WithContext(ctx).
		Where("project_key = ? AND status NOT IN ?", strings.TrimSpace(s.p.Key),
			[]string{contracts.FocusCompleted, contracts.FocusFailed, contracts.FocusCanceled}).
		First(&focus).Error
	if err != nil {
		return nil, nil // 无 active focus
	}
	return &focus, nil
}

// StopFocusRun 停止当前 active focus。
func (s *Service) StopFocusRun(ctx context.Context, reason string) error {
	focus, err := s.ActiveFocusRun(ctx)
	if err != nil {
		return err
	}
	if focus == nil {
		return fmt.Errorf("当前无 active focus")
	}

	// 取消运行中的 loop
	s.focusCancelMu.Lock()
	if s.focusCancelFn != nil {
		s.focusCancelFn()
	}
	s.focusCancelMu.Unlock()

	return s.finishFocusRun(ctx, focus, contracts.FocusCanceled, strings.TrimSpace(reason))
}

func (s *Service) updateFocusRun(ctx context.Context, focus *contracts.FocusRun) error {
	_, db, err := s.require()
	if err != nil {
		return err
	}
	return db.WithContext(ctx).Save(focus).Error
}

func (s *Service) finishFocusRun(ctx context.Context, focus *contracts.FocusRun, status string, summary string) error {
	now := time.Now()
	focus.Status = status
	focus.FinishedAt = &now
	if summary != "" {
		focus.Summary = summary
	}
	return s.updateFocusRun(ctx, focus)
}

// parseScopeTicketIDs 解析 scope JSON 为 uint slice。
func parseScopeTicketIDs(raw string) ([]uint, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var ids []uint
	if err := json.Unmarshal([]byte(raw), &ids); err != nil {
		return nil, fmt.Errorf("解析 scope ticket IDs 失败: %w", err)
	}
	return ids, nil
}
