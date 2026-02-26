package agentexec

import (
	"context"
	"dalek/internal/contracts"
	"dalek/internal/services/core"
	"errors"
	"strings"
	"time"
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
	_ = t.base.Runtime.AppendEvent(ctx, core.TaskRuntimeEventInput{
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

func (t *RunLifecycleTracker) Finish(ctx context.Context, result AgentRunResult, execErr error, successNote string) {
	if t == nil || t.base.Runtime == nil || t.runID == 0 {
		return
	}
	writeCtx := context.Background()
	now := time.Now()
	if execErr == nil && result.ExitCode == 0 {
		_ = t.base.Runtime.MarkRunSucceeded(writeCtx, t.runID, marshalJSON(result), now)
		_ = t.base.Runtime.AppendEvent(writeCtx, core.TaskRuntimeEventInput{
			TaskRunID: t.runID,
			EventType: "task_succeeded",
			FromState: map[string]any{"orchestration_state": contracts.TaskRunning},
			ToState:   map[string]any{"orchestration_state": contracts.TaskSucceeded},
			Note:      strings.TrimSpace(successNote),
			CreatedAt: now,
		})
		return
	}

	msg := strings.TrimSpace(errStringWithOutput(execErr, result.Stdout, result.Stderr))
	if msg == "" {
		msg = "agent 执行失败"
	}
	if isCanceledError(execErr) || isCanceledError(contextErr(ctx)) {
		_ = t.base.Runtime.MarkRunCanceled(writeCtx, t.runID, "agent_canceled", msg, now)
		_ = t.base.Runtime.AppendEvent(writeCtx, core.TaskRuntimeEventInput{
			TaskRunID: t.runID,
			EventType: "task_canceled",
			FromState: map[string]any{"orchestration_state": contracts.TaskRunning},
			ToState:   map[string]any{"orchestration_state": contracts.TaskCanceled},
			Note:      msg,
			CreatedAt: now,
		})
		return
	}

	_ = t.base.Runtime.MarkRunFailed(writeCtx, t.runID, "agent_exit_failed", msg, now)
	_ = t.base.Runtime.AppendEvent(writeCtx, core.TaskRuntimeEventInput{
		TaskRunID: t.runID,
		EventType: "task_failed",
		FromState: map[string]any{"orchestration_state": contracts.TaskRunning},
		ToState:   map[string]any{"orchestration_state": contracts.TaskFailed},
		Note:      msg,
		CreatedAt: now,
	})
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
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}
