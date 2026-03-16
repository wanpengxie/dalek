package snapshot

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"dalek/internal/contracts"
	"dalek/internal/store"
)

func TestGCService_Sweep_ExpiresAndRemovesSnapshotDir(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "dalek.sqlite3")
	db, err := store.OpenAndMigrate(dbPath)
	if err != nil {
		t.Fatalf("OpenAndMigrate failed: %v", err)
	}
	catalog := NewCatalog(db)
	now := time.Date(2026, 3, 14, 20, 0, 0, 0, time.UTC)
	catalog.now = func() time.Time { return now }
	storeDir := filepath.Join(t.TempDir(), "snapshot-store")
	fileStore, err := NewFileStore(storeDir)
	if err != nil {
		t.Fatalf("NewFileStore failed: %v", err)
	}
	if _, _, err := fileStore.WriteManifestPack("snap-gc-pack", Manifest{
		BaseCommit:          "abc123",
		WorkspaceGeneration: "wg-1",
		Files: []ManifestFile{
			{Path: "go.mod", Size: 12, Digest: "sha256:deadbeef", Mode: 0o644},
		},
	}); err != nil {
		t.Fatalf("WriteManifestPack failed: %v", err)
	}
	expiresAt := now.Add(-time.Minute)
	manifestPath, _ := fileStore.ManifestPath("snap-gc-pack")
	if _, err := catalog.Create(context.Background(), CreateInput{
		SnapshotID:   "snap-gc-pack",
		ProjectKey:   "demo",
		Status:       string(contracts.SnapshotReady),
		ExpiresAt:    &expiresAt,
		ArtifactPath: manifestPath,
		ManifestJSON: `{"base_commit":"abc123","workspace_generation":"wg-1","files":[{"path":"go.mod","size":12,"digest":"sha256:deadbeef","mode":420}]}`,
	}); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	gcSvc := NewGCService(catalog, fileStore)
	gcSvc.now = func() time.Time { return now }
	res, err := gcSvc.Sweep(context.Background(), 10)
	if err != nil {
		t.Fatalf("Sweep failed: %v", err)
	}
	if res.Checked != 1 || res.Expired != 1 || res.Removed != 1 {
		t.Fatalf("unexpected gc result: %+v", res)
	}
	got, err := catalog.GetBySnapshotID(context.Background(), "snap-gc-pack")
	if err != nil {
		t.Fatalf("GetBySnapshotID failed: %v", err)
	}
	if got == nil || got.Status != string(contracts.SnapshotExpired) {
		t.Fatalf("unexpected snapshot after gc: %+v", got)
	}
	snapshotDir, err := fileStore.SnapshotDir("snap-gc-pack")
	if err != nil {
		t.Fatalf("SnapshotDir failed: %v", err)
	}
	if _, err := os.Stat(snapshotDir); !os.IsNotExist(err) {
		t.Fatalf("expected snapshot dir removed, err=%v", err)
	}
}

func TestGCService_Sweep_SkipsReferencedSnapshot(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "dalek.sqlite3")
	db, err := store.OpenAndMigrate(dbPath)
	if err != nil {
		t.Fatalf("OpenAndMigrate failed: %v", err)
	}
	catalog := NewCatalog(db)
	now := time.Date(2026, 3, 14, 21, 0, 0, 0, time.UTC)
	catalog.now = func() time.Time { return now }
	storeDir := filepath.Join(t.TempDir(), "snapshot-store")
	fileStore, err := NewFileStore(storeDir)
	if err != nil {
		t.Fatalf("NewFileStore failed: %v", err)
	}
	if _, _, err := fileStore.WriteManifestPack("snap-gc-keep", Manifest{
		BaseCommit:          "abc123",
		WorkspaceGeneration: "wg-2",
		Files: []ManifestFile{
			{Path: "go.mod", Size: 12, Digest: "sha256:deadbeef", Mode: 0o644},
		},
	}); err != nil {
		t.Fatalf("WriteManifestPack failed: %v", err)
	}
	expiresAt := now.Add(-time.Minute)
	manifestPath, _ := fileStore.ManifestPath("snap-gc-keep")
	if _, err := catalog.Create(context.Background(), CreateInput{
		SnapshotID:   "snap-gc-keep",
		ProjectKey:   "demo",
		Status:       string(contracts.SnapshotReady),
		ExpiresAt:    &expiresAt,
		ArtifactPath: manifestPath,
		ManifestJSON: `{"base_commit":"abc123","workspace_generation":"wg-2","files":[{"path":"go.mod","size":12,"digest":"sha256:deadbeef","mode":420}]}`,
	}); err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if err := db.Model(&contracts.Snapshot{}).Where("snapshot_id = ?", "snap-gc-keep").Update("ref_count", 1).Error; err != nil {
		t.Fatalf("ref_count update failed: %v", err)
	}

	gcSvc := NewGCService(catalog, fileStore)
	gcSvc.now = func() time.Time { return now }
	res, err := gcSvc.Sweep(context.Background(), 10)
	if err != nil {
		t.Fatalf("Sweep failed: %v", err)
	}
	if res.Checked != 0 || res.Expired != 0 || res.Removed != 0 {
		t.Fatalf("unexpected gc result: %+v", res)
	}
	got, err := catalog.GetBySnapshotID(context.Background(), "snap-gc-keep")
	if err != nil {
		t.Fatalf("GetBySnapshotID failed: %v", err)
	}
	if got == nil || got.Status != string(contracts.SnapshotReady) || got.RefCount != 1 {
		t.Fatalf("unexpected snapshot after gc: %+v", got)
	}
	snapshotDir, err := fileStore.SnapshotDir("snap-gc-keep")
	if err != nil {
		t.Fatalf("SnapshotDir failed: %v", err)
	}
	if _, err := os.Stat(snapshotDir); err != nil {
		t.Fatalf("expected snapshot dir to remain, err=%v", err)
	}
}
