package snapshot

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"dalek/internal/contracts"

	"gorm.io/gorm"
)

type Catalog struct {
	db  *gorm.DB
	now func() time.Time
}

type CreateInput struct {
	SnapshotID          string
	ProjectKey          string
	NodeName            string
	BaseCommit          string
	WorkspaceGeneration string
	ManifestDigest      string
	ManifestJSON        string
	Status              string
	ArtifactPath        string
	ExpiresAt           *time.Time
}

func NewCatalog(db *gorm.DB) *Catalog {
	return &Catalog{
		db:  db,
		now: time.Now,
	}
}

func (c *Catalog) Create(ctx context.Context, in CreateInput) (contracts.Snapshot, error) {
	if c == nil || c.db == nil {
		return contracts.Snapshot{}, fmt.Errorf("snapshot catalog 未初始化")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	snapshotID := strings.TrimSpace(in.SnapshotID)
	if snapshotID == "" {
		return contracts.Snapshot{}, fmt.Errorf("snapshot_id 不能为空")
	}
	baseCommit := strings.TrimSpace(in.BaseCommit)
	workspaceGeneration := strings.TrimSpace(in.WorkspaceGeneration)
	manifestDigest := strings.TrimSpace(strings.ToLower(in.ManifestDigest))
	manifestJSON := strings.TrimSpace(in.ManifestJSON)
	if manifestJSON != "" || manifestDigest != "" {
		var manifest Manifest
		if manifestJSON == "" {
			return contracts.Snapshot{}, fmt.Errorf("manifest_json 不能为空")
		}
		if err := json.Unmarshal([]byte(manifestJSON), &manifest); err != nil {
			return contracts.Snapshot{}, fmt.Errorf("manifest_json 非法: %w", err)
		}
		if baseCommit != "" {
			manifest.BaseCommit = baseCommit
		}
		if workspaceGeneration != "" {
			manifest.WorkspaceGeneration = workspaceGeneration
		}
		computedDigest, normalizedJSON, err := ComputeManifestDigest(manifest)
		if err != nil {
			return contracts.Snapshot{}, err
		}
		if manifestDigest != "" && manifestDigest != computedDigest {
			return contracts.Snapshot{}, fmt.Errorf("manifest_digest 不匹配")
		}
		manifestDigest = computedDigest
		manifestJSON = normalizedJSON
		normalized := NormalizeManifest(manifest)
		baseCommit = normalized.BaseCommit
		workspaceGeneration = normalized.WorkspaceGeneration
	}
	status := strings.TrimSpace(in.Status)
	if status == "" {
		status = string(contracts.SnapshotPreparing)
	}
	rec := contracts.Snapshot{
		SnapshotID:          snapshotID,
		ProjectKey:          strings.TrimSpace(in.ProjectKey),
		NodeName:            strings.TrimSpace(in.NodeName),
		BaseCommit:          baseCommit,
		WorkspaceGeneration: workspaceGeneration,
		ManifestDigest:      manifestDigest,
		ManifestJSON:        manifestJSON,
		Status:              status,
		ArtifactPath:        strings.TrimSpace(in.ArtifactPath),
		ExpiresAt:           in.ExpiresAt,
		LastUsedAt:          timePtr(c.now()),
	}
	if err := c.db.WithContext(ctx).Create(&rec).Error; err != nil {
		if err == gorm.ErrDuplicatedKey || strings.Contains(strings.ToLower(err.Error()), "unique") {
			existing, ferr := c.GetBySnapshotID(ctx, snapshotID)
			if ferr != nil {
				return contracts.Snapshot{}, ferr
			}
			if existing != nil {
				return *existing, nil
			}
		}
		return contracts.Snapshot{}, err
	}
	return rec, nil
}

func (c *Catalog) GetBySnapshotID(ctx context.Context, snapshotID string) (*contracts.Snapshot, error) {
	if c == nil || c.db == nil {
		return nil, fmt.Errorf("snapshot catalog 未初始化")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	snapshotID = strings.TrimSpace(snapshotID)
	if snapshotID == "" {
		return nil, fmt.Errorf("snapshot_id 不能为空")
	}
	var rec contracts.Snapshot
	if err := c.db.WithContext(ctx).Where("snapshot_id = ?", snapshotID).First(&rec).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &rec, nil
}

func (c *Catalog) MarkReady(ctx context.Context, snapshotID, artifactPath string) error {
	if c == nil || c.db == nil {
		return fmt.Errorf("snapshot catalog 未初始化")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	snapshotID = strings.TrimSpace(snapshotID)
	if snapshotID == "" {
		return fmt.Errorf("snapshot_id 不能为空")
	}
	now := c.now()
	res := c.db.WithContext(ctx).Model(&contracts.Snapshot{}).
		Where("snapshot_id = ?", snapshotID).
		Updates(map[string]any{
			"status":        string(contracts.SnapshotReady),
			"artifact_path": strings.TrimSpace(artifactPath),
			"last_used_at":  &now,
			"error_message": "",
		})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("snapshot 不存在: %s", snapshotID)
	}
	return nil
}

func (c *Catalog) Touch(ctx context.Context, snapshotID string) error {
	if c == nil || c.db == nil {
		return fmt.Errorf("snapshot catalog 未初始化")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	snapshotID = strings.TrimSpace(snapshotID)
	if snapshotID == "" {
		return fmt.Errorf("snapshot_id 不能为空")
	}
	now := c.now()
	res := c.db.WithContext(ctx).Model(&contracts.Snapshot{}).
		Where("snapshot_id = ?", snapshotID).
		Updates(map[string]any{
			"last_used_at": &now,
		})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("snapshot 不存在: %s", snapshotID)
	}
	return nil
}

func (c *Catalog) AcquireReference(ctx context.Context, snapshotID string) error {
	if c == nil || c.db == nil {
		return fmt.Errorf("snapshot catalog 未初始化")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	snapshotID = strings.TrimSpace(snapshotID)
	if snapshotID == "" {
		return fmt.Errorf("snapshot_id 不能为空")
	}
	now := c.now()
	res := c.db.WithContext(ctx).Model(&contracts.Snapshot{}).
		Where("snapshot_id = ? AND status = ?", snapshotID, string(contracts.SnapshotReady)).
		Updates(map[string]any{
			"ref_count":    gorm.Expr("ref_count + 1"),
			"last_used_at": &now,
		})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("snapshot 不可引用: %s", snapshotID)
	}
	return nil
}

func (c *Catalog) ReleaseReference(ctx context.Context, snapshotID string) error {
	if c == nil || c.db == nil {
		return fmt.Errorf("snapshot catalog 未初始化")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	snapshotID = strings.TrimSpace(snapshotID)
	if snapshotID == "" {
		return fmt.Errorf("snapshot_id 不能为空")
	}
	now := c.now()
	res := c.db.WithContext(ctx).Model(&contracts.Snapshot{}).
		Where("snapshot_id = ? AND ref_count > 0", snapshotID).
		Updates(map[string]any{
			"ref_count":    gorm.Expr("ref_count - 1"),
			"last_used_at": &now,
		})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("snapshot 不可释放引用: %s", snapshotID)
	}
	return nil
}

func (c *Catalog) ListGarbageEligible(ctx context.Context, now time.Time, limit int) ([]contracts.Snapshot, error) {
	if c == nil || c.db == nil {
		return nil, fmt.Errorf("snapshot catalog 未初始化")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if now.IsZero() {
		now = c.now()
	}
	if limit <= 0 {
		limit = 100
	}
	var out []contracts.Snapshot
	if err := c.db.WithContext(ctx).
		Where("ref_count = 0 AND expires_at IS NOT NULL AND expires_at <= ? AND status <> ?", now, string(contracts.SnapshotExpired)).
		Order("expires_at asc, id asc").
		Limit(limit).
		Find(&out).Error; err != nil {
		return nil, err
	}
	if out == nil {
		return []contracts.Snapshot{}, nil
	}
	return out, nil
}

func (c *Catalog) MarkExpired(ctx context.Context, snapshotID string) error {
	if c == nil || c.db == nil {
		return fmt.Errorf("snapshot catalog 未初始化")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	snapshotID = strings.TrimSpace(snapshotID)
	if snapshotID == "" {
		return fmt.Errorf("snapshot_id 不能为空")
	}
	res := c.db.WithContext(ctx).Model(&contracts.Snapshot{}).
		Where("snapshot_id = ?", snapshotID).
		Updates(map[string]any{
			"status": string(contracts.SnapshotExpired),
		})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("snapshot 不存在: %s", snapshotID)
	}
	return nil
}

func timePtr(v time.Time) *time.Time {
	if v.IsZero() {
		return nil
	}
	return &v
}
