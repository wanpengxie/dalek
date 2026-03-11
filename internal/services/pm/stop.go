package pm

import (
	"context"
	"fmt"
)

// StopTicket 在 PM 层收敛 ticket stop 编排。
func (s *Service) StopTicket(ctx context.Context, ticketID uint) error {
	if s == nil || s.worker == nil {
		return fmt.Errorf("pm service 缺少 worker service")
	}
	return s.worker.StopTicket(ctx, ticketID)
}
