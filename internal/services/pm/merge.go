package pm

import (
	"context"
	"dalek/internal/contracts"
	"strings"
)

type ListMergeOptions struct {
	Status contracts.MergeStatus
	Limit  int
}

func (s *Service) ListMergeItems(ctx context.Context, opt ListMergeOptions) ([]contracts.MergeItem, error) {
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

	q := db.WithContext(ctx).Model(&contracts.MergeItem{})
	if strings.TrimSpace(string(opt.Status)) != "" {
		q = q.Where("status = ?", opt.Status)
	}
	var items []contracts.MergeItem
	if err := q.Order("updated_at desc").Order("id desc").Limit(limit).Find(&items).Error; err != nil {
		return nil, err
	}
	return items, nil
}
