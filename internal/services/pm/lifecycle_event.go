package pm

import (
	"context"
	"fmt"

	"dalek/internal/contracts"
	"dalek/internal/services/ticketlifecycle"

	"gorm.io/gorm"
)

func (s *Service) appendTicketLifecycleEventTx(ctx context.Context, tx *gorm.DB, input ticketlifecycle.AppendInput) error {
	_, _, err := ticketlifecycle.AppendEventTx(ctx, tx, input)
	return err
}

func latestWorkerTaskRunIDTx(ctx context.Context, tx *gorm.DB, workerID uint) (uint, error) {
	if tx == nil || workerID == 0 {
		return 0, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	var run contracts.TaskRun
	if err := tx.WithContext(ctx).
		Where("owner_type = ? AND worker_id = ?", contracts.TaskOwnerWorker, workerID).
		Order("id desc").
		First(&run).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return 0, nil
		}
		return 0, err
	}
	return run.ID, nil
}

func lifecycleRepairPayload(targetWorkflow contracts.TicketWorkflowStatus, targetIntegration contracts.IntegrationStatus, extra map[string]any) map[string]any {
	out := map[string]any{}
	if targetWorkflow != "" {
		out["target_workflow"] = string(targetWorkflow)
	}
	if targetIntegration != "" {
		out["target_integration"] = string(targetIntegration)
	}
	for k, v := range extra {
		out[k] = v
	}
	return out
}

func lifecycleIdempotencyKeyOrError(key string) error {
	if key == "" {
		return fmt.Errorf("lifecycle idempotency key 不能为空")
	}
	return nil
}
