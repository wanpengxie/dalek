package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	snapshotsvc "dalek/internal/services/snapshot"
)

func (p *Project) UploadSnapshotManifest(ctx context.Context, opt SnapshotUploadOptions) (SnapshotUploadResult, error) {
	if p == nil || p.snapshot == nil {
		return SnapshotUploadResult{}, fmt.Errorf("project snapshot service 为空")
	}
	return p.snapshot.UploadManifestPack(ctx, snapshotsvc.UploadInput{
		SnapshotID:          strings.TrimSpace(opt.SnapshotID),
		ProjectKey:          strings.TrimSpace(p.Key()),
		NodeName:            strings.TrimSpace(opt.NodeName),
		BaseCommit:          strings.TrimSpace(opt.BaseCommit),
		WorkspaceGeneration: strings.TrimSpace(opt.WorkspaceGeneration),
		ManifestJSON:        strings.TrimSpace(opt.ManifestJSON),
		ExpiresAt:           opt.ExpiresAt,
	})
}

func (p *Project) DownloadSnapshotManifest(ctx context.Context, snapshotID string) (SnapshotDownloadResult, error) {
	if p == nil || p.snapshot == nil {
		return SnapshotDownloadResult{}, fmt.Errorf("project snapshot service 为空")
	}
	return p.snapshot.DownloadManifestPack(ctx, strings.TrimSpace(snapshotID))
}

type SnapshotUploadOptions struct {
	SnapshotID          string
	NodeName            string
	BaseCommit          string
	WorkspaceGeneration string
	ManifestJSON        string
	ExpiresAt           *time.Time
}

type SnapshotUploadChunkOptions struct {
	SnapshotID          string
	NodeName            string
	BaseCommit          string
	WorkspaceGeneration string
	ChunkIndex          int
	ChunkData           []byte
	IsFinal             bool
	ExpiresAt           *time.Time
}

type SnapshotUploadChunkResult struct {
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

func (p *Project) UploadSnapshotManifestChunk(ctx context.Context, opt SnapshotUploadChunkOptions) (SnapshotUploadChunkResult, error) {
	if p == nil || p.snapshot == nil {
		return SnapshotUploadChunkResult{}, fmt.Errorf("project snapshot service 为空")
	}
	res, err := p.snapshot.UploadManifestChunk(ctx, snapshotsvc.UploadChunkInput{
		SnapshotID:          strings.TrimSpace(opt.SnapshotID),
		ProjectKey:          strings.TrimSpace(p.Key()),
		NodeName:            strings.TrimSpace(opt.NodeName),
		BaseCommit:          strings.TrimSpace(opt.BaseCommit),
		WorkspaceGeneration: strings.TrimSpace(opt.WorkspaceGeneration),
		ChunkIndex:          opt.ChunkIndex,
		ChunkData:           opt.ChunkData,
		IsFinal:             opt.IsFinal,
		ExpiresAt:           opt.ExpiresAt,
	})
	if err != nil {
		return SnapshotUploadChunkResult{}, err
	}
	return SnapshotUploadChunkResult{
		Accepted:            res.Accepted,
		SnapshotID:          res.SnapshotID,
		Status:              res.Status,
		NextIndex:           res.NextIndex,
		ManifestDigest:      res.ManifestDigest,
		ManifestJSON:        res.ManifestJSON,
		ArtifactPath:        res.ArtifactPath,
		BaseCommit:          res.BaseCommit,
		WorkspaceGeneration: res.WorkspaceGeneration,
	}, nil
}
