package snapshot

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"dalek/internal/contracts"
)

type ApplyService struct {
	catalog *Catalog
	store   *FileStore
}

type AttachInput struct {
	SnapshotID string
	BaseCommit string
}

type AttachResult struct {
	SnapshotID          string
	BaseCommit          string
	WorkspaceGeneration string
	Summary             string
}

type ApplyInput struct {
	SnapshotID string
	BaseCommit string
}

type WorkspaceApplyInput struct {
	SnapshotID   string
	BaseCommit   string
	WorkspaceDir string
}

type ApplyResult struct {
	SnapshotID          string
	BaseCommit          string
	WorkspaceGeneration string
	ManifestDigest      string
	ManifestJSON        string
	ArtifactPath        string
	Files               []ManifestFile
	Summary             string
}

type WorkspaceApplyResult struct {
	ApplyResult
	WorkspaceDir     string
	PlanPath         string
	AppliedFileCount int
}

func NewApplyService(catalog *Catalog, store *FileStore) *ApplyService {
	return &ApplyService{catalog: catalog, store: store}
}

func (s *ApplyService) Catalog() *Catalog {
	if s == nil {
		return nil
	}
	return s.catalog
}

func (s *ApplyService) Store() *FileStore {
	if s == nil {
		return nil
	}
	return s.store
}

func (s *ApplyService) AttachReadySnapshot(ctx context.Context, in AttachInput) (AttachResult, error) {
	applied, err := s.LoadReadySnapshot(ctx, ApplyInput{
		SnapshotID: in.SnapshotID,
		BaseCommit: in.BaseCommit,
	})
	if err != nil {
		return AttachResult{}, err
	}
	return AttachResult{
		SnapshotID:          applied.SnapshotID,
		BaseCommit:          applied.BaseCommit,
		WorkspaceGeneration: applied.WorkspaceGeneration,
		Summary:             applied.Summary,
	}, nil
}

