package worker

import (
	"context"
	"dalek/internal/contracts"
	"strings"
	"time"

	"gorm.io/gorm"
)

func normalizeWorkerStatus(st contracts.WorkerStatus) contracts.WorkerStatus {
	v := contracts.WorkerStatus(strings.TrimSpace(strings.ToLower(string(st))))
	if v == "" {
		return st
	}
	return v
}

func marshalWorkerStatusPayload(v any) contracts.JSONMap {
	return contracts.JSONMapFromAny(v)
}

func (s *Service) appendWorkerStatusEventTx(ctx context.Context, tx *gorm.DB, workerID uint, ticketID uint, fromStatus, toStatus contracts.WorkerStatus, source, reason string, payload any, createdAt time.Time) error {
	if tx == nil || workerID == 0 {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	fromStatus = normalizeWorkerStatus(fromStatus)
	toStatus = normalizeWorkerStatus(toStatus)
	if fromStatus == toStatus {
		return nil
	}
	if createdAt.IsZero() {
		createdAt = time.Now()
	}
	ev := contracts.WorkerStatusEvent{
		CreatedAt:   createdAt,
		WorkerID:    workerID,
		TicketID:    ticketID,
		FromStatus:  fromStatus,
		ToStatus:    toStatus,
		Source:      strings.TrimSpace(source),
		Reason:      strings.TrimSpace(reason),
		PayloadJSON: marshalWorkerStatusPayload(payload),
	}
	return tx.WithContext(ctx).Create(&ev).Error
}
