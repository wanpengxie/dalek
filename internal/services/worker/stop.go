package worker

import (
	"context"
	"dalek/internal/contracts"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
)

// StopWorker 停止一个 worker（kill 对应 tmux session）并更新 DB 状态。
// 这是“清理/回收资源”的最小能力：避免 tmux session 越堆越多。
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

	session := strings.TrimSpace(w.TmuxSession)
	if session == "" {
		return fmt.Errorf("worker 缺少 tmux_session: w%d", workerID)
	}

	// kill-session：不存在时 tmux 会返回非 0，但 infra.TmuxClient.KillSession 会把它当作非致命（err=nil）
	if err := p.Tmux.KillSession(ctx, w.TmuxSocket, session); err != nil {
		return err
	}

	now := time.Now()
	if err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.WithContext(ctx).Model(&contracts.Worker{}).Where("id = ?", workerID).Updates(map[string]any{
			"status":     contracts.WorkerStopped,
			"stopped_at": &now,
		}).Error; err != nil {
			return err
		}
		if err := s.appendWorkerStatusEventTx(ctx, tx, w.ID, w.TicketID, w.Status, contracts.WorkerStopped, "worker.stop", "stop 命令停止 worker", map[string]any{
			"worker_id":    w.ID,
			"ticket_id":    w.TicketID,
			"tmux_socket":  strings.TrimSpace(w.TmuxSocket),
			"tmux_session": strings.TrimSpace(w.TmuxSession),
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

// KillAllTmuxSessions 直接关闭本项目 tmux socket 下的所有 sessions（等价于 `tmux -L <socket> kill-server`）。
// 用于“一键清理”。
func (s *Service) KillAllTmuxSessions(ctx context.Context) error {
	p, err := s.require()
	if err != nil {
		return err
	}
	cfg, err := s.cfg()
	if err != nil {
		return err
	}
	return p.Tmux.KillServer(ctx, cfg.TmuxSocket)
}
