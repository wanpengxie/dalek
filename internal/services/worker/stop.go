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

	stopMode := "tmux"
	runtimeStopErr := error(nil)
	if hasWorkerRuntimeHandle(w) {
		stopMode = "process"
		runtimeStopErr = p.WorkerRuntime.StopProcess(ctx, workerRuntimeHandle(w), defaultWorkerProcessStopTimeout)
		if runtimeStopErr == nil {
			stopMode = "process"
		}
	}
	if runtimeStopErr != nil || !hasWorkerRuntimeHandle(w) {
		session := strings.TrimSpace(w.TmuxSession)
		if p.Tmux == nil {
			if runtimeStopErr != nil {
				return runtimeStopErr
			}
			return fmt.Errorf("worker 缺少可停止运行句柄: w%d", workerID)
		}
		if session == "" {
			if runtimeStopErr != nil {
				return runtimeStopErr
			}
			return fmt.Errorf("worker 缺少可停止运行句柄: w%d", workerID)
		}
		// kill-session：不存在时 tmux 会返回非 0，但 infra.TmuxClient.KillSession 会把它当作非致命（err=nil）
		if err := p.Tmux.KillSession(ctx, w.TmuxSocket, session); err != nil {
			return err
		}
		stopMode = "tmux"
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
			"stop_mode":      stopMode,
			"process_pid":    w.ProcessPID,
			"log_path":       strings.TrimSpace(w.LogPath),
			"tmux_socket":    strings.TrimSpace(w.TmuxSocket),
			"tmux_session":   strings.TrimSpace(w.TmuxSession),
			"runtime_failed": runtimeStopErr != nil,
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
