package pm

import (
	"context"
	"fmt"
	"time"
)

// StopTicket 在 PM 层收敛 ticket stop 编排。
func (s *Service) StopTicket(ctx context.Context, ticketID uint) error {
	if s == nil || s.worker == nil {
		return fmt.Errorf("pm service 缺少 worker service")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	now := time.Now()
	reason := legacyDispatchCleanupReason(ticketID)
	_, legacyErr := s.cancelLegacyDispatchRuns(ctx, nil, ticketID, 0, reason, now)
	workerErr := s.worker.StopTicket(ctx, ticketID)
	switch {
	case legacyErr != nil && workerErr != nil:
		return fmt.Errorf("%w; legacy dispatch cleanup failed: %v", workerErr, legacyErr)
	case workerErr != nil:
		return workerErr
	case legacyErr != nil:
		return fmt.Errorf("legacy dispatch cleanup failed: %w", legacyErr)
	default:
		return nil
	}
}
