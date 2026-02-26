package worker

import (
	"context"
	"dalek/internal/contracts"
	"encoding/json"
	"strings"
	"time"

	"dalek/internal/store"

	"gorm.io/gorm"
)

func normalizeWorkerStatus(st contracts.WorkerStatus) contracts.WorkerStatus {
	v := contracts.WorkerStatus(strings.TrimSpace(strings.ToLower(string(st))))
	if v == "" {
		return st
	}
	return v
}

func marshalWorkerStatusPayload(v any) string {
	if v == nil {
		return ""
	}
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
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
	ev := store.WorkerStatusEvent{
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
