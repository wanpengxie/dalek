package pm

import (
	"context"
	"dalek/internal/contracts"
	"dalek/internal/fsm"
	"dalek/internal/services/ticketlifecycle"
	"fmt"
	"strings"
	"time"

	workersvc "dalek/internal/services/worker"

	"gorm.io/gorm"
)

type StartOptions struct {
	BaseBranch string
}

// StartTicket 是 PM 视角的 start：编排 worker 资源启动，并把 ticket 投影到 queued。
//
// 约束：
// - worker 只负责“资源启动”（worktree + runtime 进程）。
//
// TODO(tech-debt): 当前 start 仍会在入队前预建 worker 资源。
// 这让 queued 变成“资源已预热、等待消费”的半启动状态，而不是纯排队态。
// 目标语义应改为：start 只负责把 ticket 提交到 queued；worktree/runtime 的创建延后到
// queue consumer 真正拿到项目级配额并开始消费时再做。
func (s *Service) StartTicket(ctx context.Context, ticketID uint) (*contracts.Worker, error) {
	return s.StartTicketWithOptions(ctx, ticketID, StartOptions{})
}

func (s *Service) StartTicketWithOptions(ctx context.Context, ticketID uint, opt StartOptions) (*contracts.Worker, error) {
	_, db, err := s.require()
	if err != nil {
		return nil, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if ticketID == 0 {
		return nil, fmt.Errorf("ticket_id 不能为空")
	}

	var t contracts.Ticket
	if err := db.WithContext(ctx).First(&t, ticketID).Error; err != nil {
		return nil, err
	}
	baseBranch := strings.TrimSpace(opt.BaseBranch)
	if baseBranch == "" {
		baseBranch, err = requiredWorkerBaseBranch(t)
	} else {
		baseBranch, err = resolveWorkerBaseBranch(t, baseBranch)
	}
	if err != nil {
		return nil, err
	}
	shouldNotifyQueued := contracts.CanonicalTicketWorkflowStatus(t.WorkflowStatus) != contracts.TicketQueued
	if !fsm.CanStartTicket(t.WorkflowStatus) {
		switch contracts.CanonicalTicketWorkflowStatus(t.WorkflowStatus) {
		case contracts.TicketArchived:
			return nil, fmt.Errorf("ticket 已归档，不能启动（start）：t%d", ticketID)
		default:
			return nil, fmt.Errorf("ticket 已完成，不能启动（start）：t%d", ticketID)
		}
	}

	preStartWorker, err := s.worker.LatestWorker(ctx, ticketID)
	if err != nil {
		return nil, err
	}
	preStartReady := false
	if preStartWorker != nil && (preStartWorker.Status == contracts.WorkerRunning || preStartWorker.Status == contracts.WorkerStopped) {
		ready, rerr := s.workerDispatchReady(ctx, preStartWorker)
		if rerr != nil {
			return nil, rerr
		}
		preStartReady = ready
	}

	// TODO(tech-debt): 这一步理想上应迁移到 queue consumer 拿到项目级配额之后。
	// 当前保留在 start 中，原因是历史语义把 start 定义成“预热执行资源”而非“纯入队”。
	// 这也是 start/queue 边界仍不干净的核心耦合点。
	// 1) 启动 worker 资源（worktree + runtime 进程），不做 PM bootstrap。
	w, err := s.worker.StartTicketResourcesWithOptions(ctx, ticketID, workersvc.StartOptions{
		BaseBranch: baseBranch,
	})
	if err != nil {
		return nil, err
	}
	if w == nil {
		return nil, fmt.Errorf("start 失败：未返回 worker")
	}

	// 已经具备运行锚点则直接返回，start 只负责准备资源而非进入执行。
	if preStartReady && (w.Status == contracts.WorkerRunning || w.Status == contracts.WorkerStopped) {
		ready, rerr := s.workerDispatchReady(ctx, w)
		if rerr != nil {
			return nil, rerr
		}
		if ready {
			if err := s.ensureTicketTargetRefOnStart(ctx, t.ID, baseBranch); err != nil {
				return nil, err
			}
			if err := s.promoteTicketQueuedOnStart(ctx, db, t, *w); err != nil {
				return nil, err
			}
			if shouldNotifyQueued {
				s.notifyQueued(t.ID)
			}
			return w, nil
		}
	}

	// 2) start 初始化 hook（当前 no-op，仅保留接口）。
	if err := s.executePMBootstrapEntrypoint(ctx, t, *w); err != nil {
		return nil, err
	}

	// 3) 返回最新 worker；start 结束时 worker 应保持 stopped（资源已准备，等待 run accepted）。
	out, err := s.worker.WorkerByID(ctx, w.ID)
	if err != nil {
		return nil, fmt.Errorf("读取 worker 失败（w%d）：%w", w.ID, err)
	}
	if out.Status != contracts.WorkerStopped && out.Status != contracts.WorkerRunning {
		return nil, fmt.Errorf("start 后 worker 未进入可调度状态（w%d status=%s）", out.ID, out.Status)
	}
	if err := s.promoteTicketQueuedOnStart(ctx, db, t, *out); err != nil {
		return nil, err
	}
	if shouldNotifyQueued {
		s.notifyQueued(t.ID)
	}
	if err := s.ensureTicketTargetRefOnStart(ctx, t.ID, baseBranch); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Service) promoteTicketQueuedOnStart(ctx context.Context, db *gorm.DB, t contracts.Ticket, w contracts.Worker) error {
	if db == nil {
		return nil
	}
	now := time.Now()
	if err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var current contracts.Ticket
		if err := tx.WithContext(ctx).Select("id", "workflow_status").First(&current, t.ID).Error; err != nil {
			return err
		}
		from := contracts.CanonicalTicketWorkflowStatus(current.WorkflowStatus)
		if from == "" {
			from = contracts.TicketBacklog
		}
		if from != contracts.TicketBacklog && from != contracts.TicketBlocked {
			return nil
		}
		lifecycleResult, err := s.appendTicketLifecycleEventAndProjectSnapshotTx(ctx, tx, ticketlifecycle.AppendInput{
			TicketID:       t.ID,
			EventType:      contracts.TicketLifecycleStartRequested,
			Source:         "pm.start",
			ActorType:      contracts.TicketLifecycleActorUser,
			WorkerID:       w.ID,
			IdempotencyKey: ticketlifecycle.StartRequestedIdempotencyKey(t.ID, now),
			Payload: map[string]any{
				"ticket_id": t.ID,
				"worker_id": w.ID,
			},
			CreatedAt: now,
		})
		if err != nil {
			return err
		}
		if !lifecycleResult.WorkflowChanged() {
			return nil
		}
		return s.appendTicketWorkflowEventTx(ctx, tx, t.ID, lifecycleResult.Before.WorkflowStatus, lifecycleResult.After.WorkflowStatus, "pm.start", "start 推进到 queued", map[string]any{
			"ticket_id": t.ID,
			"worker_id": w.ID,
		}, now)
	}); err != nil {
		return fmt.Errorf("更新 ticket workflow 失败（t%d w%d）：%w", t.ID, w.ID, err)
	}
	return nil
}

func (s *Service) ensureTicketTargetRefOnStart(ctx context.Context, ticketID uint, baseBranch string) error {
	_, db, err := s.require()
	if err != nil {
		return err
	}
	target := normalizeIntegrationTargetRef(baseBranch)
	if target == "" {
		target = s.currentHeadTargetRef(ctx)
	}
	if strings.TrimSpace(target) == "" {
		return nil
	}
	now := time.Now()
	return db.WithContext(ctx).
		Model(&contracts.Ticket{}).
		Where("id = ? AND TRIM(COALESCE(target_branch, '')) = ''", ticketID).
		Updates(map[string]any{
			"target_branch": target,
			"updated_at":    now,
		}).Error
}
