package pm

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"dalek/internal/store"

	"gorm.io/gorm"
)

func normalizeTicketWorkflowStatus(st store.TicketWorkflowStatus) store.TicketWorkflowStatus {
	return store.CanonicalTicketWorkflowStatus(st)
}

func marshalWorkflowEventPayload(v any) string {
	if v == nil {
		return ""
	}
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func (s *Service) appendTicketWorkflowEventTx(ctx context.Context, tx *gorm.DB, ticketID uint, fromStatus, toStatus store.TicketWorkflowStatus, source, reason string, payload any, createdAt time.Time) error {
	if tx == nil || ticketID == 0 {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	fromStatus = normalizeTicketWorkflowStatus(fromStatus)
	toStatus = normalizeTicketWorkflowStatus(toStatus)
	if fromStatus == toStatus {
		return nil
	}
	if createdAt.IsZero() {
		createdAt = time.Now()
	}
	ev := store.TicketWorkflowEvent{
		CreatedAt:   createdAt,
		TicketID:    ticketID,
		FromStatus:  fromStatus,
		ToStatus:    toStatus,
		Source:      strings.TrimSpace(source),
		Reason:      strings.TrimSpace(reason),
		PayloadJSON: marshalWorkflowEventPayload(payload),
	}
	return tx.WithContext(ctx).Create(&ev).Error
}
