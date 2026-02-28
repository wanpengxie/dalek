package worker

import (
	"context"
	"dalek/internal/contracts"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
)

// StopWorker 停止一个 worker 并更新 DB 状态。
func (s *Service) StopWorker(ctx context.Context, workerID uint) error {
	p, err := s.require()
	if err != nil {
		return err
	}
	db := p.DB
	if ctx == nil {
		ctx = context.Background()
	}
	if workerID == 0 {
		return fmt.Errorf("worker_id 不能为空")
	}

	var w contracts.Worker
	if err := db.First(&w, workerID).Error; err != nil {
		return err
	}

	now := time.Now()
	if err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.WithContext(ctx).Model(&contracts.Worker{}).Where("id = ?", workerID).Updates(map[string]any{
			"status":      contracts.WorkerStopped,
			"stopped_at":  &now,
			"process_pid": 0,
		}).Error; err != nil {
			return err
		}
		if err := s.appendWorkerStatusEventTx(ctx, tx, w.ID, w.TicketID, w.Status, contracts.WorkerStopped, "worker.stop", "stop 命令停止 worker", map[string]any{
			"worker_id":      w.ID,
			"ticket_id":      w.TicketID,
			"stop_mode":      "task_runtime",
			"log_path":       strings.TrimSpace(w.LogPath),
			"runtime_failed": false,
		}, now); err != nil {
			return err
		}
		rt, terr := s.taskRuntimeForDB(tx)
		if terr != nil {
			return terr
		}
		return s.finalizeWorkerTaskRunOnStopWithRuntime(ctx, rt, w, fmt.Sprintf("worker stopped by stop command: w%d", w.ID), "worker_stop", now)
	}); err != nil {
		return err
	}

	return nil
}