func (s *ApplyService) LoadReadySnapshot(ctx context.Context, in ApplyInput) (ApplyResult, error) {
	if s == nil || s.catalog == nil || s.store == nil {
		return ApplyResult{}, fmt.Errorf("snapshot apply service 未初始化")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	snapshotID := strings.TrimSpace(in.SnapshotID)
	if snapshotID == "" {
		return ApplyResult{}, fmt.Errorf("snapshot_id 不能为空")
	}
	rec, err := s.catalog.GetBySnapshotID(ctx, snapshotID)
	if err != nil {
		return ApplyResult{}, err
	}
	if rec == nil {
		return ApplyResult{}, fmt.Errorf("snapshot 不存在: %s", snapshotID)
	}
	if strings.TrimSpace(rec.Status) != string(contracts.SnapshotReady) {
		return ApplyResult{}, fmt.Errorf("snapshot 未 ready: %s", snapshotID)
	}
	baseCommit := strings.TrimSpace(in.BaseCommit)
	if baseCommit != "" && strings.TrimSpace(rec.BaseCommit) != "" && baseCommit != strings.TrimSpace(rec.BaseCommit) {
		return ApplyResult{}, fmt.Errorf("snapshot base_commit 不匹配")
	}
	manifest, digest, normalizedJSON, err := s.store.LoadManifestPack(snapshotID)
	if err != nil {
		return ApplyResult{}, err
	}
	if recorded := strings.TrimSpace(strings.ToLower(rec.ManifestDigest)); recorded != "" && recorded != strings.TrimSpace(strings.ToLower(digest)) {
		return ApplyResult{}, fmt.Errorf("snapshot manifest_digest 不匹配")
	}
	_ = s.catalog.Touch(ctx, snapshotID)
	return ApplyResult{
		SnapshotID:          rec.SnapshotID,
		BaseCommit:          manifest.BaseCommit,
		WorkspaceGeneration: manifest.WorkspaceGeneration,
		ManifestDigest:      digest,
		ManifestJSON:        normalizedJSON,
		ArtifactPath:        strings.TrimSpace(rec.ArtifactPath),
		Files:               append([]ManifestFile(nil), manifest.Files...),
		Summary:             "snapshot ready: " + rec.SnapshotID,
	}, nil
}

func (s *ApplyService) ApplyToWorkspace(ctx context.Context, in WorkspaceApplyInput) (WorkspaceApplyResult, error) {
	applied, err := s.LoadReadySnapshot(ctx, ApplyInput{
		SnapshotID: in.SnapshotID,
		BaseCommit: in.BaseCommit,
	})
	if err != nil {
		return WorkspaceApplyResult{}, err
	}
	return s.applyToWorkspaceResult(in.SnapshotID, in.WorkspaceDir, applied)
}

func (s *ApplyService) ApplyManifestJSONToWorkspace(ctx context.Context, snapshotID, manifestJSON, baseCommit, workspaceDir string) (WorkspaceApplyResult, error) {
	if s == nil || s.store == nil {
		return WorkspaceApplyResult{}, fmt.Errorf("snapshot apply service 未初始化")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	snapshotID = strings.TrimSpace(snapshotID)
	if snapshotID == "" {
		return WorkspaceApplyResult{}, fmt.Errorf("snapshot_id 不能为空")
	}
	manifest, err := parseManifestForUpload(strings.TrimSpace(manifestJSON), baseCommit, "")
	if err != nil {
		return WorkspaceApplyResult{}, err
	}
	digest, normalizedJSON, err := ComputeManifestDigest(manifest)
	if err != nil {
		return WorkspaceApplyResult{}, err
	}
	artifactPath, _, err := s.store.WriteManifestPack(snapshotID, manifest)
	if err != nil {
		return WorkspaceApplyResult{}, err
	}
	applied := ApplyResult{
		SnapshotID:          snapshotID,
		BaseCommit:          manifest.BaseCommit,
		WorkspaceGeneration: manifest.WorkspaceGeneration,
		ManifestDigest:      digest,
		ManifestJSON:        normalizedJSON,
		ArtifactPath:        artifactPath,
		Files:               append([]ManifestFile(nil), manifest.Files...),
		Summary:             "snapshot downloaded: " + snapshotID,
	}
	return s.applyToWorkspaceResult(snapshotID, workspaceDir, applied)
}

func (s *ApplyService) resolveWorkspaceDir(snapshotID, workspaceDir string) (string, error) {
	if s == nil || s.store == nil {
		return "", fmt.Errorf("snapshot apply service 未初始化")
	}
	workspaceDir = strings.TrimSpace(workspaceDir)
	if workspaceDir != "" {
		return filepath.Abs(workspaceDir)
	}
	root := filepath.Dir(s.store.RootDir())
	if strings.TrimSpace(root) == "" {
		return "", fmt.Errorf("snapshot workspace root 非法")
	}
	return filepath.Join(root, "workspaces", normalizeSnapshotPathSegment(snapshotID)), nil
}

func (s *ApplyService) applyToWorkspaceResult(snapshotID, workspaceDir string, applied ApplyResult) (WorkspaceApplyResult, error) {
	workspaceDir, err := s.resolveWorkspaceDir(snapshotID, workspaceDir)
	if err != nil {
		return WorkspaceApplyResult{}, err
	}
	if err := os.MkdirAll(workspaceDir, 0o755); err != nil {
		return WorkspaceApplyResult{}, err
	}
	planPath := filepath.Join(workspaceDir, "apply-plan.json")
	planRaw, err := json.MarshalIndent(map[string]any{
		"snapshot_id":          applied.SnapshotID,
		"base_commit":          applied.BaseCommit,
		"workspace_generation": applied.WorkspaceGeneration,
		"manifest_digest":      applied.ManifestDigest,
		"artifact_path":        applied.ArtifactPath,
		"workspace_dir":        workspaceDir,
		"applied_file_count":   len(applied.Files),
		"files":                applied.Files,
	}, "", "  ")
	if err != nil {
		return WorkspaceApplyResult{}, err
	}
	if err := os.WriteFile(planPath, planRaw, 0o644); err != nil {
		return WorkspaceApplyResult{}, err
	}
	result := WorkspaceApplyResult{
		ApplyResult:      applied,
		WorkspaceDir:     workspaceDir,
		PlanPath:         planPath,
		AppliedFileCount: len(applied.Files),
	}
	if workspaceDir != "" {
		result.Summary = fmt.Sprintf("snapshot apply accepted: %s -> %s (%d files)", applied.SnapshotID, workspaceDir, len(applied.Files))
	} else {
		result.Summary = fmt.Sprintf("snapshot apply accepted: %s (%d files)", applied.SnapshotID, len(applied.Files))
	}
	return result, nil
}
