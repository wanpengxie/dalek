package pm

import (
	"context"
	"fmt"
	"strings"
	"time"

	"dalek/internal/contracts"
	"dalek/internal/store"
)

// WorkerLoopResult 是 executeWorkerLoop 的返回结果。
type WorkerLoopResult struct {
	Stages         int    // 执行的 stage 数（每次 agent 启动-等待-消费 report 算一个 stage）
	LastNextAction string // 最后一次 report 的 next_action
	InjectedCmd    string // 首次启动时的 injected_cmd 标识
	LastRunID      uint   // 最后一轮 stage 对应的 task run id
}

// executeWorkerLoop 是 worker SDK 同步执行的核心循环。
//
// 流程：
//  1. 启动 worker SDK handle（launchWorkerSDKHandle）
//  2. handle.Wait() 等待 agent 完成
//  3. 读取本轮 run 在 DB 中的 next_action
//  4. 如果 next_action == "continue"，用"继续执行任务"作为 prompt 重新启动
//  5. 否则（wait_user/done/空）退出循环
//  6. 退出时标记 worker 为 stopped
//
// 无超时限制：agent 可能运行数小时。内部使用 cancel-only context：
// 只透传主动 cancel，不继承调用方 deadline。
func (s *Service) executeWorkerLoop(ctx context.Context, t contracts.Ticket, w contracts.Worker, entryPrompt string) (WorkerLoopResult, error) {
	if strings.TrimSpace(entryPrompt) == "" {
		entryPrompt = defaultContinuePrompt
	}

	// worker agent 可运行数小时，不应受 dispatch 超时影响；但需要响应主动 cancel。
	loopCtx, stopLoop := newCancelOnlyContext(ctx)
	defer stopLoop()

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

		// 1) 启动 worker SDK handle
		handle, err := s.launchWorkerSDKHandle(loopCtx, t, w, prompt)
		if err != nil {
			s.markWorkerLoopExit(loopCtx, w, fmt.Sprintf("worker_loop launch failed stage=%d: %v", result.Stages, err))
			return result, fmt.Errorf("worker_loop stage %d launch 失败: %w", result.Stages, err)
		}

		// 记录 stage 启动事件
		result.LastRunID = handle.RunID()
		_ = s.worker.AppendWorkerTaskEvent(loopCtx, w.ID, "worker_loop_stage_start",
			fmt.Sprintf("stage=%d run_id=%d", result.Stages, handle.RunID()),
			map[string]any{
				"stage":  result.Stages,
				"run_id": handle.RunID(),
			}, time.Now())

		// 2) 等待 agent 完成（无超时）
		runResult, waitErr := handle.Wait()
		if waitErr != nil {
			s.markWorkerLoopExit(loopCtx, w, fmt.Sprintf("worker_loop wait failed stage=%d: %v", result.Stages, waitErr))
			return result, fmt.Errorf("worker_loop stage %d wait 失败: %w", result.Stages, waitErr)
		}

		// 3) 读取本轮 run 在 DB 中的 next_action（由 agent 通过 worker report 上报）。
		nextAction := s.readWorkerNextActionFromRun(loopCtx, handle.RunID())
		result.LastNextAction = nextAction

		// 记录 stage 完成事件
		_ = s.worker.AppendWorkerTaskEvent(loopCtx, w.ID, "worker_loop_stage_done",
			fmt.Sprintf("stage=%d exit_code=%d next_action=%s", result.Stages, runResult.ExitCode, nextAction),
			map[string]any{
				"stage":       result.Stages,
				"exit_code":   runResult.ExitCode,
				"next_action": nextAction,
			}, time.Now())

		// 4) 判断是否继续
		if strings.TrimSpace(strings.ToLower(nextAction)) != string(contracts.NextContinue) {
			break
		}

		// 5) 用默认 prompt 继续下一轮
		prompt = defaultContinuePrompt
	}

	// 6) 退出时标记 worker 为 stopped
	s.markWorkerLoopExit(loopCtx, w, "")

	return result, nil
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
func (s *Service) markWorkerLoopExit(ctx context.Context, w contracts.Worker, lastError string) {
	now := time.Now()
	if strings.TrimSpace(lastError) != "" {
		_ = s.worker.MarkWorkerFailed(ctx, w.ID, now, lastError)
	} else {
		_ = s.worker.MarkWorkerSessionNotAlive(ctx, w, now)
	}
}
