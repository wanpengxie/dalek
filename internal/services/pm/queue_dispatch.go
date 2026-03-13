package pm

import (
	"context"
	"time"
)

const queueConsumerCycleTimeout = 30 * time.Second

// notifyQueued 在 ticket 进入 queued 后发送唤醒信号，并立即触发消费者按项目配额扫描 queued backlog。
func (s *Service) notifyQueued(ticketID uint) {
	if s == nil || ticketID == 0 {
		return
	}
	select {
	case s.queuedCh <- ticketID:
	default:
		s.slog().Warn("queued dispatch channel full, wake will rely on next rescan",
			"ticket_id", ticketID,
		)
	}
	s.KickQueueConsumer()
}

// KickQueueConsumer 主动唤醒 queued consumer 重新扫描 backlog。
// 用于 daemon 启动、run settled 或周期性事件后重新按额度消费 queued ticket。
func (s *Service) KickQueueConsumer() {
	if s == nil || s.queueWakeCh == nil {
		return
	}
	select {
	case s.queueWakeCh <- struct{}{}:
	default:
	}
}

// StartQueueConsumer 启动 queued ticket 的即时消费 goroutine。
// 幂等：多次调用只会启动一个消费者。ctx 取消时消费者退出。
func (s *Service) StartQueueConsumer(ctx context.Context) {
	if s == nil {
		return
	}
	s.queueConsumerOnce.Do(func() {
		go s.runQueueConsumer(ctx)
	})
	s.KickQueueConsumer()
}

func (s *Service) runQueueConsumer(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case ticketID := <-s.queuedCh:
			s.slog().Debug("queue consumer: ticket notified", "ticket_id", ticketID)
			s.consumeQueuedBacklog(ctx)
		case <-s.queueWakeCh:
			s.consumeQueuedBacklog(ctx)
		}
	}
}

func (s *Service) consumeQueuedBacklog(parent context.Context) {
	if s == nil {
		return
	}
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), queueConsumerCycleTimeout)
	defer cancel()

	_, db, err := s.require()
	if err != nil {
		s.slog().Warn("queue consumer: service not ready", "error", err)
		return
	}
	st, err := s.getOrInitPMState(ctx)
	if err != nil {
		s.slog().Warn("queue consumer: load pm state failed", "error", err)
		return
	}
	taskRuntime, err := s.taskRuntimeForDB(db)
	if err != nil {
		s.slog().Warn("queue consumer: task runtime unavailable", "error", err)
		return
	}
	scanResult, err := s.scanRunningWorkers(ctx, db, taskRuntime, st)
	if err != nil {
		s.slog().Warn("queue consumer: scan running workers failed", "error", err)
		return
	}
	maxRunning := clampMaxRunning(st.MaxRunningWorkers)
	capacity := maxRunning - scanResult.Progressable
	if capacity <= 0 {
		if err := s.persistPlannerState(ctx, db, st); err != nil {
			s.slog().Warn("queue consumer: persist planner state failed", "error", err)
		}
		return
	}

	// TODO(tech-debt): queue consumer 当前只负责“按项目级配额挑选 queued ticket 并提交运行”，
	// 但默认假设 start 可能已经提前预建了 worker 资源。等 start 收敛为纯入队后，
	// 这里应成为唯一的资源启动入口：先拿到项目级配额，再创建 worktree/runtime，再提交 worker run。
	result := s.scheduleQueuedTickets(ctx, db, scheduleOptions{
		Capacity:         capacity,
		RunningTicketIDs: scanResult.RunningTicketIDs,
		PMState:          st,
		Source:           "pm.queue_consumer",
	})
	for _, msg := range result.Errors {
		s.slog().Warn("queue consumer: consume queued backlog failed", "error", msg)
	}
	if err := s.persistPlannerState(ctx, db, st); err != nil {
		s.slog().Warn("queue consumer: persist planner state failed", "error", err)
	}
}
