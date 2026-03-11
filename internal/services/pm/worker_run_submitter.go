package pm

import "context"

// WorkerRunSubmitter 抽象“把 ticket 激活为受管 deliver_ticket run”的能力。
// 典型注入方是 daemon 运行时；未注入时 ManagerTick 会保留当前的 blocker 行为。
type WorkerRunSubmitter interface {
	SubmitTicketWorkerRun(ctx context.Context, ticketID uint) error
}
