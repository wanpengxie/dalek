package pm

import (
	"context"
	"fmt"
	"strings"
	"time"

	"dalek/internal/store"

	"gorm.io/gorm"
)

type ListInboxOptions struct {
	Status store.InboxStatus
	Limit  int
}

func (s *Service) ListInbox(ctx context.Context, opt ListInboxOptions) ([]store.InboxItem, error) {
	_, db, err := s.require()
	if err != nil {
		return nil, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	st := opt.Status
	if strings.TrimSpace(string(st)) == "" {
		st = store.InboxOpen
	}
	limit := opt.Limit
	if limit <= 0 {
		limit = 200
	}
	if limit > 2000 {
		limit = 2000
	}

	var items []store.InboxItem
	if err := db.WithContext(ctx).
		Where("status = ?", st).
		// severity 是字符串；这里用 CASE 显式排序，避免 blocker/warn/info 的字典序误排。
		Order("CASE severity WHEN 'blocker' THEN 3 WHEN 'warn' THEN 2 WHEN 'info' THEN 1 ELSE 0 END desc").
		Order("updated_at desc").
		Order("id desc").
		Limit(limit).
		Find(&items).Error; err != nil {
		return nil, err
	}
	return items, nil
}

func (s *Service) GetInboxItem(ctx context.Context, id uint) (*store.InboxItem, error) {
	_, db, err := s.require()
	if err != nil {
		return nil, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	var it store.InboxItem
	if err := db.WithContext(ctx).First(&it, id).Error; err != nil {
		return nil, err
	}
	return &it, nil
}

func (s *Service) CloseInboxItem(ctx context.Context, id uint) error {
	_, db, err := s.require()
	if err != nil {
		return err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	var it store.InboxItem
	if err := db.WithContext(ctx).First(&it, id).Error; err != nil {
		return err
	}
	if it.Status == store.InboxDone {
		return nil
	}
	now := time.Now()
	return db.WithContext(ctx).Model(&store.InboxItem{}).Where("id = ?", id).Updates(map[string]any{
		"status":     store.InboxDone,
		"closed_at":  &now,
		"updated_at": now,
	}).Error
}

func (s *Service) SnoozeInboxItem(ctx context.Context, id uint, until time.Time) error {
	_, db, err := s.require()
	if err != nil {
		return err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if until.IsZero() {
		return fmt.Errorf("until 不能为空")
	}
	var it store.InboxItem
	if err := db.WithContext(ctx).First(&it, id).Error; err != nil {
		return err
	}
	if it.Status == store.InboxDone {
		return nil
	}
	now := time.Now()
	return db.WithContext(ctx).Model(&store.InboxItem{}).Where("id = ?", id).Updates(map[string]any{
		"status":        store.InboxSnoozed,
		"snoozed_until": &until,
		"updated_at":    now,
	}).Error
}

func (s *Service) UnsnoozeInboxItem(ctx context.Context, id uint) error {
	_, db, err := s.require()
	if err != nil {
		return err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	var it store.InboxItem
	if err := db.WithContext(ctx).First(&it, id).Error; err != nil {
		return err
	}
	if it.Status != store.InboxSnoozed {
		return nil
	}
	now := time.Now()
	return db.WithContext(ctx).Model(&store.InboxItem{}).Where("id = ?", id).Updates(map[string]any{
		"status":        store.InboxOpen,
		"snoozed_until": nil,
		"updated_at":    now,
	}).Error
}

func (s *Service) DeleteInboxItem(ctx context.Context, id uint) error {
	// v0：给调试用；正常产品形态可能不提供硬删除。
	_, db, err := s.require()
	if err != nil {
		return err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return db.WithContext(ctx).Delete(&store.InboxItem{}, id).Error
}

func (s *Service) ensureInboxUniqueOpenKey(ctx context.Context, key string) error {
	_, db, err := s.require()
	if err != nil {
		return err
	}
	// 尽量保证同一个 key 的 open 只有一条；不是强一致约束（不引入唯一索引）。
	// 在单进程 manager tick 下足够；未来若多进程并发，可再加锁/唯一索引。
	if ctx == nil {
		ctx = context.Background()
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return nil
	}
	var items []store.InboxItem
	if err := db.WithContext(ctx).
		Where("key = ? AND status = ?", key, store.InboxOpen).
		Order("id desc").
		Find(&items).Error; err != nil {
		return err
	}
	if len(items) <= 1 {
		return nil
	}
	keep := items[0].ID
	var ids []uint
	for _, it := range items[1:] {
		ids = append(ids, it.ID)
	}
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now := time.Now()
		if err := tx.Model(&store.InboxItem{}).Where("id = ?", keep).Update("updated_at", now).Error; err != nil {
			return err
		}
		return tx.Delete(&store.InboxItem{}, ids).Error
	})
}
