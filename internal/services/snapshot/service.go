package snapshot

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"dalek/internal/contracts"
)

type Service struct {
	catalog *Catalog
	store   *FileStore
}

type UploadInput struct {
	SnapshotID          string
	ProjectKey          string
	NodeName            string
	BaseCommit          string
	WorkspaceGeneration string
	ManifestJSON        string
	ExpiresAt           *time.Time
}

type UploadChunkInput struct {
	SnapshotID          string
	ProjectKey          string
	NodeName            string
	BaseCommit          string
	WorkspaceGeneration string
	ChunkIndex          int
	ChunkData           []byte
	IsFinal             bool
	ExpiresAt           *time.Time
}

type UploadResult struct {
	SnapshotID          string
	Status              string
	ManifestDigest      string
	ManifestJSON        string
	ArtifactPath        string
	BaseCommit          string
	WorkspaceGeneration string
}

type UploadChunkResult struct {
	Accepted            bool
	SnapshotID          string
	Status              string
	NextIndex           int
	ManifestDigest      string
	ManifestJSON        string
	ArtifactPath        string
	BaseCommit          string
	WorkspaceGeneration string
}

type DownloadResult struct {
	Found               bool
	SnapshotID          string
	Status              string
	ManifestDigest      string
	ManifestJSON        string
	ArtifactPath        string
	BaseCommit          string
	WorkspaceGeneration string
}

func NewService(catalog *Catalog, store *FileStore) *Service {
	return &Service{catalog: catalog, store: store}
}

