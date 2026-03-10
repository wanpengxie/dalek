package worker

import (
	"context"
	"dalek/internal/contracts"
	"fmt"

	"gorm.io/gorm"
)

func (s *Service) LatestWorker(ctx context.Context, ticketID uint) (*contracts.Worker, error) {
	db, err := s.db()
	if err != nil {
		return nil, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if ticketID == 0 {
		return nil, fmt.Errorf("ticket_id 不能为空")
	}
	var w contracts.Worker
	err = db.WithContext(ctx).Where("ticket_id = ?", ticketID).Order("id desc").First(&w).Error
	if err == nil {
		return &w, nil
	}
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	return nil, err
}

func (s *Service) WorkerByID(ctx context.Context, workerID uint) (*contracts.Worker, error) {
	db, err := s.db()
	if err != nil {
		return nil, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if workerID == 0 {
		return nil, fmt.Errorf("worker_id 不能为空")
	}
	var w contracts.Worker
	if err := db.WithContext(ctx).First(&w, workerID).Error; err != nil {
		return nil, err
	}
	return &w, nil
}

func (s *Service) ListRunningWorkers(ctx context.Context) ([]contracts.Worker, error) {
	db, err := s.db()
	if err != nil {
		return nil, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	var workers []contracts.Worker
	if err := db.WithContext(ctx).Where("status = ?", contracts.WorkerRunning).Order("id asc").Find(&workers).Error; err != nil {
		return nil, err
	}
	return workers, nil
}

func (s *Service) ListStoppableWorkers(ctx context.Context) ([]contracts.Worker, error) {
	db, err := s.db()
	if err != nil {
		return nil, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	var workers []contracts.Worker
	if err := db.WithContext(ctx).
		Where("status IN ? AND TRIM(COALESCE(log_path, '')) <> ''", []contracts.WorkerStatus{
			contracts.WorkerCreating,
			contracts.WorkerRunning,
			contracts.WorkerStopped,
		}).
		Order("id asc").
		Find(&workers).Error; err != nil {
		return nil, err
	}
	return workers, nil
}
