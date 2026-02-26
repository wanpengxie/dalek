package worker

import (
	"context"
	"dalek/internal/contracts"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
)

type WatcherCommandTrace struct {
	Prompt     string
	InputJSON  string
	OutputJSON string
	Stderr     string
}

func (s *Service) RequestWorkerSemanticWatch(ctx context.Context, workerID uint, now time.Time) error {
	rt, err := s.taskRuntime()
	if err != nil {
		return err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if workerID == 0 {
		return fmt.Errorf("worker_id 不能为空")
	}
	if now.IsZero() {
		now = time.Now()
	}
	run, err := rt.LatestActiveWorkerRun(ctx, workerID)
	if err != nil || run == nil {
		return err
	}
	return rt.AppendEvent(ctx, contracts.TaskEventInput{
		TaskRunID: run.ID,
		EventType: "watch_requested",
		Note:      "worker semantic watch requested",
		CreatedAt: now,
	})
}

func (s *Service) MarkWorkerRunning(ctx context.Context, workerID uint, now time.Time) error {
	db, err := s.db()
	if err != nil {
		return err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if workerID == 0 {
		return fmt.Errorf("worker_id 不能为空")
	}
	if now.IsZero() {
		now = time.Now()
	}
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var w contracts.Worker
		if err := tx.WithContext(ctx).First(&w, workerID).Error; err != nil {
			return err
		}
		if err := tx.WithContext(ctx).Model(&contracts.Worker{}).Where("id = ?", workerID).Updates(map[string]any{
			"status":     contracts.WorkerRunning,
			"started_at": &now,
			"stopped_at": nil,
			"last_error": "",
		}).Error; err != nil {
			return err
		}
		return s.appendWorkerStatusEventTx(ctx, tx, w.ID, w.TicketID, w.Status, contracts.WorkerRunning, "worker.transition", "worker 标记为 running", map[string]any{
			"worker_id": w.ID,
			"ticket_id": w.TicketID,
		}, now)
	})
}

func (s *Service) MarkWorkerFailed(ctx context.Context, workerID uint, now time.Time, lastError string) error {
	db, err := s.db()
	if err != nil {
		return err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if workerID == 0 {
		return fmt.Errorf("worker_id 不能为空")
	}
	if now.IsZero() {
		now = time.Now()
	}

	var w contracts.Worker
	if err := db.WithContext(ctx).First(&w, workerID).Error; err != nil {
		return err
	}

	lastError = strings.TrimSpace(lastError)
	_ = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.WithContext(ctx).Model(&contracts.Worker{}).Where("id = ?", workerID).Updates(map[string]any{
			"status":     contracts.WorkerFailed,
			"last_error": lastError,
			"stopped_at": &now,
		}).Error; err != nil {
			return err
		}
		return s.appendWorkerStatusEventTx(ctx, tx, w.ID, w.TicketID, w.Status, contracts.WorkerFailed, "worker.transition", "worker 标记为 failed", map[string]any{
			"worker_id":  w.ID,
			"ticket_id":  w.TicketID,
			"last_error": lastError,
		}, now)
	})

	_ = s.appendWorkerTaskEvent(ctx, w.ID, "worker_failed", truncateMiddle(lastError, 2000), nil, now)
	return nil
}

// MarkWorkerSessionNotAlive 由 watcher 调用：当确认 tmux session 不存在时，推进 worker 生命周期状态。
//
// 约束：
// - watcher 不直接写 worker.status/ticket.status，必须走 worker 的权威入口。
// - 这里不 kill tmux（session 已不存活），只做 DB 状态收口。
func (s *Service) MarkWorkerSessionNotAlive(ctx context.Context, w contracts.Worker, now time.Time) error {
	db, err := s.db()
	if err != nil {
		return err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if w.ID == 0 {
		return fmt.Errorf("worker_id 不能为空")
	}
	if w.TicketID == 0 {
		return fmt.Errorf("ticket_id 不能为空")
	}
	if now.IsZero() {
		now = time.Now()
	}

	// 同步 worker 生命周期状态（避免 manager/autopilot 继续把它计入 running 容量）。
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		res := tx.WithContext(ctx).Model(&contracts.Worker{}).
			Where("id = ? AND status = ?", w.ID, contracts.WorkerRunning).
			Updates(map[string]any{
				"status":     contracts.WorkerStopped,
				"stopped_at": &now,
			})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			// 已经不是 running，则不再回退 ticket（避免覆盖并发状态变更）。
			return nil
		}
		return s.appendWorkerStatusEventTx(ctx, tx, w.ID, w.TicketID, contracts.WorkerRunning, contracts.WorkerStopped, "worker.transition", "session 不存活，worker 收口为 stopped", map[string]any{
			"worker_id": w.ID,
			"ticket_id": w.TicketID,
		}, now)
	})
}

func truncateMiddle(s string, maxRunes int) string {
	s = strings.TrimSpace(s)
	if s == "" || maxRunes <= 0 {
		return ""
	}
	rs := []rune(s)
	if len(rs) <= maxRunes {
		return s
	}
	marker := "\n...TRUNCATED...\n"
	mr := []rune(marker)
	if maxRunes <= len(mr)+10 {
		return string(rs[:maxRunes])
	}
	head := (maxRunes - len(mr)) / 2
	tail := maxRunes - len(mr) - head
	if head < 0 {
		head = 0
	}
	if tail < 0 {
		tail = 0
	}
	return string(rs[:head]) + marker + string(rs[len(rs)-tail:])
}
