package pm

import (
	"context"
	"dalek/internal/contracts"
	"dalek/internal/fsm"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
)

// DirectDispatchOptions 控制直接 worker 派发的行为。
type DirectDispatchOptions struct {
	// EntryPrompt 为空时默认 "继续执行任务"。
	EntryPrompt string
	// AutoStart=nil/true 时，dispatch 发现 worker 未就绪会先自动 start。
	AutoStart *bool
}

// DirectDispatchResult 是 DirectDispatchWorker 的返回结果。
type DirectDispatchResult struct {
	TicketID       uint
	WorkerID       uint
	Stages         int
	LastNextAction string
	LastRunID      uint
}

// DirectDispatchWorker 跳过 PM agent，直接启动 worker SDK 同步循环。
//
// 使用场景：
//   - 用户解决了 agent 上报的 block 因素后，手动继续执行
//   - 用户在现有 ticket 上追加新的需求
//
// 与 DispatchTicket 的区别：不执行 PM dispatch agent（不生成契约文件），直接进入 worker loop。
func (s *Service) DirectDispatchWorker(ctx context.Context, ticketID uint, opt DirectDispatchOptions) (DirectDispatchResult, error) {
	_, db, err := s.require()
	if err != nil {
		return DirectDispatchResult{}, err
	}
	if ctx == nil {
		ctx = context.Background()
	}

	var t contracts.Ticket
	if err := db.WithContext(ctx).First(&t, ticketID).Error; err != nil {
		return DirectDispatchResult{}, err
	}
	if !fsm.CanDispatchTicket(t.WorkflowStatus) {
		switch contracts.CanonicalTicketWorkflowStatus(t.WorkflowStatus) {
		case contracts.TicketArchived:
			return DirectDispatchResult{}, fmt.Errorf("ticket 已归档：t%d", ticketID)
		default:
			return DirectDispatchResult{}, fmt.Errorf("ticket 已完成：t%d", ticketID)
		}
	}

	autoStart := dispatchAutoStartEnabled(opt.AutoStart)
	w, err := s.worker.LatestWorker(ctx, ticketID)
	if err != nil {
		return DirectDispatchResult{}, err
	}
	ready, rerr := s.workerDispatchReady(ctx, w)
	if rerr != nil {
		return DirectDispatchResult{}, rerr
	}
	if autoStart && (w == nil || !ready) {
		w, err = s.ensureDispatchWorkerStarted(ctx, ticketID)
		if err != nil {
			return DirectDispatchResult{}, err
		}
	}
	if w == nil {
		return DirectDispatchResult{}, s.workerMissingSessionError()
	}
	if w.Status == contracts.WorkerCreating {
		ready, waitErr := s.waitWorkerReadyForDispatch(ctx, ticketID, w)
		if waitErr != nil {
			return DirectDispatchResult{}, waitErr
		}
		w = ready
	}
	if autoStart && w.Status != contracts.WorkerRunning && w.Status != contracts.WorkerStopped {
		w, err = s.ensureDispatchWorkerStarted(ctx, ticketID)
		if err != nil {
			return DirectDispatchResult{}, err
		}
		if w != nil && w.Status == contracts.WorkerCreating {
			ready, waitErr := s.waitWorkerReadyForDispatch(ctx, ticketID, w)
			if waitErr != nil {
				return DirectDispatchResult{}, waitErr
			}
			w = ready
		}
	}
	if w.Status != contracts.WorkerRunning && w.Status != contracts.WorkerStopped {
		return DirectDispatchResult{}, fmt.Errorf("该 ticket 的最新 worker 不在 running（w%d status=%s），请重新启动", w.ID, w.Status)
	}
	ready, rerr = s.workerDispatchReady(ctx, w)
	if rerr != nil {
		return DirectDispatchResult{}, rerr
	}
	if !ready && w.Status == contracts.WorkerRunning {
		if autoStart {
			w, err = s.ensureDispatchWorkerStarted(ctx, ticketID)
			if err != nil {
				return DirectDispatchResult{}, err
			}
			ready, rerr = s.workerDispatchReady(ctx, w)
			if rerr != nil {
				return DirectDispatchResult{}, rerr
			}
		}
		if !ready {
			return DirectDispatchResult{}, fmt.Errorf("worker runtime/session 不在线（w%d），请先 start", w.ID)
		}
	}

	if w.Status == contracts.WorkerStopped {
		live, lerr := s.workerDispatchLive(ctx, w)
		if lerr != nil {
			return DirectDispatchResult{}, lerr
		}
		if !live {
			if autoStart {
				w, err = s.ensureDispatchWorkerStarted(ctx, ticketID)
				if err != nil {
					return DirectDispatchResult{}, err
				}
				if w != nil && w.Status == contracts.WorkerCreating {
					readyWorker, waitErr := s.waitWorkerReadyForDispatch(ctx, ticketID, w)
					if waitErr != nil {
						return DirectDispatchResult{}, waitErr
					}
					w = readyWorker
				}
				live, lerr = s.workerDispatchLive(ctx, w)
				if lerr != nil {
					return DirectDispatchResult{}, lerr
				}
			}
			if !live {
				return DirectDispatchResult{}, fmt.Errorf("worker runtime/session 不在线（w%d），请先 start", w.ID)
			}
		}
		if w.Status == contracts.WorkerStopped {
			if err := s.worker.MarkWorkerRunning(ctx, w.ID, time.Now()); err != nil {
				return DirectDispatchResult{}, fmt.Errorf("恢复 worker 运行态失败（w%d）：%w", w.ID, err)
			}
		}
	}

	entryPrompt := strings.TrimSpace(opt.EntryPrompt)
	if entryPrompt == "" {
		entryPrompt = defaultContinuePrompt
	}

	// 先推进到 active，失败时回滚，避免残留 active 悬挂态。
	workflowPromoted := false
	prevWorkflow := contracts.CanonicalTicketWorkflowStatus(t.WorkflowStatus)
	if prevWorkflow != contracts.TicketActive {
		now := time.Now()
		if err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			res := tx.WithContext(ctx).Model(&contracts.Ticket{}).
				Where("id = ? AND workflow_status != ? AND workflow_status != ?", ticketID, contracts.TicketDone, contracts.TicketArchived).
				Updates(map[string]any{
					"workflow_status": contracts.TicketActive,
					"updated_at":      now,
				})
			if res.Error != nil {
				return res.Error
			}
			if res.RowsAffected == 0 {
				return nil
			}
			return s.appendTicketWorkflowEventTx(ctx, tx, ticketID, prevWorkflow, contracts.TicketActive, "pm.direct_dispatch", "direct dispatch 开始", map[string]any{
				"ticket_id":    ticketID,
				"worker_id":    w.ID,
				"entry_prompt": entryPrompt,
			}, now)
		}); err != nil {
			return DirectDispatchResult{}, fmt.Errorf("更新 ticket workflow 失败（t%d）：%w", ticketID, err)
		}
		var refreshed contracts.Ticket
		if rerr := db.WithContext(ctx).Select("workflow_status").First(&refreshed, ticketID).Error; rerr == nil {
			workflowPromoted = contracts.CanonicalTicketWorkflowStatus(refreshed.WorkflowStatus) == contracts.TicketActive && prevWorkflow != contracts.TicketActive
		}
	}

	// 记录直接派发事件
	_ = s.worker.AppendWorkerTaskEvent(ctx, w.ID, "direct_dispatch_start",
		fmt.Sprintf("direct dispatch t%d w%d", ticketID, w.ID),
		map[string]any{
			"ticket_id":    ticketID,
			"entry_prompt": entryPrompt,
		}, time.Now())

	loopResult, err := s.executeWorkerLoop(ctx, t, *w, entryPrompt)
	if err != nil {
		loopErrMsg := strings.TrimSpace(err.Error())
		rollbackTarget := prevWorkflow
		if !fsm.CanTicketWorkflowTransition(contracts.TicketActive, rollbackTarget) {
			rollbackTarget = contracts.TicketBlocked
		}
		now := time.Now()
		failErr := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			// best-effort：仅在 workflow 仍为 active 时回滚，避免覆盖并发 report 推进后的状态。
			if workflowPromoted {
				res := tx.WithContext(ctx).Model(&contracts.Ticket{}).
					Where("id = ? AND workflow_status = ?", ticketID, contracts.TicketActive).
					Updates(map[string]any{
						"workflow_status": rollbackTarget,
						"updated_at":      now,
					})
				if res.Error != nil {
					return res.Error
				}
				if res.RowsAffected > 0 {
					reason := "direct dispatch 失败回滚 workflow"
					if rollbackTarget != prevWorkflow {
						reason = "direct dispatch 失败回滚 workflow（回退目标非法，已降级到 blocked）"
					}
					if err := s.appendTicketWorkflowEventTx(ctx, tx, ticketID, contracts.TicketActive, rollbackTarget, "pm.direct_dispatch", reason, map[string]any{
						"ticket_id":         ticketID,
						"worker_id":         w.ID,
						"error":             loopErrMsg,
						"previous_workflow": prevWorkflow,
						"rollback_target":   rollbackTarget,
					}, now); err != nil {
						return err
					}
				}
			}
			_, uerr := s.upsertOpenInboxTx(ctx, tx, contracts.InboxItem{
				Key:      inboxKeyWorkerIncident(w.ID, "direct_dispatch_failed"),
				Status:   contracts.InboxOpen,
				Severity: contracts.InboxWarn,
				Reason:   contracts.InboxIncident,
				Title:    fmt.Sprintf("直接派发失败：t%d w%d", ticketID, w.ID),
				Body:     loopErrMsg,
				TicketID: ticketID,
				WorkerID: w.ID,
			})
			return uerr
		})
		if failErr != nil {
			return DirectDispatchResult{}, fmt.Errorf("%w（且写入失败 inbox 失败: %v）", err, failErr)
		}
		return DirectDispatchResult{}, err
	}

	return DirectDispatchResult{
		TicketID:       ticketID,
		WorkerID:       w.ID,
		Stages:         loopResult.Stages,
		LastNextAction: strings.TrimSpace(loopResult.LastNextAction),
		LastRunID:      loopResult.LastRunID,
	}, nil
}
