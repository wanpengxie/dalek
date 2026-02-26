package task

import (
	"context"
	"fmt"
	"strings"
	"time"

	"dalek/internal/contracts"
)

type CancelRunResult struct {
	RunID     uint
	Found     bool
	Canceled  bool
	Reason    string
	FromState string
	ToState   string
}

func (s *Service) FinishAgentRun(ctx context.Context, runID uint, exitCode int, now time.Time) error {
	if s == nil {
		return fmt.Errorf("task service 为空")
	}
	if runID == 0 {
		return fmt.Errorf("run_id 不能为空")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if now.IsZero() {
		now = time.Now()
	}
	if exitCode == 0 {
		if err := s.MarkRunSucceeded(ctx, runID, "", now); err != nil {
			return err
		}
		return s.AppendEvent(ctx, contracts.TaskEventInput{
			TaskRunID: runID,
			EventType: "task_succeeded",
			FromState: map[string]any{"orchestration_state": contracts.TaskRunning},
			ToState:   map[string]any{"orchestration_state": contracts.TaskSucceeded},
			Note:      "agent finish exit_code=0",
			CreatedAt: now,
		})
	}
	msg := fmt.Sprintf("agent_exit code=%d", exitCode)
	if err := s.MarkRunFailed(ctx, runID, "agent_exit", msg, now); err != nil {
		return err
	}
	return s.AppendEvent(ctx, contracts.TaskEventInput{
		TaskRunID: runID,
		EventType: "task_failed",
		FromState: map[string]any{"orchestration_state": contracts.TaskRunning},
		ToState:   map[string]any{"orchestration_state": contracts.TaskFailed},
		Note:      msg,
		CreatedAt: now,
	})
}

func (s *Service) CancelRun(ctx context.Context, runID uint, now time.Time) (CancelRunResult, error) {
	if s == nil {
		return CancelRunResult{}, fmt.Errorf("task service 为空")
	}
	if runID == 0 {
		return CancelRunResult{}, fmt.Errorf("run_id 不能为空")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if now.IsZero() {
		now = time.Now()
	}

	st, err := s.GetStatusByRunID(ctx, runID)
	if err != nil {
		return CancelRunResult{}, err
	}
	if st == nil {
		return CancelRunResult{
			RunID:    runID,
			Found:    false,
			Canceled: false,
			Reason:   fmt.Sprintf("task run #%d 不存在", runID),
		}, nil
	}

	fromState := strings.TrimSpace(st.OrchestrationState)
	if fromState == "" {
		fromState = "unknown"
	}
	result := CancelRunResult{
		RunID:     runID,
		Found:     true,
		FromState: fromState,
		ToState:   fromState,
	}

	switch strings.ToLower(strings.TrimSpace(st.OrchestrationState)) {
	case string(contracts.TaskSucceeded), string(contracts.TaskFailed), string(contracts.TaskCanceled):
		result.Canceled = false
		result.Reason = fmt.Sprintf("task run 已结束，当前状态=%s", fromState)
		return result, nil
	}

	reason := "canceled by task cancel command"
	if err := s.MarkRunCanceled(ctx, runID, "manual_cancel", reason, now); err != nil {
		return CancelRunResult{}, err
	}
	if err := s.AppendEvent(ctx, contracts.TaskEventInput{
		TaskRunID: runID,
		EventType: "task_canceled",
		FromState: map[string]any{
			"orchestration_state": fromState,
		},
		ToState: map[string]any{
			"orchestration_state": contracts.TaskCanceled,
		},
		Note:      reason,
		Payload:   map[string]any{"source": "dalek task cancel"},
		CreatedAt: now,
	}); err != nil {
		return CancelRunResult{}, err
	}

	result.Canceled = true
	result.ToState = string(contracts.TaskCanceled)
	return result, nil
}
