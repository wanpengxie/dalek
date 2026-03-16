package snapshot

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"dalek/internal/contracts"
	"dalek/internal/store"
)

func newApplyServiceForTest(t *testing.T) (*ApplyService, *Catalog, *FileStore) {
	t.Helper()
	rootDir := t.TempDir()
	dbPath := filepath.Join(rootDir, "dalek.sqlite3")
	db, err := store.OpenAndMigrate(dbPath)
	if err != nil {
		t.Fatalf("OpenAndMigrate failed: %v", err)
	}
	catalog := NewCatalog(db)
	fileStore, err := NewFileStore(filepath.Join(rootDir, "snapshots"))
	if err != nil {
		t.Fatalf("NewFileStore failed: %v", err)
	}
	return NewApplyService(catalog, fileStore), catalog, fileStore
}

func TestApplyService_AttachReadySnapshot(t *testing.T) {
	applySvc, catalog, fileStore := newApplyServiceForTest(t)
	path, digest, err := fileStore.WriteManifestPack("snap-attach", Manifest{
		BaseCommit:          "abc123",
		WorkspaceGeneration: "wg-1",
		Files: []ManifestFile{
			{Path: "go.mod", Size: 12, Digest: "sha256:deadbeef", Mode: 420},
		},
	})
	if err != nil {
		t.Fatalf("WriteManifestPack failed: %v", err)
	}
	if _, err := catalog.Create(context.Background(), CreateInput{
		SnapshotID:     "snap-attach",
		ProjectKey:     "demo",
		BaseCommit:     "abc123",
		ManifestDigest: digest,
		ManifestJSON:   `{"base_commit":"abc123","workspace_generation":"wg-1","files":[{"path":"go.mod","size":12,"digest":"sha256:deadbeef","mode":420}]}`,
		ArtifactPath:   path,
	}); err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if err := catalog.MarkReady(context.Background(), "snap-attach", path); err != nil {
		t.Fatalf("MarkReady failed: %v", err)
	}

	res, err := applySvc.AttachReadySnapshot(context.Background(), AttachInput{
		SnapshotID: "snap-attach",
		BaseCommit: "abc123",
	})
	if err != nil {
		t.Fatalf("AttachReadySnapshot failed: %v", err)
	}
	if res.SnapshotID != "snap-attach" || res.WorkspaceGeneration != "wg-1" {
		t.Fatalf("unexpected attach result: %+v", res)
	}
}

func TestApplyService_AttachReadySnapshot_RejectsMismatch(t *testing.T) {
	applySvc, catalog, fileStore := newApplyServiceForTest(t)
	path, digest, err := fileStore.WriteManifestPack("snap-mismatch", Manifest{
		BaseCommit:          "abc123",
		WorkspaceGeneration: "wg-1",
		Files: []ManifestFile{
			{Path: "go.mod", Size: 12, Digest: "sha256:deadbeef", Mode: 420},
		},
	})
	if err != nil {
		t.Fatalf("WriteManifestPack failed: %v", err)
	}
	if _, err := catalog.Create(context.Background(), CreateInput{
		SnapshotID:     "snap-mismatch",
		ProjectKey:     "demo",
		BaseCommit:     "abc123",
		Status:         string(contracts.SnapshotReady),
		ManifestDigest: digest,
		ManifestJSON:   `{"base_commit":"abc123","workspace_generation":"wg-1","files":[{"path":"go.mod","size":12,"digest":"sha256:deadbeef","mode":420}]}`,
		ArtifactPath:   path,
	}); err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if err := catalog.MarkReady(context.Background(), "snap-mismatch", path); err != nil {
		t.Fatalf("MarkReady failed: %v", err)
	}

	_, err = applySvc.AttachReadySnapshot(context.Background(), AttachInput{
		SnapshotID: "snap-mismatch",
		BaseCommit: "def456",
	})
	if err == nil {
		t.Fatalf("expected mismatch to fail")
	}
}

func TestApplyService_LoadReadySnapshot(t *testing.T) {
	applySvc, catalog, fileStore := newApplyServiceForTest(t)
	path, digest, err := fileStore.WriteManifestPack("snap-load", Manifest{
		BaseCommit:          "abc123",
		WorkspaceGeneration: "wg-2",
		Files: []ManifestFile{
			{Path: "go.mod", Size: 12, Digest: "sha256:deadbeef", Mode: 420},
			{Path: "main.go", Size: 34, Digest: "sha256:cafebabe", Mode: 420},
		},
	})
	if err != nil {
		t.Fatalf("WriteManifestPack failed: %v", err)
	}
	if _, err := catalog.Create(context.Background(), CreateInput{
		SnapshotID:     "snap-load",
		ProjectKey:     "demo",
		BaseCommit:     "abc123",
		Status:         string(contracts.SnapshotReady),
		ManifestDigest: digest,
		ManifestJSON:   `{"base_commit":"abc123","workspace_generation":"wg-2","files":[{"path":"go.mod","size":12,"digest":"sha256:deadbeef","mode":420},{"path":"main.go","size":34,"digest":"sha256:cafebabe","mode":420}]}`,
		ArtifactPath:   path,
	}); err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if err := catalog.MarkReady(context.Background(), "snap-load", path); err != nil {
		t.Fatalf("MarkReady failed: %v", err)
	}

	res, err := applySvc.LoadReadySnapshot(context.Background(), ApplyInput{
		SnapshotID: "snap-load",
		BaseCommit: "abc123",
	})
	if err != nil {
		t.Fatalf("LoadReadySnapshot failed: %v", err)
	}
	if res.SnapshotID != "snap-load" || res.ManifestDigest != digest {
		t.Fatalf("unexpected apply result: %+v", res)
	}
	if len(res.Files) != 2 {
		t.Fatalf("expected 2 files, got=%d", len(res.Files))
	}
}

