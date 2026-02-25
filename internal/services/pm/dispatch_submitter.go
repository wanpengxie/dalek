package pm

import "context"

// DispatchSubmitter 抽象“把 ticket dispatch 提交到受管执行器”的能力。
// 典型注入方是 daemon 运行时；未注入时 ManagerTick 会 fallback 到本地 goroutine 路径。
type DispatchSubmitter interface {
	SubmitTicketDispatch(ctx context.Context, ticketID uint) error
}
