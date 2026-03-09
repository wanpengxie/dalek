package pm

import (
	"context"
	"dalek/internal/contracts"
	"dalek/internal/fsm"
	"fmt"
	"strings"
	"time"

	workersvc "dalek/internal/services/worker"

	"gorm.io/gorm"
)

type StartOptions struct {
	BaseBranch string
}

// StartTicket 是 PM 视角的 start：编排 worker 资源启动，并把 ticket 置为可 dispatch 的 queued。
//
// 约束：
// - worker 只负责“资源启动”（worktree + runtime 进程）。
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
	if !fsm.CanStartTicket(t.WorkflowStatus) {
		switch contracts.CanonicalTicketWorkflowStatus(t.WorkflowStatus) {
		case contracts.TicketArchived:
			return nil, fmt.Errorf("ticket 已归档，不能启动（start）：t%d", ticketID)
		default:
			return nil, fmt.Errorf("ticket 已完成，不能启动（start）：t%d", ticketID)
		}
	}

	// 1) 启动 worker 资源（worktree + runtime 进程），不做 PM bootstrap。
	w, err := s.worker.StartTicketResourcesWithOptions(ctx, ticketID, workersvc.StartOptions{
		BaseBranch: strings.TrimSpace(opt.BaseBranch),
	})
	if err != nil {
		return nil, err
	}
	if w == nil {
		return nil, fmt.Errorf("start 失败：未返回 worker")
	}

	// 已经是 running 且执行资源可探测则直接返回。
	if w.Status == contracts.WorkerRunning {
		ready, rerr := s.workerDispatchReady(ctx, w)
		if rerr != nil {
			return nil, rerr
		}
		if ready {
			if err := s.promoteTicketQueuedOnStart(ctx, db, t, *w); err != nil {
				return nil, err
			}
			return w, nil
		}
	}

	// 2) start 初始化 hook（当前 no-op，仅保留接口）。
	if err := s.executePMBootstrapEntrypoint(ctx, t, *w); err != nil {
		return nil, err
	}

	// 3) 标记 worker 为 running。
	// ticket 状态由 dispatch/report 流程推进，start 不做业务状态跳变。
	now := time.Now()
	if err := s.worker.MarkWorkerRunning(ctx, w.ID, now); err != nil {
		return nil, fmt.Errorf("标记 worker 为 running 失败（w%d）：%w", w.ID, err)
	}

	// 4) 返回最新 worker，并确保 contract：Start 返回时 worker 必须为 running。
	out, err := s.worker.WorkerByID(ctx, w.ID)
	if err != nil {
		return nil, fmt.Errorf("读取 worker 失败（w%d）：%w", w.ID, err)
	}
	if out.Status != contracts.WorkerRunning {
		retryAt := time.Now()
		if err := s.worker.MarkWorkerRunning(ctx, w.ID, retryAt); err != nil {
			return nil, fmt.Errorf("start 后 worker 未进入 running（w%d status=%s），重试失败：%w", out.ID, out.Status, err)
		}
		out, err = s.worker.WorkerByID(ctx, w.ID)
		if err != nil {
			return nil, fmt.Errorf("读取 worker 失败（w%d）：%w", w.ID, err)
		}
		if out.Status != contracts.WorkerRunning {
			return nil, fmt.Errorf("start 后 worker 未进入 running（w%d status=%s）", out.ID, out.Status)
		}
	}
	if err := s.promoteTicketQueuedOnStart(ctx, db, t, *out); err != nil {
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
		res := tx.WithContext(ctx).Model(&contracts.Ticket{}).
			Where("id = ? AND (workflow_status = ? OR TRIM(COALESCE(workflow_status, '')) = '')", t.ID, contracts.TicketBacklog).
			Updates(map[string]any{
				"workflow_status": contracts.TicketQueued,
				"updated_at":      now,
			})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return nil
		}
		from := contracts.CanonicalTicketWorkflowStatus(t.WorkflowStatus)
		if from == "" {
			from = contracts.TicketBacklog
		}
		return s.appendTicketWorkflowEventTx(ctx, tx, t.ID, from, contracts.TicketQueued, "pm.start", "start 推进到 queued", map[string]any{
			"ticket_id": t.ID,
			"worker_id": w.ID,
		}, now)
	}); err != nil {
		return fmt.Errorf("更新 ticket workflow 失败（t%d w%d）：%w", t.ID, w.ID, err)
	}
	return nil
}
