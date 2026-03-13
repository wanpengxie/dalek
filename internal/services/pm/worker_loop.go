package pm

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"dalek/internal/contracts"
	"dalek/internal/services/agentexec"
	"dalek/internal/store"
)

// launchWorkerSDK 委托到测试 mock 或真实的 launchWorkerSDKHandle。
func (s *Service) launchWorkerSDK(ctx context.Context, t contracts.Ticket, w contracts.Worker, entryPrompt string) (agentexec.AgentRunHandle, error) {
	if s.sdkHandleLauncher != nil {
		return s.sdkHandleLauncher(ctx, t, w, entryPrompt)
	}
	return s.launchWorkerSDKHandle(ctx, t, w, entryPrompt)
}

// WorkerLoopResult 是 executeWorkerLoop 的返回结果。
type WorkerLoopResult struct {
	Stages         int    // 已闭合或已尝试收口的 stage 数；同一 stage 内 repair run 不额外计数
	LastNextAction string // 最后一次 report 的 next_action
	InjectedCmd    string // 首次启动时的 injected_cmd 标识
	LastRunID      uint   // 最后一轮 stage 对应的 task run id
}

type workerLoopStageStartedFunc func(stage int, runID uint) error

type workerLoopStageResult struct {
	LastRunID      uint
	LastNextAction string
}

// executeWorkerLoop 是 worker SDK 同步执行的核心循环。
//
// 流程：
//  1. 启动 worker SDK handle（launchWorkerSDKHandle）
//  2. handle.Wait() 等待 agent 完成
//  3. 读取本轮 run 在 DB 中的 next_action
//  4. 如果 next_action 为空，补报重试 1 次
//  5. 如果 next_action == "continue"，用"继续执行任务"作为 prompt 重新启动
//  6. 否则（wait_user/done）退出循环；连续两轮空 report 视为异常退出
//  7. 退出时标记 worker 为 stopped；连续两轮空 report 标记为 failed
//
// 无超时限制：agent 可能运行数小时。内部使用 cancel-only context：
// 只透传主动 cancel，不继承调用方 deadline。
func (s *Service) executeWorkerLoop(ctx context.Context, t contracts.Ticket, w contracts.Worker, entryPrompt string) (WorkerLoopResult, error) {
	return s.executeWorkerLoopWithHook(ctx, t, w, entryPrompt, nil)
}

func (s *Service) executeWorkerLoopWithHook(ctx context.Context, t contracts.Ticket, w contracts.Worker, entryPrompt string, onStageStarted workerLoopStageStartedFunc) (WorkerLoopResult, error) {
	if strings.TrimSpace(entryPrompt) == "" {
		entryPrompt = defaultContinuePrompt
	}

	// worker agent 可运行数小时，不应受 dispatch 超时影响；但需要响应主动 cancel。
	loopCtx, stopLoop := newCancelOnlyContext(ctx)
	defer stopLoop()
	sink := workerLoopControlSinkFromContext(loopCtx)

	result := WorkerLoopResult{}
	prompt := strings.TrimSpace(entryPrompt)

	// 推断 injected_cmd 标识
	p, _, _ := s.require()
	if p != nil {
		cfg := p.Config.WithDefaults()
		result.InjectedCmd = "sdk:" + strings.TrimSpace(strings.ToLower(cfg.WorkerAgent.Provider))
	}

	for {
		result.Stages++
		stageResult, err := s.runWorkerLoopStage(loopCtx, t, w, result.Stages, prompt, onStageStarted)
		result.LastRunID = stageResult.LastRunID
		result.LastNextAction = stageResult.LastNextAction
		if err != nil {
			var closureErr *workerLoopClosureExhaustedError
			switch {
			case isWorkerLoopCanceledError(loopCtx, err):
				if sink != nil {
					sink.LoopCancelRequested()
				}
				s.markWorkerLoopExit(loopCtx, w, "")
				return result, fmt.Errorf("worker_loop stage %d 已取消: %w", result.Stages, context.Canceled)
			case errors.As(err, &closureErr):
				s.markWorkerLoopExit(loopCtx, w, "")
				return result, closureErr
			default:
				if sink != nil {
					sink.LoopErrored(err)
				}
				s.markWorkerLoopExit(loopCtx, w, fmt.Sprintf("worker_loop stage failed stage=%d: %v", result.Stages, err))
				return result, err
			}
		}
		if strings.TrimSpace(strings.ToLower(stageResult.LastNextAction)) != string(contracts.NextContinue) {
			break
		}
		prompt = defaultContinuePrompt
	}
	s.markWorkerLoopExit(loopCtx, w, "")
	return result, nil
}

