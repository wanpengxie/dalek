package pm

import (
	"context"
	"dalek/internal/contracts"
	"fmt"
	"strings"
	"time"

	"dalek/internal/store"

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
	p, db, err := s.require()
	if err != nil {
		return DirectDispatchResult{}, err
	}
	if ctx == nil {
		ctx = context.Background()
	}

	var t store.Ticket
	if err := db.WithContext(ctx).First(&t, ticketID).Error; err != nil {
		return DirectDispatchResult{}, err
	}
	if t.WorkflowStatus == contracts.TicketArchived {
		return DirectDispatchResult{}, fmt.Errorf("ticket 已归档：t%d", ticketID)
	}
	if t.WorkflowStatus == contracts.TicketDone {
		return DirectDispatchResult{}, fmt.Errorf("ticket 已完成：t%d", ticketID)
	}

	autoStart := dispatchAutoStartEnabled(opt.AutoStart)
	w, err := s.worker.LatestWorker(ctx, ticketID)
	if err != nil {
		return DirectDispatchResult{}, err
	}
	if autoStart && (w == nil || strings.TrimSpace(w.TmuxSession) == "") {
		w, err = s.ensureDispatchWorkerStarted(ctx, ticketID)
		if err != nil {
			return DirectDispatchResult{}, err
		}
	}
	if w == nil || strings.TrimSpace(w.TmuxSession) == "" {
		return DirectDispatchResult{}, fmt.Errorf("该 ticket 尚未启动（没有 worker/session），请先按 s 或运行 start")
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

	if w.Status == contracts.WorkerStopped {
		socket := strings.TrimSpace(w.TmuxSocket)
		if socket == "" {
			socket = strings.TrimSpace(p.Config.WithDefaults().TmuxSocket)
		}
		session := strings.TrimSpace(w.TmuxSession)
		if p.Tmux != nil && socket != "" && session != "" {
			listCtx, cancel := context.WithTimeout(ctx, tmuxListSessionsTimeout)
			sessions, lerr := p.Tmux.ListSessions(listCtx, socket)
			cancel()
			if lerr == nil && !sessions[session] {
				if autoStart {
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
				} else {
					return DirectDispatchResult{}, fmt.Errorf("worker session 不在线（w%d session=%s），请先 start", w.ID, session)
				}
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
	prevWorkflow := normalizeTicketWorkflowStatus(t.WorkflowStatus)
	if prevWorkflow != contracts.TicketActive {
		now := time.Now()
		if err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			res := tx.WithContext(ctx).Model(&store.Ticket{}).
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
		var refreshed store.Ticket
		if rerr := db.WithContext(ctx).Select("workflow_status").First(&refreshed, ticketID).Error; rerr == nil {
			workflowPromoted = normalizeTicketWorkflowStatus(refreshed.WorkflowStatus) == contracts.TicketActive && prevWorkflow != contracts.TicketActive
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
		now := time.Now()
		failErr := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			// best-effort：仅在 workflow 仍为 active 时回滚，避免覆盖并发 report 推进后的状态。
			if workflowPromoted {
				res := tx.WithContext(ctx).Model(&store.Ticket{}).
					Where("id = ? AND workflow_status = ?", ticketID, contracts.TicketActive).
					Updates(map[string]any{
						"workflow_status": prevWorkflow,
						"updated_at":      now,
					})
				if res.Error != nil {
					return res.Error
				}
				if res.RowsAffected > 0 {
					if err := s.appendTicketWorkflowEventTx(ctx, tx, ticketID, contracts.TicketActive, prevWorkflow, "pm.direct_dispatch", "direct dispatch 失败回滚 workflow", map[string]any{
						"ticket_id": ticketID,
						"worker_id": w.ID,
						"error":     loopErrMsg,
					}, now); err != nil {
						return err
					}
				}
			}
			_, uerr := s.upsertOpenInboxTx(ctx, tx, store.InboxItem{
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
