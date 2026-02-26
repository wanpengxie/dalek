package pm

import (
	"context"
	"dalek/internal/contracts"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
)

func inboxKeyNeedsUser(workerID uint) string {
	return fmt.Sprintf("needs_user:w%d", workerID)
}

func inboxKeyWorkerIncident(workerID uint, typ string) string {
	typ = strings.TrimSpace(strings.ToLower(typ))
	if typ == "" {
		typ = "incident"
	}
	return fmt.Sprintf("incident:w%d:%s", workerID, typ)
}

func inboxKeyTicketIncident(ticketID uint, typ string) string {
	typ = strings.TrimSpace(strings.ToLower(typ))
	if typ == "" {
		typ = "incident"
	}
	return fmt.Sprintf("incident:t%d:%s", ticketID, typ)
}

func inboxKeyMergeApproval(mergeItemID uint) string {
	return fmt.Sprintf("approval:merge:%d", mergeItemID)
}

func (s *Service) upsertOpenInbox(ctx context.Context, item contracts.InboxItem) (bool, error) {
	_, db, err := s.require()
	if err != nil {
		return false, err
	}
	return upsertOpenInboxWithDB(ctx, db, item)
}

func (s *Service) UpsertOpenInbox(ctx context.Context, item contracts.InboxItem) (bool, error) {
	return s.upsertOpenInbox(ctx, item)
}

func (s *Service) upsertOpenInboxTx(ctx context.Context, tx *gorm.DB, item contracts.InboxItem) (bool, error) {
	if tx == nil {
		return false, fmt.Errorf("tx 不能为空")
	}
	return upsertOpenInboxWithDB(ctx, tx, item)
}

func upsertOpenInboxWithDB(ctx context.Context, db *gorm.DB, item contracts.InboxItem) (bool, error) {
	if db == nil {
		return false, fmt.Errorf("db 不能为空")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	item.Key = strings.TrimSpace(item.Key)
	if item.Key == "" {
		return false, fmt.Errorf("inbox key 不能为空")
	}
	if strings.TrimSpace(string(item.Status)) == "" {
		item.Status = contracts.InboxOpen
	}
	if item.Status != contracts.InboxOpen {
		return false, fmt.Errorf("upsertOpenInbox 只支持 status=open")
	}
	if strings.TrimSpace(string(item.Severity)) == "" {
		item.Severity = contracts.InboxInfo
	}
	if strings.TrimSpace(string(item.Reason)) == "" {
		item.Reason = contracts.InboxQuestion
	}
	if strings.TrimSpace(item.Title) == "" {
		item.Title = item.Key
	}

	var existing contracts.InboxItem
	err := db.WithContext(ctx).
		Where("key = ? AND status = ?", item.Key, contracts.InboxOpen).
		Order("id desc").
		First(&existing).Error
	if err == nil {
		now := time.Now()
		upd := map[string]any{
			"updated_at":    now,
			"severity":      item.Severity,
			"reason":        item.Reason,
			"title":         item.Title,
			"body":          item.Body,
			"ticket_id":     item.TicketID,
			"worker_id":     item.WorkerID,
			"merge_item_id": item.MergeItemID,
		}
		if item.SnoozedUntil != nil {
			upd["snoozed_until"] = item.SnoozedUntil
		}
		return false, db.WithContext(ctx).Model(&contracts.InboxItem{}).Where("id = ?", existing.ID).Updates(upd).Error
	}
	if err != gorm.ErrRecordNotFound {
		return false, err
	}
	item.Status = contracts.InboxOpen
	if err := db.WithContext(ctx).Create(&item).Error; err != nil {
		return false, err
	}
	return true, nil
}
