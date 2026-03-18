package agentexec

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"dalek/internal/agent/progresstimeout"
	"dalek/internal/contracts"

	"errors"
)

// RunLifecycleTracker 统一管理 TaskRun 生命周期：create -> running -> terminal state。
type RunLifecycleTracker struct {
	base  BaseConfig
	runID uint
}

func NewRunLifecycleTracker(base BaseConfig) *RunLifecycleTracker {
	return &RunLifecycleTracker{base: base}
}

func (t *RunLifecycleTracker) RunID() uint {
	if t == nil {
		return 0
	}
	return t.runID
}

func (t *RunLifecycleTracker) Start(ctx context.Context, requestPayloadJSON, runnerID string, lease *time.Time, note string) (uint, error) {
	runID, err := t.CreatePending(ctx, requestPayloadJSON)
	if err != nil {
		return 0, err
	}
	if err := t.MarkRunning(ctx, runnerID, lease, note); err != nil {
		return 0, err
	}
	return runID, nil
}

func (t *RunLifecycleTracker) CreatePending(ctx context.Context, requestPayloadJSON string) (uint, error) {
	if t == nil || t.base.Runtime == nil {
		return 0, nil
	}
	ctx = ensureContext(ctx)

	run, err := t.base.Runtime.CreateRun(ctx, t.base.createRunInput(requestPayloadJSON))
	if err != nil {
		return 0, err
	}
	t.runID = run.ID
	return t.runID, nil
}

func (t *RunLifecycleTracker) MarkRunning(ctx context.Context, runnerID string, lease *time.Time, note string) error {
	if t == nil || t.base.Runtime == nil || t.runID == 0 {
		return nil
	}
	ctx = ensureContext(ctx)

	now := time.Now()
	runnerID = strings.TrimSpace(runnerID)
	if err := t.base.Runtime.MarkRunRunning(ctx, t.runID, runnerID, lease, now, true); err != nil {
		_ = markProcessRunFailed(t.base.Runtime, t.runID, "agent_mark_running_failed", err.Error())
		return err
	}
	_ = t.base.Runtime.AppendEvent(ctx, contracts.TaskEventInput{
		TaskRunID: t.runID,
		EventType: "task_started",
		ToState: map[string]any{
			"orchestration_state": contracts.TaskRunning,
			"runner_id":           runnerID,
		},
		Note:      strings.TrimSpace(note),
		CreatedAt: now,
	})
	return nil
}

// finishWriteTimeout 是 Finish 方法中 DB 写入的最大超时时间。
// 使用短超时替代 context.Background()，确保即使进程正在退出，
// 清理写入也能在有限时间内完成或超时，不会阻塞进程退出。
const finishWriteTimeout = 10 * time.Second

func (t *RunLifecycleTracker) Finish(ctx context.Context, result AgentRunResult, execErr error, successNote string) {
	if t == nil || t.base.Runtime == nil || t.runID == 0 {
		return
	}
	// 使用独立的短超时 context 而非 context.Background()：
	// 保证清理写入不会被调用方 cancel 打断（不继承 parent），
	// 但也不会无限阻塞（有 finishWriteTimeout 兜底）。
	writeCtx, writeCancel := context.WithTimeout(context.Background(), finishWriteTimeout)
	defer writeCancel()

	now := time.Now()
	if execErr == nil && result.ExitCode == 0 {
		if err := t.base.Runtime.MarkRunSucceeded(writeCtx, t.runID, marshalJSON(result), now); err != nil {
			slog.Warn("lifecycle_tracker: MarkRunSucceeded failed during cleanup", "run_id", t.runID, "err", err)
		}
		_ = t.base.Runtime.AppendEvent(writeCtx, contracts.TaskEventInput{
			TaskRunID: t.runID,
			EventType: "task_succeeded",
			FromState: map[string]any{"orchestration_state": contracts.TaskRunning},
			ToState:   map[string]any{"orchestration_state": contracts.TaskSucceeded},
			Note:      strings.TrimSpace(successNote),
			CreatedAt: now,
		})
		slog.Debug("lifecycle_tracker: run finished successfully", "run_id", t.runID)
		return
	}

	msg := strings.TrimSpace(errStringWithOutput(execErr, result.Stdout, result.Stderr))
	if msg == "" {
		msg = "agent 执行失败"
	}
	if progresstimeout.Is(execErr) {
		if err := t.base.Runtime.MarkRunFailed(writeCtx, t.runID, "agent_timeout", msg, now); err != nil {
			slog.Warn("lifecycle_tracker: MarkRunFailed timeout failed during cleanup", "run_id", t.runID, "err", err)
		}
		_ = t.base.Runtime.AppendEvent(writeCtx, contracts.TaskEventInput{
			TaskRunID: t.runID,
			EventType: "task_failed",
			FromState: map[string]any{"orchestration_state": contracts.TaskRunning},
			ToState: map[string]any{
				"orchestration_state": contracts.TaskFailed,
				"error_code":          "agent_timeout",
			},
			Note:      msg,
			CreatedAt: now,
		})
		slog.Info("lifecycle_tracker: run timed out", "run_id", t.runID, "msg", msg)
		return
	}
	if isCanceledError(execErr) || isCanceledError(contextErr(ctx)) {
		cancelCause := contracts.TaskCancelCauseFromError(context.Cause(ctx))
		if summary := cancelCause.Summary(); summary != "" {
			switch strings.TrimSpace(msg) {
			case "", context.Canceled.Error(), context.DeadlineExceeded.Error():
				msg = summary
			default:
				if !strings.Contains(msg, summary) {
					msg = summary + ": " + msg
				}
			}
		}
		errorCode := cancelCause.ErrorCode()
		if err := t.base.Runtime.MarkRunCanceled(writeCtx, t.runID, errorCode, msg, now); err != nil {
			slog.Warn("lifecycle_tracker: MarkRunCanceled failed during cleanup", "run_id", t.runID, "err", err)
		}
		toState := map[string]any{
			"orchestration_state": contracts.TaskCanceled,
			"error_code":          errorCode,
		}
		payload := map[string]any{}
		if cancelCause.Valid() {
			toState["cancel_cause"] = string(cancelCause)
			payload["cancel_cause"] = string(cancelCause)
		}
		_ = t.base.Runtime.AppendEvent(writeCtx, contracts.TaskEventInput{
			TaskRunID: t.runID,
			EventType: "task_canceled",
			FromState: map[string]any{"orchestration_state": contracts.TaskRunning},
			ToState:   toState,
			Note:      msg,
			Payload:   payload,
			CreatedAt: now,
		})
		slog.Info("lifecycle_tracker: run canceled", "run_id", t.runID, "msg", msg)
		return
	}

	if err := t.base.Runtime.MarkRunFailed(writeCtx, t.runID, "agent_exit_failed", msg, now); err != nil {
		slog.Warn("lifecycle_tracker: MarkRunFailed failed during cleanup", "run_id", t.runID, "err", err)
	}
	_ = t.base.Runtime.AppendEvent(writeCtx, contracts.TaskEventInput{
		TaskRunID: t.runID,
		EventType: "task_failed",
		FromState: map[string]any{"orchestration_state": contracts.TaskRunning},
		ToState:   map[string]any{"orchestration_state": contracts.TaskFailed},
		Note:      msg,
		CreatedAt: now,
	})
	slog.Info("lifecycle_tracker: run failed", "run_id", t.runID, "msg", msg)
}

func ensureContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

func contextErr(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}

func isCanceledError(err error) bool {
	if progresstimeout.Is(err) {
		return false
	}
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}
