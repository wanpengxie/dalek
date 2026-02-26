package pm

import (
	"context"
	"dalek/internal/contracts"
	"encoding/json"
	"strings"
	"time"

	"gorm.io/gorm"
)

func normalizeTicketWorkflowStatus(st contracts.TicketWorkflowStatus) contracts.TicketWorkflowStatus {
	return contracts.CanonicalTicketWorkflowStatus(st)
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

func (s *Service) appendTicketWorkflowEventTx(ctx context.Context, tx *gorm.DB, ticketID uint, fromStatus, toStatus contracts.TicketWorkflowStatus, source, reason string, payload any, createdAt time.Time) error {
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
	ev := contracts.TicketWorkflowEvent{
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
