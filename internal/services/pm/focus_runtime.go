package pm

import (
	"context"

	"dalek/internal/contracts"
)

// FocusLoopControl 抽象 daemon 当前进程持有的 ticket loop 取消能力。
// controller 用它在 desired_state=canceling 时优先终止 live loop，再回写 durable 状态。
type FocusLoopControl interface {
	CancelTaskRun(ctx context.Context, runID uint, cause contracts.TaskCancelCause) error
	CancelTicketLoop(ctx context.Context, ticketID uint) error
}
