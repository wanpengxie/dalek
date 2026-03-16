package snapshot

import (
	"context"
	"path/filepath"
	"testing"

	"dalek/internal/store"
)

func newSnapshotServiceForTest(t *testing.T) *Service {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "dalek.sqlite3")
	db, err := store.OpenAndMigrate(dbPath)
	if err != nil {
		t.Fatalf("OpenAndMigrate failed: %v", err)
	}
	catalog := NewCatalog(db)
	fileStore, err := NewFileStore(filepath.Join(t.TempDir(), "snapshots"))
	if err != nil {
		t.Fatalf("NewFileStore failed: %v", err)
	}
	return NewService(catalog, fileStore)
}

func TestService_UploadAndDownloadManifestPack(t *testing.T) {
	svc := newSnapshotServiceForTest(t)

	upload, err := svc.UploadManifestPack(context.Background(), UploadInput{
		SnapshotID:   "snap-upload-1",
		ProjectKey:   "demo",
		NodeName:     "node-b",
		BaseCommit:   "abc123",
		ManifestJSON: `{"base_commit":"abc123","workspace_generation":"wg-1","files":[{"path":"go.mod","size":12,"digest":"sha256:deadbeef","mode":420}]}`,
	})
	if err != nil {
		t.Fatalf("UploadManifestPack failed: %v", err)
	}
	if upload.Status != "ready" || upload.ManifestDigest == "" || upload.ArtifactPath == "" {
		t.Fatalf("unexpected upload result: %+v", upload)
	}

	download, err := svc.DownloadManifestPack(context.Background(), "snap-upload-1")
	if err != nil {
		t.Fatalf("DownloadManifestPack failed: %v", err)
	}
	if !download.Found || download.ManifestDigest != upload.ManifestDigest || download.BaseCommit != "abc123" {
		t.Fatalf("unexpected download result: %+v", download)
	}
}

func TestService_UploadManifestChunk_CombinesChunks(t *testing.T) {
	svc := newSnapshotServiceForTest(t)
	manifest := `{"base_commit":"abc123","workspace_generation":"wg-2","files":[{"path":"go.sum","size":12,"digest":"sha256:beef","mode":420}]}`

	res, err := svc.UploadManifestChunk(context.Background(), UploadChunkInput{
		SnapshotID:          "snap-chunk-1",
		ProjectKey:          "demo",
		NodeName:            "node-b",
		BaseCommit:          "abc123",
		WorkspaceGeneration: "wg-2",
		ChunkIndex:          0,
		ChunkData:           []byte(manifest[:40]),
		IsFinal:             false,
	})
	if err != nil {
		t.Fatalf("UploadManifestChunk first failed: %v", err)
	}
	if !res.Accepted || res.Status != "preparing" || res.NextIndex != 1 {
		t.Fatalf("unexpected first chunk result: %+v", res)
	}

	res, err = svc.UploadManifestChunk(context.Background(), UploadChunkInput{
		SnapshotID:          "snap-chunk-1",
		ProjectKey:          "demo",
		NodeName:            "node-b",
		BaseCommit:          "abc123",
		WorkspaceGeneration: "wg-2",
		ChunkIndex:          1,
		ChunkData:           []byte(manifest[40:]),
		IsFinal:             true,
	})
	if err != nil {
		t.Fatalf("UploadManifestChunk final failed: %v", err)
	}
	if !res.Accepted || res.Status != "ready" || res.ManifestDigest == "" || res.ArtifactPath == "" {
		t.Fatalf("unexpected final chunk result: %+v", res)
	}

	download, err := svc.DownloadManifestPack(context.Background(), "snap-chunk-1")
	if err != nil {
		t.Fatalf("DownloadManifestPack failed: %v", err)
	}
	if !download.Found || download.ManifestDigest != res.ManifestDigest || download.BaseCommit != "abc123" {
		t.Fatalf("unexpected download result: %+v", download)
	}
}
