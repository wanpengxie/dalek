package pm

import (
	"context"
	"fmt"
)

// StopTicket 在 PM 层收敛 ticket stop 编排：
// 1) 停止 worker；2) 终结仍处于 pending/running 的 dispatch job。
func (s *Service) StopTicket(ctx context.Context, ticketID uint) error {
	if s == nil || s.worker == nil {
		return fmt.Errorf("pm service 缺少 worker service")
	}
	stopErr := s.worker.StopTicket(ctx, ticketID)
	_, dispatchErr := s.ForceFailActiveDispatchesForTicket(ctx, ticketID, "ticket stop: force fail active dispatch")

	if stopErr != nil && dispatchErr != nil {
		return fmt.Errorf("%w；另外 dispatch 终结失败: %v", stopErr, dispatchErr)
	}
	if stopErr != nil {
		return stopErr
	}
	return dispatchErr
}
