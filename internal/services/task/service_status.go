package task

import (
	"context"
	"dalek/internal/contracts"
	"fmt"
	"strings"

	"dalek/internal/store"

	"gorm.io/gorm"
)

type ListStatusOptions struct {
	OwnerType       contracts.TaskOwnerType
	TaskType        string
	TicketID        uint
	WorkerID        uint
	IncludeTerminal bool
	Limit           int
}

type EventScopeRow struct {
	contracts.TaskEvent
	TicketID  uint
	WorkerID  uint
	OwnerType string
	TaskType  string
}

func (s *Service) ListStatus(ctx context.Context, opt ListStatusOptions) ([]store.TaskStatusView, error) {
	db, err := s.requireDB()
	if err != nil {
		return nil, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	limit := opt.Limit
	if limit <= 0 {
		limit = 100
	}
	q := db.WithContext(ctx).Model(&store.TaskStatusView{})
	if validOwnerType(opt.OwnerType) {
		q = q.Where("owner_type = ?", opt.OwnerType)
	}
	if strings.TrimSpace(opt.TaskType) != "" {
		q = q.Where("task_type = ?", strings.TrimSpace(opt.TaskType))
	}
	if opt.TicketID != 0 {
		q = q.Where("ticket_id = ?", opt.TicketID)
	}
	if opt.WorkerID != 0 {
		q = q.Where("worker_id = ?", opt.WorkerID)
	}
	if !opt.IncludeTerminal {
		q = q.Where("orchestration_state IN ?", []contracts.TaskOrchestrationState{contracts.TaskPending, contracts.TaskRunning})
	}
	var out []store.TaskStatusView
	if err := q.Order("run_id desc").Limit(limit).Find(&out).Error; err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Service) ListEventsByScope(ctx context.Context, ticketID, workerID uint, limit int) ([]EventScopeRow, error) {
	db, err := s.requireDB()
	if err != nil {
		return nil, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if ticketID == 0 && workerID == 0 {
		return nil, fmt.Errorf("ticket_id 与 worker_id 不能同时为空")
	}
	if limit <= 0 {
		limit = 50
	}
	if limit > 2000 {
		limit = 2000
	}

	q := db.WithContext(ctx).
		Table("task_events AS ev").
		Select("ev.id, ev.created_at, ev.task_run_id, ev.event_type, ev.from_state_json, ev.to_state_json, ev.note, ev.payload_json, tr.ticket_id, tr.worker_id, tr.owner_type, tr.task_type").
		Joins("JOIN task_runs AS tr ON tr.id = ev.task_run_id").
		Order("ev.id desc").
		Limit(limit)
	if workerID != 0 {
		q = q.Where("tr.worker_id = ?", workerID)
	} else {
		q = q.Where("tr.ticket_id = ?", ticketID)
	}
	var out []EventScopeRow
	if err := q.Find(&out).Error; err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Service) ListEventsAfterID(ctx context.Context, afterID uint, limit int) ([]EventScopeRow, error) {
	db, err := s.requireDB()
	if err != nil {
		return nil, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if limit <= 0 {
		limit = 2000
	}
	if limit > 5000 {
		limit = 5000
	}
	var out []EventScopeRow
	if err := db.WithContext(ctx).
		Table("task_events AS ev").
		Select("ev.id, ev.created_at, ev.task_run_id, ev.event_type, ev.from_state_json, ev.to_state_json, ev.note, ev.payload_json, tr.ticket_id, tr.worker_id, tr.owner_type, tr.task_type").
		Joins("JOIN task_runs AS tr ON tr.id = ev.task_run_id").
		Where("ev.id > ?", afterID).
		Order("ev.id asc").
		Limit(limit).
		Find(&out).Error; err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Service) GetStatusByRunID(ctx context.Context, runID uint) (*store.TaskStatusView, error) {
	db, err := s.requireDB()
	if err != nil {
		return nil, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if runID == 0 {
		return nil, fmt.Errorf("run_id 不能为空")
	}
	var out store.TaskStatusView
	if err := db.WithContext(ctx).Model(&store.TaskStatusView{}).Where("run_id = ?", runID).First(&out).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &out, nil
}

func (s *Service) ListEvents(ctx context.Context, runID uint, limit int) ([]contracts.TaskEvent, error) {
	db, err := s.requireDB()
	if err != nil {
		return nil, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if runID == 0 {
		return nil, fmt.Errorf("run_id 不能为空")
	}
	if limit <= 0 {
		limit = 100
	}
	// 语义为“最新 N 条”，但返回顺序按时间升序，便于 CLI 直接按时间线展示。
	latestN := db.WithContext(ctx).Model(&contracts.TaskEvent{}).
		Select("id").
		Where("task_run_id = ?", runID).
		Order("created_at desc, id desc").
		Limit(limit)
	var out []contracts.TaskEvent
	if err := db.WithContext(ctx).
		Where("id IN (?)", latestN).
		Order("created_at asc, id asc").
		Find(&out).Error; err != nil {
		return nil, err
	}
	return out, nil
}
