package snapshot

import (
	"context"
	"fmt"
	"os"
	"time"
)

type GCService struct {
	catalog *Catalog
	store   *FileStore
	now     func() time.Time
}

type GCResult struct {
	Checked int
	Expired int
	Removed int
}

func NewGCService(catalog *Catalog, store *FileStore) *GCService {
	return &GCService{
		catalog: catalog,
		store:   store,
		now:     time.Now,
	}
}

func (s *GCService) Sweep(ctx context.Context, limit int) (GCResult, error) {
	if s == nil || s.catalog == nil {
		return GCResult{}, fmt.Errorf("snapshot gc service 未初始化")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	items, err := s.catalog.ListGarbageEligible(ctx, s.now(), limit)
	if err != nil {
		return GCResult{}, err
	}
	result := GCResult{Checked: len(items)}
	for _, item := range items {
		if err := s.catalog.MarkExpired(ctx, item.SnapshotID); err != nil {
			return result, err
		}
		result.Expired++
		if s.store == nil {
			continue
		}
		dir, derr := s.store.SnapshotDir(item.SnapshotID)
		if derr != nil {
			return result, derr
		}
		if err := os.RemoveAll(dir); err != nil {
			return result, err
		}
		result.Removed++
	}
	return result, nil
}
