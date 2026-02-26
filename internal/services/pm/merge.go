package pm

import (
	"context"
	"dalek/internal/contracts"
	"fmt"
	"strings"
	"time"

	"dalek/internal/store"

	"gorm.io/gorm"
)

type ListMergeOptions struct {
	Status contracts.MergeStatus
	Limit  int
}

func mergeTerminalStatuses() []contracts.MergeStatus {
	return []contracts.MergeStatus{contracts.MergeMerged, contracts.MergeDiscarded}
}

func (s *Service) ListMergeItems(ctx context.Context, opt ListMergeOptions) ([]store.MergeItem, error) {
	_, db, err := s.require()
	if err != nil {
		return nil, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	limit := opt.Limit
	if limit <= 0 {
		limit = 200
	}
	if limit > 2000 {
		limit = 2000
	}

	q := db.WithContext(ctx).Model(&store.MergeItem{})
	if strings.TrimSpace(string(opt.Status)) != "" {
		q = q.Where("status = ?", opt.Status)
	}
	var items []store.MergeItem
	if err := q.Order("updated_at desc").Order("id desc").Limit(limit).Find(&items).Error; err != nil {
		return nil, err
	}
	return items, nil
}

func (s *Service) ProposeMerge(ctx context.Context, ticketID uint) (*store.MergeItem, error) {
	_, db, err := s.require()
	if err != nil {
		return nil, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	var t store.Ticket
	if err := db.WithContext(ctx).First(&t, ticketID).Error; err != nil {
		return nil, err
	}
	w, err := s.worker.LatestWorker(ctx, ticketID)
	if err != nil {
		return nil, err
	}
	if w == nil || strings.TrimSpace(w.Branch) == "" {
		return nil, fmt.Errorf("worker 分支为空：t%d（请先 start）", ticketID)
	}

	// 防重复：已有非终态 item 就返回最新的那条
	var existing store.MergeItem
	if err := db.WithContext(ctx).
		Where("ticket_id = ? AND status NOT IN ?", ticketID, mergeTerminalStatuses()).
		Order("id desc").
		First(&existing).Error; err == nil {
		return &existing, nil
	}

	mi := store.MergeItem{
		Status:   contracts.MergeProposed,
		TicketID: ticketID,
		WorkerID: w.ID,
		Branch:   strings.TrimSpace(w.Branch),
	}
	if err := db.WithContext(ctx).Create(&mi).Error; err != nil {
		return nil, err
	}

	// 写 inbox：请求审批（低风险：只写记录，不做 merge）
	_, _ = s.upsertOpenInbox(ctx, store.InboxItem{
		Key:         inboxKeyMergeApproval(mi.ID),
		Status:      contracts.InboxOpen,
		Severity:    contracts.InboxWarn,
		Reason:      contracts.InboxApprovalRequired,
		Title:       fmt.Sprintf("待合并审批：t%d", ticketID),
		Body:        fmt.Sprintf("merge_item=%d  branch=%s\n\n请确认是否允许合并，以及合并策略（squash/merge）。", mi.ID, strings.TrimSpace(mi.Branch)),
		TicketID:    ticketID,
		MergeItemID: mi.ID,
	})

	return &mi, nil
}

func (s *Service) ApproveMerge(ctx context.Context, mergeItemID uint, approvedBy string) error {
	_, db, err := s.require()
	if err != nil {
		return err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	approvedBy = strings.TrimSpace(approvedBy)
	if approvedBy == "" {
		approvedBy = "cto"
	}
	var mi store.MergeItem
	if err := db.WithContext(ctx).First(&mi, mergeItemID).Error; err != nil {
		return err
	}
	if mi.Status == contracts.MergeMerged || mi.Status == contracts.MergeDiscarded {
		return nil
	}
	now := time.Now()
	return db.WithContext(ctx).Model(&store.MergeItem{}).Where("id = ?", mergeItemID).Updates(map[string]any{
		"status":      contracts.MergeApproved,
		"approved_by": approvedBy,
		"approved_at": &now,
		"updated_at":  now,
	}).Error
}

func (s *Service) DiscardMerge(ctx context.Context, mergeItemID uint, _ string) error {
	_, db, err := s.require()
	if err != nil {
		return err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	var mi store.MergeItem
	if err := db.WithContext(ctx).First(&mi, mergeItemID).Error; err != nil {
		return err
	}
	if mi.Status == contracts.MergeDiscarded {
		return nil
	}
	if mi.Status == contracts.MergeMerged {
		return fmt.Errorf("merge#%d 已 merged，不能 discard", mergeItemID)
	}

	now := time.Now()
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.WithContext(ctx).Model(&store.MergeItem{}).Where("id = ?", mergeItemID).Updates(map[string]any{
			"status":     contracts.MergeDiscarded,
			"updated_at": now,
		}).Error; err != nil {
			return err
		}
		if err := tx.WithContext(ctx).Model(&store.InboxItem{}).
			Where("merge_item_id = ? AND status = ?", mergeItemID, contracts.InboxOpen).
			Updates(map[string]any{
				"status":     contracts.InboxDone,
				"closed_at":  &now,
				"updated_at": now,
			}).Error; err != nil {
			return err
		}
		return nil
	})
}

func (s *Service) MarkMergeMerged(ctx context.Context, mergeItemID uint) error {
	_, db, err := s.require()
	if err != nil {
		return err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	var mi store.MergeItem
	if err := db.WithContext(ctx).First(&mi, mergeItemID).Error; err != nil {
		return err
	}
	if mi.Status == contracts.MergeDiscarded {
		return fmt.Errorf("merge#%d 已 discarded，不能标记 merged", mergeItemID)
	}
	now := time.Now()
	var archiveErr error
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if mi.Status != contracts.MergeMerged {
			if err := tx.WithContext(ctx).Model(&store.MergeItem{}).Where("id = ?", mergeItemID).Updates(map[string]any{
				"status":     contracts.MergeMerged,
				"merged_at":  &now,
				"updated_at": now,
			}).Error; err != nil {
				return err
			}
		}
		if err := tx.WithContext(ctx).Model(&store.InboxItem{}).
			Where("merge_item_id = ? AND status = ?", mergeItemID, contracts.InboxOpen).
			Updates(map[string]any{
				"status":     contracts.InboxDone,
				"closed_at":  &now,
				"updated_at": now,
			}).Error; err != nil {
			return err
		}

		// merge=merged 时尽量推进 workflow=archived（需满足：无 running worker，避免隐藏活跃资源）。
		if mi.TicketID != 0 {
			var cnt int64
			if err := tx.WithContext(ctx).Model(&store.Worker{}).
				Where("ticket_id = ? AND status = ?", mi.TicketID, contracts.WorkerRunning).
				Count(&cnt).Error; err != nil {
				return err
			}
			if cnt == 0 {
				res := tx.WithContext(ctx).Model(&store.Ticket{}).
					Where("id = ? AND workflow_status = ?", mi.TicketID, contracts.TicketDone).
					Updates(map[string]any{
						"workflow_status": contracts.TicketArchived,
						"updated_at":      now,
					})
				if res.Error != nil {
					archiveErr = res.Error
				} else if res.RowsAffected > 0 {
					if err := s.appendTicketWorkflowEventTx(ctx, tx, mi.TicketID, contracts.TicketDone, contracts.TicketArchived, "pm.merge", "merge 完成后自动归档", map[string]any{
						"merge_item_id": mergeItemID,
						"ticket_id":     mi.TicketID,
					}, now); err != nil {
						archiveErr = err
					}
				}
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	if archiveErr != nil && mi.TicketID != 0 {
		// 不阻塞 merge 标记：归档失败作为 inbox incident 暴露给用户处理。
		key := inboxKeyTicketIncident(mi.TicketID, "archive_failed_after_merge")
		title := fmt.Sprintf("归档失败（merge 已完成）：t%d", mi.TicketID)
		body := archiveErr.Error()
		_, _ = s.upsertOpenInbox(ctx, store.InboxItem{
			Key:         key,
			Status:      contracts.InboxOpen,
			Severity:    contracts.InboxWarn,
			Reason:      contracts.InboxIncident,
			Title:       title,
			Body:        body,
			TicketID:    mi.TicketID,
			MergeItemID: mi.ID,
		})
	}
	return nil
}