func (s *Service) runWorkerLoopStage(ctx context.Context, t contracts.Ticket, w contracts.Worker, stage int, prompt string, onStageStarted workerLoopStageStartedFunc) (workerLoopStageResult, error) {
	if strings.TrimSpace(prompt) == "" {
		prompt = defaultContinuePrompt
	}
	currentPrompt := strings.TrimSpace(prompt)
	repairAttempts := 0
	sink := workerLoopControlSinkFromContext(ctx)
	for {
		handle, err := s.launchWorkerSDK(ctx, t, w, currentPrompt)
		if err != nil {
			if isWorkerLoopCanceledError(ctx, err) {
				return workerLoopStageResult{}, fmt.Errorf("worker_loop stage %d launch 已取消: %w", stage, context.Canceled)
			}
			return workerLoopStageResult{}, fmt.Errorf("worker_loop stage %d launch 失败: %w", stage, err)
		}
		runID := handle.RunID()
		if sink != nil {
			phase := WorkerLoopPhaseRunning
			if repairAttempts > 0 {
				phase = WorkerLoopPhaseRepairing
			}
			sink.LoopRunAttached(runID, w.ID, phase)
		}
		if repairAttempts == 0 && onStageStarted != nil {
			if err := onStageStarted(stage, runID); err != nil {
				if isWorkerLoopCanceledError(ctx, err) {
					return workerLoopStageResult{LastRunID: runID}, fmt.Errorf("worker_loop stage %d start 已取消: %w", stage, context.Canceled)
				}
				return workerLoopStageResult{LastRunID: runID}, fmt.Errorf("worker_loop stage %d start hook 失败: %w", stage, err)
			}
		}
		_ = s.worker.AppendWorkerTaskEvent(ctx, w.ID, "worker_loop_stage_start",
			fmt.Sprintf("stage=%d run_id=%d repair_attempt=%d", stage, runID, repairAttempts),
			map[string]any{
				"stage":          stage,
				"run_id":         runID,
				"repair_attempt": repairAttempts,
			}, time.Now())

		runResult, waitErr := handle.Wait(ctx)
		if waitErr != nil && isWorkerLoopCanceledError(ctx, waitErr) {
			return workerLoopStageResult{LastRunID: runID}, fmt.Errorf("worker_loop stage %d wait 已取消: %w", stage, context.Canceled)
		}

		nextAction := ""
		if report, found, err := s.loadWorkerLoopCandidateReport(ctx, t.ID, w, runID); err != nil {
			return workerLoopStageResult{LastRunID: runID}, fmt.Errorf("worker_loop stage %d 读取 closure report 失败: %w", stage, err)
		} else if found {
			nextAction = strings.TrimSpace(strings.ToLower(report.NextAction))
		}

		donePayload := map[string]any{
			"stage":          stage,
			"run_id":         runID,
			"repair_attempt": repairAttempts,
			"next_action":    nextAction,
		}
		note := fmt.Sprintf("stage=%d run_id=%d repair_attempt=%d next_action=%s", stage, runID, repairAttempts, nextAction)
		if waitErr != nil {
			donePayload["wait_error"] = strings.TrimSpace(waitErr.Error())
			note = fmt.Sprintf("%s wait_error=%s", note, strings.TrimSpace(waitErr.Error()))
		} else {
			donePayload["exit_code"] = runResult.ExitCode
			note = fmt.Sprintf("%s exit_code=%d", note, runResult.ExitCode)
		}
		_ = s.worker.AppendWorkerTaskEvent(ctx, w.ID, "worker_loop_stage_done", note, donePayload, time.Now())

		if waitErr == nil && nextAction == string(contracts.NextContinue) {
			return workerLoopStageResult{
				LastRunID:      runID,
				LastNextAction: nextAction,
			}, nil
		}
		if sink != nil {
			sink.LoopClosing()
		}

		decision, err := s.evaluateWorkerLoopStageClosure(ctx, t.ID, w, runID, waitErr)
		if err != nil {
			return workerLoopStageResult{LastRunID: runID, LastNextAction: nextAction}, fmt.Errorf("worker_loop stage %d closure check 失败: %w", stage, err)
		}
		if decision.Accepted {
			return workerLoopStageResult{
				LastRunID:      runID,
				LastNextAction: decision.NextAction,
			}, nil
		}
		if repairAttempts < defaultWorkerLoopClosureRepairAttempts && decision.Repairable {
			repairAttempts++
			_ = s.worker.AppendWorkerTaskEvent(ctx, w.ID, "worker_loop_closure_repair_requested",
				fmt.Sprintf("stage=%d run_id=%d reason=%s", stage, runID, decision.ReasonCode),
				map[string]any{
					"stage":          stage,
					"run_id":         runID,
					"repair_attempt": repairAttempts,
					"reason":         decision.ReasonCode,
					"issues":         decision.Issues,
				}, time.Now())
			currentPrompt = buildWorkerLoopClosureRepairPrompt(decision)
			continue
		}
		_ = s.worker.AppendWorkerTaskEvent(ctx, w.ID, "worker_loop_closure_exhausted",
			fmt.Sprintf("stage=%d run_id=%d reason=%s", stage, runID, decision.ReasonCode),
			map[string]any{
				"stage":          stage,
				"run_id":         runID,
				"repair_attempt": repairAttempts,
				"reason":         decision.ReasonCode,
				"issues":         decision.Issues,
			}, time.Now())
		return workerLoopStageResult{
				LastRunID:      runID,
				LastNextAction: decision.NextAction,
			}, &workerLoopClosureExhaustedError{
				Stage:     stage,
				LastRunID: runID,
				Decision:  decision,
			}
	}
}

