package pm

import (
	"context"
	"dalek/internal/contracts"
	"dalek/internal/fsm"
	"errors"
	"fmt"
	"strings"
	"time"
)

// DirectDispatchOptions 控制直接 worker 派发的行为。
type DirectDispatchOptions struct {
	// EntryPrompt 为空时默认 "继续执行任务"。
	EntryPrompt string
	// AutoStart=nil/true 时，dispatch 发现 worker 未就绪会先自动 start。
	AutoStart *bool
	// BaseBranch 非空时，auto-start worker 会优先从该基线创建/修复 worktree。
	BaseBranch string
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
	if !fsm.CanQueueRunTicket(t.WorkflowStatus) {
		switch contracts.CanonicalTicketWorkflowStatus(t.WorkflowStatus) {
		case contracts.TicketArchived:
			return DirectDispatchResult{}, fmt.Errorf("ticket 已归档：t%d", ticketID)
		default:
			return DirectDispatchResult{}, fmt.Errorf("ticket 已完成：t%d", ticketID)
		}
	}

	autoStart := dispatchAutoStartEnabled(opt.AutoStart)
	baseBranch := strings.TrimSpace(opt.BaseBranch)
	w, err := s.worker.LatestWorker(ctx, ticketID)
	if err != nil {
		return DirectDispatchResult{}, err
	}
	ready, rerr := s.workerDispatchReady(ctx, w)
	if rerr != nil {
		return DirectDispatchResult{}, rerr
	}
	if autoStart && (w == nil || !ready) {
		w, err = s.ensureDispatchWorkerStarted(ctx, ticketID, baseBranch)
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
		w, err = s.ensureDispatchWorkerStarted(ctx, ticketID, baseBranch)
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
	if !ready {
		if autoStart {
			w, err = s.ensureDispatchWorkerStarted(ctx, ticketID, baseBranch)
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
			ready, rerr = s.workerDispatchReady(ctx, w)
			if rerr != nil {
				return DirectDispatchResult{}, rerr
			}
		}
		if !ready {
			return DirectDispatchResult{}, fmt.Errorf("worker runtime/session 不在线（w%d），请先 start", w.ID)
		}
	}

	entryPrompt := strings.TrimSpace(opt.EntryPrompt)
	if entryPrompt == "" {
		entryPrompt = defaultContinuePrompt
	}
	if err := db.WithContext(ctx).First(&t, ticketID).Error; err != nil {
		return DirectDispatchResult{}, err
	}
	if _, err := s.ensureWorkerBootstrap(ctx, t, *w, entryPrompt); err != nil {
		return DirectDispatchResult{}, err
	}

	workflowPromoted := false

	// 记录直接派发事件
	_ = s.worker.AppendWorkerTaskEvent(ctx, w.ID, "direct_dispatch_start",
		fmt.Sprintf("direct dispatch t%d w%d", ticketID, w.ID),
		map[string]any{
			"ticket_id":    ticketID,
			"entry_prompt": entryPrompt,
		}, time.Now())

	loopResult, err := s.executeWorkerLoopWithHook(ctx, t, *w, entryPrompt, func(stage int, runID uint) error {
		if stage != 1 || runID == 0 {
			return nil
		}
		activated, actErr := s.acceptWorkerRun(ctx, ticketID, w, runID, "pm.direct_dispatch", contracts.TicketLifecycleActorUser, map[string]any{
			"ticket_id":    ticketID,
			"worker_id":    w.ID,
			"entry_prompt": entryPrompt,
		})
		workflowPromoted = workflowPromoted || activated
		if actErr != nil {
			return fmt.Errorf("更新 ticket workflow 失败（t%d run=%d）：%w", ticketID, runID, actErr)
		}
		return nil
	})
	if err != nil {
		var missingErr *workerLoopMissingReportError
		if errors.As(err, &missingErr) {
			if applyErr := s.applyMissingWorkerReportWaitUser(ctx, t.ID, *w, loopResult, "pm.direct_dispatch.missing_report"); applyErr == nil {
				return DirectDispatchResult{
					TicketID:       ticketID,
					WorkerID:       w.ID,
					Stages:         loopResult.Stages,
					LastNextAction: string(contracts.NextWaitUser),
					LastRunID:      loopResult.LastRunID,
				}, nil
			} else {
				err = fmt.Errorf("worker loop 缺少 report，自动 blocked 失败: %w", applyErr)
			}
		}
		loopErrMsg := strings.TrimSpace(err.Error())
		if workflowPromoted {
			if _, cerr := s.convergeExecutionLost(ctx, executionLossInput{
				TicketID:        ticketID,
				WorkerID:        w.ID,
				TaskRunID:       loopResult.LastRunID,
				Source:          "pm.direct_dispatch",
				ObservationKind: "unexpected_exit",
				FailureCode:     "worker_loop_failed",
				Reason:          loopErrMsg,
				Payload: map[string]any{
					"loop_stage_count": loopResult.Stages,
				},
				Now: time.Now(),
			}); cerr != nil {
				return DirectDispatchResult{}, fmt.Errorf("%w（且 execution 收敛失败: %v）", err, cerr)
			}
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