func TestApplyService_ApplyToWorkspace(t *testing.T) {
	applySvc, catalog, fileStore := newApplyServiceForTest(t)
	path, digest, err := fileStore.WriteManifestPack("snap-apply", Manifest{
		BaseCommit:          "abc123",
		WorkspaceGeneration: "wg-3",
		Files: []ManifestFile{
			{Path: "go.mod", Size: 12, Digest: "sha256:deadbeef", Mode: 420},
			{Path: "main.go", Size: 34, Digest: "sha256:cafebabe", Mode: 420},
		},
	})
	if err != nil {
		t.Fatalf("WriteManifestPack failed: %v", err)
	}
	if _, err := catalog.Create(context.Background(), CreateInput{
		SnapshotID:     "snap-apply",
		ProjectKey:     "demo",
		BaseCommit:     "abc123",
		Status:         string(contracts.SnapshotReady),
		ManifestDigest: digest,
		ManifestJSON:   `{"base_commit":"abc123","workspace_generation":"wg-3","files":[{"path":"go.mod","size":12,"digest":"sha256:deadbeef","mode":420},{"path":"main.go","size":34,"digest":"sha256:cafebabe","mode":420}]}`,
		ArtifactPath:   path,
	}); err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if err := catalog.MarkReady(context.Background(), "snap-apply", path); err != nil {
		t.Fatalf("MarkReady failed: %v", err)
	}

	res, err := applySvc.ApplyToWorkspace(context.Background(), WorkspaceApplyInput{
		SnapshotID:   "snap-apply",
		BaseCommit:   "abc123",
		WorkspaceDir: "/tmp/run-workspace",
	})
	if err != nil {
		t.Fatalf("ApplyToWorkspace failed: %v", err)
	}
	if res.AppliedFileCount != 2 || res.WorkspaceDir != "/tmp/run-workspace" {
		t.Fatalf("unexpected workspace apply result: %+v", res)
	}
	if _, err := os.Stat(res.PlanPath); err != nil {
		t.Fatalf("expected plan file to exist: %v", err)
	}
}

func TestApplyService_ApplyToWorkspace_DefaultWorkspaceDir(t *testing.T) {
	applySvc, catalog, fileStore := newApplyServiceForTest(t)
	path, digest, err := fileStore.WriteManifestPack("snap-default-workspace", Manifest{
		BaseCommit:          "abc123",
		WorkspaceGeneration: "wg-4",
		Files: []ManifestFile{
			{Path: "go.mod", Size: 12, Digest: "sha256:deadbeef", Mode: 420},
		},
	})
	if err != nil {
		t.Fatalf("WriteManifestPack failed: %v", err)
	}
	if _, err := catalog.Create(context.Background(), CreateInput{
		SnapshotID:     "snap-default-workspace",
		ProjectKey:     "demo",
		BaseCommit:     "abc123",
		Status:         string(contracts.SnapshotReady),
		ManifestDigest: digest,
		ManifestJSON:   `{"base_commit":"abc123","workspace_generation":"wg-4","files":[{"path":"go.mod","size":12,"digest":"sha256:deadbeef","mode":420}]}`,
		ArtifactPath:   path,
	}); err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if err := catalog.MarkReady(context.Background(), "snap-default-workspace", path); err != nil {
		t.Fatalf("MarkReady failed: %v", err)
	}

	res, err := applySvc.ApplyToWorkspace(context.Background(), WorkspaceApplyInput{
		SnapshotID: "snap-default-workspace",
		BaseCommit: "abc123",
	})
	if err != nil {
		t.Fatalf("ApplyToWorkspace failed: %v", err)
	}
	if filepath.Base(res.WorkspaceDir) != "snap-default-workspace" {
		t.Fatalf("unexpected default workspace dir: %s", res.WorkspaceDir)
	}
	if _, err := os.Stat(res.PlanPath); err != nil {
		t.Fatalf("expected default plan file to exist: %v", err)
	}
}

func TestApplyService_ApplyManifestJSONToWorkspace(t *testing.T) {
	applySvc, _, _ := newApplyServiceForTest(t)

	res, err := applySvc.ApplyManifestJSONToWorkspace(context.Background(),
		"snap-json-apply",
		`{"base_commit":"abc123","workspace_generation":"wg-5","files":[{"path":"go.mod","size":12,"digest":"sha256:deadbeef","mode":420}]}`,
		"abc123",
		"/tmp/run-manifest",
	)
	if err != nil {
		t.Fatalf("ApplyManifestJSONToWorkspace failed: %v", err)
	}
	if res.SnapshotID != "snap-json-apply" || res.AppliedFileCount != 1 {
		t.Fatalf("unexpected apply result: %+v", res)
	}
	if _, err := os.Stat(res.PlanPath); err != nil {
		t.Fatalf("expected plan file to exist: %v", err)
	}
}
