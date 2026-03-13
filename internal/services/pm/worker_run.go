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

type WorkerRunOptions struct {
	EntryPrompt string
	AutoStart   *bool
	BaseBranch  string
}

type WorkerRunResult struct {
	TicketID       uint
	WorkerID       uint
	RunID          uint
	Stages         int
	LastNextAction string
}

func (s *Service) RunTicketWorker(ctx context.Context, ticketID uint, opt WorkerRunOptions) (WorkerRunResult, error) {
	_, db, err := s.require()
	if err != nil {
		return WorkerRunResult{}, err
	}
	if ctx == nil {
		ctx = context.Background()
	}

	var t contracts.Ticket
	if err := db.WithContext(ctx).First(&t, ticketID).Error; err != nil {
		return WorkerRunResult{}, err
	}
	if !fsm.CanQueueRunTicket(t.WorkflowStatus) {
		switch contracts.CanonicalTicketWorkflowStatus(t.WorkflowStatus) {
		case contracts.TicketArchived:
			return WorkerRunResult{}, fmt.Errorf("ticket 已归档：t%d", ticketID)
		default:
			return WorkerRunResult{}, fmt.Errorf("ticket 已完成：t%d", ticketID)
		}
	}

	autoStart := workerRunAutoStartEnabled(opt.AutoStart)
	baseBranch := strings.TrimSpace(opt.BaseBranch)
	t, w, err := s.resolveWorkerRunTarget(ctx, ticketID, autoStart, baseBranch)
	if err != nil {
		return WorkerRunResult{}, err
	}
	if sink := workerLoopControlSinkFromContext(ctx); sink != nil {
		sink.LoopClaimed(t.ID, w.ID)
	}

	entryPrompt := strings.TrimSpace(opt.EntryPrompt)
	if entryPrompt == "" {
		entryPrompt = defaultContinuePrompt
	}
	bootstrapMode, err := s.determineWorkerBootstrapMode(ctx, w.ID)
	if err != nil {
		return WorkerRunResult{}, fmt.Errorf("判定 worker bootstrap 模式失败（w%d）：%w", w.ID, err)
	}
	if _, err := s.ensureWorkerBootstrap(ctx, t, *w, entryPrompt, bootstrapMode); err != nil {
		return WorkerRunResult{}, err
	}

	workflowPromoted := false
	_ = s.worker.AppendWorkerTaskEvent(ctx, w.ID, "worker_run_start",
		fmt.Sprintf("worker run t%d w%d", ticketID, w.ID),
		map[string]any{
			"ticket_id":    ticketID,
			"entry_prompt": entryPrompt,
		}, time.Now())

	loopResult, err := s.executeWorkerLoopWithHook(ctx, t, *w, entryPrompt, func(stage int, runID uint) error {
		if stage != 1 || runID == 0 {
			return nil
		}
		activated, actErr := s.acceptWorkerRun(ctx, ticketID, w, runID, "pm.worker_run", contracts.TicketLifecycleActorUser, map[string]any{
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
		var closureErr *workerLoopClosureExhaustedError
		if errors.As(err, &closureErr) {
			if applyErr := s.applyWorkerLoopClosureFallback(ctx, t.ID, *w, loopResult, closureErr.Decision, "pm.worker_run.closure"); applyErr == nil {
				return WorkerRunResult{
					TicketID:       ticketID,
					WorkerID:       w.ID,
					RunID:          loopResult.LastRunID,
					Stages:         loopResult.Stages,
					LastNextAction: string(contracts.NextWaitUser),
				}, nil
			} else {
				return WorkerRunResult{}, fmt.Errorf("worker loop closure fallback 失败（closure=%s）: %w", strings.TrimSpace(closureErr.Error()), applyErr)
			}
		}
		loopErrMsg := strings.TrimSpace(err.Error())
		if workflowPromoted {
			canceledExit, cerr := s.isCanceledWorkerLoopTermination(ctx, loopResult.LastRunID, err)
			if cerr != nil {
				return WorkerRunResult{}, fmt.Errorf("%w（且读取取消状态失败: %v）", err, cerr)
			}
			if !canceledExit {
				if _, cerr := s.convergeExecutionLost(ctx, executionLossInput{
					TicketID:        ticketID,
					WorkerID:        w.ID,
					TaskRunID:       loopResult.LastRunID,
					Source:          "pm.worker_run",
					ObservationKind: "unexpected_exit",
					FailureCode:     "worker_loop_failed",
					Reason:          loopErrMsg,
					Payload: map[string]any{
						"loop_stage_count": loopResult.Stages,
					},
					Now: time.Now(),
				}); cerr != nil {
					return WorkerRunResult{}, fmt.Errorf("%w（且 execution 收敛失败: %v）", err, cerr)
				}
			}
		}
		return WorkerRunResult{}, err
	}
	if closeErr := s.applyWorkerLoopTerminalClosure(ctx, t.ID, *w, loopResult, "pm.worker_run"); closeErr != nil {
		return WorkerRunResult{}, closeErr
	}

	return WorkerRunResult{
		TicketID:       ticketID,
		WorkerID:       w.ID,
		RunID:          loopResult.LastRunID,
		Stages:         loopResult.Stages,
		LastNextAction: strings.TrimSpace(loopResult.LastNextAction),
	}, nil
}

func (s *Service) applyWorkerLoopClosureFallback(ctx context.Context, ticketID uint, w contracts.Worker, loopResult WorkerLoopResult, decision workerLoopStageClosureDecision, source string) error {
	if s != nil && s.workerLoopClosureFallbackApplier != nil {
		return s.workerLoopClosureFallbackApplier(ctx, ticketID, w, loopResult, decision, source)
	}
	return s.applyWorkerLoopClosureFallbackWaitUser(ctx, ticketID, w, loopResult, decision, source)
}

func workerRunAutoStartEnabled(v *bool) bool {
	if v == nil {
		return true
	}
	return *v
}

func (s *Service) isCanceledWorkerLoopTermination(ctx context.Context, runID uint, loopErr error) (bool, error) {
	if !isWorkerLoopCanceledError(ctx, loopErr) || runID == 0 {
		return false, nil
	}
	_, db, err := s.require()
	if err != nil {
		return false, err
	}
	queryBase := context.Background()
	if ctx != nil {
		queryBase = context.WithoutCancel(ctx)
	}
	queryCtx, cancel := context.WithTimeout(queryBase, 5*time.Second)
	defer cancel()

	var run contracts.TaskRun
	if err := db.WithContext(queryCtx).
		Select("id", "orchestration_state").
		First(&run, runID).Error; err != nil {
		return false, err
	}
	return run.OrchestrationState == contracts.TaskCanceled, nil
}

func (s *Service) resolveWorkerRunTarget(ctx context.Context, ticketID uint, autoStart bool, baseBranch string) (contracts.Ticket, *contracts.Worker, error) {
	t, w, err := s.inspectWorkerRunTarget(ctx, ticketID)
	if err != nil {
		return contracts.Ticket{}, nil, err
	}
	ready, rerr := s.workerDispatchReady(ctx, w)
	if rerr != nil {
		return contracts.Ticket{}, nil, rerr
	}
	if autoStart && (w == nil || !ready) {
		w, err = s.ensureWorkerStartedForRun(ctx, ticketID, baseBranch)
		if err != nil {
			return contracts.Ticket{}, nil, err
		}
	}
	if w == nil {
		return contracts.Ticket{}, nil, s.workerMissingSessionError()
	}
	if w.Status == contracts.WorkerCreating {
		readyWorker, waitErr := s.waitWorkerReadyForDispatch(ctx, ticketID, w)
		if waitErr != nil {
			return contracts.Ticket{}, nil, waitErr
		}
		w = readyWorker
	}
	if autoStart && w.Status != contracts.WorkerRunning && w.Status != contracts.WorkerStopped {
		w, err = s.ensureWorkerStartedForRun(ctx, ticketID, baseBranch)
		if err != nil {
			return contracts.Ticket{}, nil, err
		}
		if w != nil && w.Status == contracts.WorkerCreating {
			readyWorker, waitErr := s.waitWorkerReadyForDispatch(ctx, ticketID, w)
			if waitErr != nil {
				return contracts.Ticket{}, nil, waitErr
			}
			w = readyWorker
		}
	}
	ready, rerr = s.workerDispatchReady(ctx, w)
	if rerr != nil {
		return contracts.Ticket{}, nil, rerr
	}
	if !ready {
		if autoStart {
			w, err = s.ensureWorkerStartedForRun(ctx, ticketID, baseBranch)
			if err != nil {
				return contracts.Ticket{}, nil, err
			}
			if w != nil && w.Status == contracts.WorkerCreating {
				readyWorker, waitErr := s.waitWorkerReadyForDispatch(ctx, ticketID, w)
				if waitErr != nil {
					return contracts.Ticket{}, nil, waitErr
				}
				w = readyWorker
			}
			ready, rerr = s.workerDispatchReady(ctx, w)
			if rerr != nil {
				return contracts.Ticket{}, nil, rerr
			}
			if !ready {
				return contracts.Ticket{}, nil, s.workerMissingSessionError()
			}
		} else {
			return contracts.Ticket{}, nil, s.workerMissingSessionError()
		}
	}
	if w.Status != contracts.WorkerRunning && w.Status != contracts.WorkerStopped {
		return contracts.Ticket{}, nil, s.workerNotRunningError(w)
	}
	return t, w, nil
}

func (s *Service) inspectWorkerRunTarget(ctx context.Context, ticketID uint) (contracts.Ticket, *contracts.Worker, error) {
	_, db, err := s.require()
	if err != nil {
		return contracts.Ticket{}, nil, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	var t contracts.Ticket
	if err := db.WithContext(ctx).First(&t, ticketID).Error; err != nil {
		return contracts.Ticket{}, nil, err
	}
	if !fsm.CanQueueRunTicket(t.WorkflowStatus) {
		switch contracts.CanonicalTicketWorkflowStatus(t.WorkflowStatus) {
		case contracts.TicketArchived:
			return contracts.Ticket{}, nil, fmt.Errorf("ticket 已归档，不能启动 worker run：t%d", ticketID)
		default:
			return contracts.Ticket{}, nil, fmt.Errorf("ticket 已完成，不能启动 worker run：t%d", ticketID)
		}
	}
	w, err := s.worker.LatestWorker(ctx, ticketID)
	if err != nil {
		return contracts.Ticket{}, nil, err
	}
	return t, w, nil
}

func (s *Service) ensureWorkerStartedForRun(ctx context.Context, ticketID uint, baseBranch string) (*contracts.Worker, error) {
	// TODO(tech-debt): auto-start 仍通过 pm.StartTicketWithOptions 回到“start 预建资源”语义。
	// 当 start 收敛为纯入队后，这里应直接调用 worker.StartTicketResourcesWithOptions，
	// 避免 worker run 路径再反向依赖 pm.start 的队列语义。
	w, err := s.StartTicketWithOptions(ctx, ticketID, StartOptions{BaseBranch: strings.TrimSpace(baseBranch)})
	if err != nil {
		return nil, fmt.Errorf("auto-start 失败: %w", err)
	}
	if ready, rerr := s.workerDispatchReady(ctx, w); rerr != nil {
		return nil, fmt.Errorf("auto-start 失败: %w", rerr)
	} else if ready {
		return w, nil
	}
	w, err = s.worker.LatestWorker(ctx, ticketID)
	if err != nil {
		return nil, fmt.Errorf("auto-start 失败: %w", err)
	}
	if ready, rerr := s.workerDispatchReady(ctx, w); rerr != nil {
		return nil, fmt.Errorf("auto-start 失败: %w", rerr)
	} else if !ready {
		if w == nil {
			return nil, fmt.Errorf("auto-start 失败: 启动后仍无可用 worker/runtime（t%d）", ticketID)
		}
		return nil, fmt.Errorf("auto-start 失败: 启动后仍无可用 worker/runtime（t%d w%d status=%s）", ticketID, w.ID, w.Status)
	}
	return w, nil
}