func isWorkerLoopCanceledError(ctx context.Context, err error) bool {
	if errors.Is(err, context.Canceled) {
		return true
	}
	return ctx != nil && errors.Is(ctx.Err(), context.Canceled)
}

// readWorkerNextActionFromRun 读取指定 run 的最新 semantic_next_action。
// 返回空字符串表示该轮没有上报可用 next_action。
func (s *Service) readWorkerNextActionFromRun(ctx context.Context, runID uint) string {
	_, db, err := s.require()
	if err != nil {
		return ""
	}
	if runID == 0 {
		return ""
	}
	if ctx == nil {
		ctx = context.Background()
	}
	var status store.TaskStatusView
	if err := db.WithContext(ctx).Model(&store.TaskStatusView{}).Where("run_id = ?", runID).First(&status).Error; err != nil {
		return ""
	}
	return strings.TrimSpace(strings.ToLower(status.SemanticNextAction))
}

// markWorkerLoopExit 在 worker loop 退出时标记 worker 状态。
// lastError 非空表示异常退出（标记 failed），空表示正常退出（标记 stopped）。
// 使用独立短超时 context 确保即使调用方 context 已 cancel，状态也能写入。
func (s *Service) markWorkerLoopExit(ctx context.Context, w contracts.Worker, lastError string) {
	// 如果调用方 context 已被 cancel（如 SIGHUP 信号导致），使用独立 context 完成状态写入。
	writeCtx := ctx
	if ctx.Err() != nil {
		var cancel context.CancelFunc
		writeCtx, cancel = context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		slog.Info("worker_loop: parent context canceled, using independent context for cleanup",
			"worker_id", w.ID, "parent_err", ctx.Err())
	}

	now := time.Now()
	if strings.TrimSpace(lastError) != "" {
		slog.Warn("worker_loop: marking worker as failed on exit", "worker_id", w.ID, "error", lastError)
		_ = s.worker.MarkWorkerFailed(writeCtx, w.ID, now, lastError)
	} else {
		slog.Info("worker_loop: marking worker runtime as stopped on exit", "worker_id", w.ID)
		_ = s.worker.MarkWorkerRuntimeNotAlive(writeCtx, w, now)
	}
}