func (s *Service) UploadManifestPack(ctx context.Context, in UploadInput) (UploadResult, error) {
	if s == nil || s.catalog == nil || s.store == nil {
		return UploadResult{}, fmt.Errorf("snapshot service 未初始化")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	manifest, err := parseManifestForUpload(in.ManifestJSON, in.BaseCommit, in.WorkspaceGeneration)
	if err != nil {
		return UploadResult{}, err
	}
	digest, normalizedJSON, err := ComputeManifestDigest(manifest)
	if err != nil {
		return UploadResult{}, err
	}
	path, _, err := s.store.WriteManifestPack(strings.TrimSpace(in.SnapshotID), manifest)
	if err != nil {
		return UploadResult{}, err
	}
	rec, err := s.catalog.Create(ctx, CreateInput{
		SnapshotID:          strings.TrimSpace(in.SnapshotID),
		ProjectKey:          strings.TrimSpace(in.ProjectKey),
		NodeName:            strings.TrimSpace(in.NodeName),
		BaseCommit:          manifest.BaseCommit,
		WorkspaceGeneration: manifest.WorkspaceGeneration,
		ManifestDigest:      digest,
		ManifestJSON:        normalizedJSON,
		ArtifactPath:        path,
		ExpiresAt:           in.ExpiresAt,
	})
	if err != nil {
		return UploadResult{}, err
	}
	if err := s.catalog.MarkReady(ctx, rec.SnapshotID, path); err != nil {
		return UploadResult{}, err
	}
	return UploadResult{
		SnapshotID:          rec.SnapshotID,
		Status:              string(contracts.SnapshotReady),
		ManifestDigest:      digest,
		ManifestJSON:        normalizedJSON,
		ArtifactPath:        path,
		BaseCommit:          manifest.BaseCommit,
		WorkspaceGeneration: manifest.WorkspaceGeneration,
	}, nil
}

func (s *Service) UploadManifestChunk(ctx context.Context, in UploadChunkInput) (UploadChunkResult, error) {
	if s == nil || s.catalog == nil || s.store == nil {
		return UploadChunkResult{}, fmt.Errorf("snapshot service 未初始化")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	snapshotID := strings.TrimSpace(in.SnapshotID)
	if snapshotID == "" {
		return UploadChunkResult{}, fmt.Errorf("snapshot_id 不能为空")
	}
	if in.ChunkIndex < 0 {
		return UploadChunkResult{}, fmt.Errorf("chunk_index 不能为负值")
	}
	if len(in.ChunkData) == 0 {
		return UploadChunkResult{}, fmt.Errorf("chunk_data 不能为空")
	}

	state, exists, err := s.loadUploadState(snapshotID)
	if err != nil {
		return UploadChunkResult{}, err
	}
	if !exists {
		if in.ChunkIndex != 0 {
			return UploadChunkResult{}, fmt.Errorf("chunk_index 非法，必须从 0 开始")
		}
		state = uploadState{
			NextIndex:           0,
			BaseCommit:          strings.TrimSpace(in.BaseCommit),
			WorkspaceGeneration: strings.TrimSpace(in.WorkspaceGeneration),
		}
		tempPath, err := s.store.ManifestUploadTempPath(snapshotID)
		if err != nil {
			return UploadChunkResult{}, err
		}
		if err := os.MkdirAll(filepath.Dir(tempPath), 0o755); err != nil {
			return UploadChunkResult{}, err
		}
		file, err := os.Create(tempPath)
		if err != nil {
			return UploadChunkResult{}, err
		}
		_ = file.Close()
		if err := s.saveUploadState(snapshotID, state); err != nil {
			return UploadChunkResult{}, err
		}
	} else {
		if in.ChunkIndex != state.NextIndex {
			return UploadChunkResult{}, fmt.Errorf("chunk_index 非法，期待=%d 实际=%d", state.NextIndex, in.ChunkIndex)
		}
		if baseCommit := strings.TrimSpace(in.BaseCommit); baseCommit != "" && state.BaseCommit != "" && baseCommit != state.BaseCommit {
			return UploadChunkResult{}, fmt.Errorf("base_commit 不一致")
		}
		if wg := strings.TrimSpace(in.WorkspaceGeneration); wg != "" && state.WorkspaceGeneration != "" && wg != state.WorkspaceGeneration {
			return UploadChunkResult{}, fmt.Errorf("workspace_generation 不一致")
		}
	}

	tempPath, err := s.store.ManifestUploadTempPath(snapshotID)
	if err != nil {
		return UploadChunkResult{}, err
	}
	file, err := os.OpenFile(tempPath, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return UploadChunkResult{}, err
	}
	if _, err := file.Write(in.ChunkData); err != nil {
		_ = file.Close()
		return UploadChunkResult{}, err
	}
	if err := file.Close(); err != nil {
		return UploadChunkResult{}, err
	}

	if !in.IsFinal {
		state.NextIndex++
		if err := s.saveUploadState(snapshotID, state); err != nil {
			return UploadChunkResult{}, err
		}
		return UploadChunkResult{
			Accepted:   true,
			SnapshotID: snapshotID,
			Status:     string(contracts.SnapshotPreparing),
			NextIndex:  state.NextIndex,
		}, nil
	}

	raw, err := os.ReadFile(tempPath)
	if err != nil {
		return UploadChunkResult{}, err
	}
	manifest, err := parseManifestForUpload(string(raw), strings.TrimSpace(in.BaseCommit), strings.TrimSpace(in.WorkspaceGeneration))
	if err != nil {
		_ = s.clearUploadState(snapshotID)
		_ = os.Remove(tempPath)
		return UploadChunkResult{}, err
	}
	digest, normalizedJSON, err := ComputeManifestDigest(manifest)
	if err != nil {
		_ = s.clearUploadState(snapshotID)
		_ = os.Remove(tempPath)
		return UploadChunkResult{}, err
	}
	path, _, err := s.store.WriteManifestPack(snapshotID, manifest)
	if err != nil {
		return UploadChunkResult{}, err
	}
	rec, err := s.catalog.Create(ctx, CreateInput{
		SnapshotID:          snapshotID,
		ProjectKey:          strings.TrimSpace(in.ProjectKey),
		NodeName:            strings.TrimSpace(in.NodeName),
		BaseCommit:          manifest.BaseCommit,
		WorkspaceGeneration: manifest.WorkspaceGeneration,
		ManifestDigest:      digest,
		ManifestJSON:        normalizedJSON,
		ArtifactPath:        path,
		ExpiresAt:           in.ExpiresAt,
	})
	if err != nil {
		return UploadChunkResult{}, err
	}
	if err := s.catalog.MarkReady(ctx, rec.SnapshotID, path); err != nil {
		return UploadChunkResult{}, err
	}
	_ = s.clearUploadState(snapshotID)
	_ = os.Remove(tempPath)

	return UploadChunkResult{
		Accepted:            true,
		SnapshotID:          rec.SnapshotID,
		Status:              string(contracts.SnapshotReady),
		ManifestDigest:      digest,
		ManifestJSON:        normalizedJSON,
		ArtifactPath:        path,
		BaseCommit:          manifest.BaseCommit,
		WorkspaceGeneration: manifest.WorkspaceGeneration,
	}, nil
}

type uploadState struct {
	NextIndex           int    `json:"next_index"`
	BaseCommit          string `json:"base_commit,omitempty"`
	WorkspaceGeneration string `json:"workspace_generation,omitempty"`
}

func (s *Service) loadUploadState(snapshotID string) (uploadState, bool, error) {
	if s == nil || s.store == nil {
		return uploadState{}, false, fmt.Errorf("snapshot service 未初始化")
	}
	path, err := s.store.ManifestUploadStatePath(snapshotID)
	if err != nil {
		return uploadState{}, false, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return uploadState{}, false, nil
		}
		return uploadState{}, false, err
	}
	var state uploadState
	if err := json.Unmarshal(raw, &state); err != nil {
		return uploadState{}, false, err
	}
	if state.NextIndex < 0 {
		state.NextIndex = 0
	}
	state.BaseCommit = strings.TrimSpace(state.BaseCommit)
	state.WorkspaceGeneration = strings.TrimSpace(state.WorkspaceGeneration)
	return state, true, nil
}

func (s *Service) saveUploadState(snapshotID string, state uploadState) error {
	if s == nil || s.store == nil {
		return fmt.Errorf("snapshot service 未初始化")
	}
	path, err := s.store.ManifestUploadStatePath(snapshotID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	raw, err := json.Marshal(state)
	if err != nil {
		return err
	}
	return os.WriteFile(path, raw, 0o644)
}

func (s *Service) clearUploadState(snapshotID string) error {
	if s == nil || s.store == nil {
		return nil
	}
	path, err := s.store.ManifestUploadStatePath(snapshotID)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (s *Service) DownloadManifestPack(ctx context.Context, snapshotID string) (DownloadResult, error) {
	if s == nil || s.catalog == nil || s.store == nil {
		return DownloadResult{}, fmt.Errorf("snapshot service 未初始化")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	snapshotID = strings.TrimSpace(snapshotID)
	if snapshotID == "" {
		return DownloadResult{}, fmt.Errorf("snapshot_id 不能为空")
	}
	rec, err := s.catalog.GetBySnapshotID(ctx, snapshotID)
	if err != nil {
		return DownloadResult{}, err
	}
	if rec == nil {
		return DownloadResult{Found: false, SnapshotID: snapshotID}, nil
	}
	_, digest, normalizedJSON, err := s.store.LoadManifestPack(snapshotID)
	if err != nil {
		return DownloadResult{}, err
	}
	_ = s.catalog.Touch(ctx, snapshotID)
	return DownloadResult{
		Found:               true,
		SnapshotID:          rec.SnapshotID,
		Status:              strings.TrimSpace(rec.Status),
		ManifestDigest:      digest,
		ManifestJSON:        normalizedJSON,
		ArtifactPath:        strings.TrimSpace(rec.ArtifactPath),
		BaseCommit:          strings.TrimSpace(rec.BaseCommit),
		WorkspaceGeneration: strings.TrimSpace(rec.WorkspaceGeneration),
	}, nil
}
