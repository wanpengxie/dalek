package worker

import (
	"context"
	"dalek/internal/contracts"
	"fmt"
	"strings"
	"time"

	"dalek/internal/store"

	"gorm.io/gorm"
)

func (s *Service) LatestWorker(ctx context.Context, ticketID uint) (*store.Worker, error) {
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
	var w store.Worker
	err = db.WithContext(ctx).Where("ticket_id = ?", ticketID).Order("id desc").First(&w).Error
	if err == nil {
		return &w, nil
	}
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	return nil, err
}

func (s *Service) WorkerByID(ctx context.Context, workerID uint) (*store.Worker, error) {
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
	var w store.Worker
	if err := db.WithContext(ctx).First(&w, workerID).Error; err != nil {
		return nil, err
	}
	return &w, nil
}

func (s *Service) ListRunningWorkers(ctx context.Context) ([]store.Worker, error) {
	db, err := s.db()
	if err != nil {
		return nil, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	var workers []store.Worker
	if err := db.WithContext(ctx).Where("status = ?", contracts.WorkerRunning).Order("id asc").Find(&workers).Error; err != nil {
		return nil, err
	}
	return workers, nil
}

func (s *Service) ReconcileRunningWorkersAfterKillAll(ctx context.Context, socket string) (int64, error) {
	db, err := s.db()
	if err != nil {
		return 0, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	socket = strings.TrimSpace(socket)
	if socket == "" {
		cfg, err := s.cfg()
		if err != nil {
			return 0, err
		}
		socket = strings.TrimSpace(cfg.TmuxSocket)
	}
	if socket == "" {
		return 0, fmt.Errorf("tmux socket 为空")
	}

	now := nowLocal()
	var rows int64
	if err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var txWorkers []store.Worker
		if err := tx.WithContext(ctx).
			Where("tmux_socket = ? AND status = ?", socket, contracts.WorkerRunning).
			Order("id asc").
			Find(&txWorkers).Error; err != nil {
			return err
		}
		if len(txWorkers) == 0 {
			rows = 0
			return nil
		}

		workerIDs := make([]uint, 0, len(txWorkers))
		for _, w := range txWorkers {
			workerIDs = append(workerIDs, w.ID)
		}

		if err := tx.WithContext(ctx).Model(&store.Worker{}).
			Where("id IN ?", workerIDs).
			Updates(map[string]any{
				"status":     contracts.WorkerStopped,
				"stopped_at": &now,
			}).Error; err != nil {
			return err
		}
		rows = int64(len(workerIDs))
		rt, terr := s.taskRuntimeForDB(tx)
		if terr != nil {
			return terr
		}
		for _, w := range txWorkers {
			if err := s.appendWorkerStatusEventTx(ctx, tx, w.ID, w.TicketID, w.Status, contracts.WorkerStopped, "worker.stop_all", "kill-all 后批量收口为 stopped", map[string]any{
				"worker_id": w.ID,
				"ticket_id": w.TicketID,
				"socket":    socket,
			}, now); err != nil {
				return err
			}
			if err := s.finalizeWorkerTaskRunOnStopWithRuntime(ctx, rt, w, fmt.Sprintf("worker stopped by stop-all: socket=%s", socket), "worker_stop_all", now); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return 0, err
	}
	return rows, nil
}

func nowLocal() time.Time {
	return time.Now()
}
