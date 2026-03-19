package pm

import (
	"context"
	"strings"
	"time"

	"dalek/internal/contracts"
)

// handlerTerminationInput 是所有 handler 终止事件的统一消费入口参数。
// 所有 terminal event（task_canceled / worker_loop_terminated / zombie 检测）
// 提取 cause 后调 convergeHandlerTermination，由此函数统一路由到
// convergeUserInitiatedTaskCancel 或 convergeExecutionLost。
type handlerTerminationInput struct {
	TicketID  uint
	WorkerID  uint
	TaskRunID uint
	Cause     contracts.TaskCancelCause // 唯一路由依据
	Source    string                    // 审计来源
	Reason    string                    // 人类可读描述
	EventID   uint
	Now       time.Time

	// 以下字段仅在非 user-initiated 路径使用，传递给 convergeExecutionLost。
	ObservationKind string
	FailureCode     string
	Payload         map[string]any
}

// convergeHandlerTermination 是 handler 终止事件的统一消费入口。
// 根据 cause 路由：
//   - user_stop / user_interrupt / user_cancel → convergeUserInitiatedTaskCancel（ticket blocked，不重试）
//   - focus_cancel / daemon_shutdown / superseded / unknown / crash → convergeExecutionLost（重试或 block）
func (s *Service) convergeHandlerTermination(ctx context.Context, input handlerTerminationInput) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if input.Now.IsZero() {
		input.Now = time.Now()
	}
	input.Source = strings.TrimSpace(input.Source)
	if input.Source == "" {
		input.Source = "pm.handler_termination"
	}
	input.Reason = strings.TrimSpace(input.Reason)

	if isUserInitiatedTaskCancelCause(input.Cause) {
		if input.Reason == "" {
			input.Reason = userInitiatedTaskCancelSummary(input.Cause)
		}
		_, err := s.convergeUserInitiatedTaskCancel(ctx, userInitiatedTaskCancelInput{
			TicketID:  input.TicketID,
			WorkerID:  input.WorkerID,
			TaskRunID: input.TaskRunID,
			Cause:     input.Cause,
			Source:    input.Source,
			Reason:    input.Reason,
			EventID:   input.EventID,
			Now:       input.Now,
		})
		return err
	}

	// 非 user-initiated：crash / daemon_shutdown / focus_cancel / superseded / unknown
	if input.Reason == "" {
		input.Reason = "handler terminated"
	}
	failureCode := strings.TrimSpace(input.FailureCode)
	if failureCode == "" {
		if input.Cause.Valid() {
			failureCode = string(input.Cause)
		} else {
			failureCode = "execution_lost"
		}
	}
	observationKind := strings.TrimSpace(input.ObservationKind)
	if observationKind == "" {
		observationKind = "handler_terminated"
	}

	_, err := s.convergeExecutionLost(ctx, executionLossInput{
		TicketID:        input.TicketID,
		WorkerID:        input.WorkerID,
		TaskRunID:       input.TaskRunID,
		Source:          input.Source,
		ObservationKind: observationKind,
		FailureCode:     failureCode,
		Reason:          input.Reason,
		Payload:         input.Payload,
		Now:             input.Now,
	})
	return err
}
