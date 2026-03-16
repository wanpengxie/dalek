package snapshot

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFileStore_WriteAndLoadManifestPack(t *testing.T) {
	store, err := NewFileStore(filepath.Join(t.TempDir(), "snapshots"))
	if err != nil {
		t.Fatalf("NewFileStore failed: %v", err)
	}

	path, digest, err := store.WriteManifestPack("snap-1", Manifest{
		BaseCommit:          "abc123",
		WorkspaceGeneration: "wg-1",
		Files: []ManifestFile{
			{Path: "go.mod", Size: 12, Digest: "sha256:deadbeef", Mode: 0o644},
		},
	})
	if err != nil {
		t.Fatalf("WriteManifestPack failed: %v", err)
	}
	if !strings.HasSuffix(path, filepath.Join("snap-1", "manifest.json")) {
		t.Fatalf("unexpected manifest path: %q", path)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("manifest file missing: %v", err)
	}

	manifest, loadedDigest, raw, err := store.LoadManifestPack("snap-1")
	if err != nil {
		t.Fatalf("LoadManifestPack failed: %v", err)
	}
	if loadedDigest != digest {
		t.Fatalf("digest mismatch: write=%s load=%s", digest, loadedDigest)
	}
	if manifest.BaseCommit != "abc123" || manifest.WorkspaceGeneration != "wg-1" {
		t.Fatalf("unexpected manifest: %+v", manifest)
	}
	if !strings.Contains(raw, `"path":"go.mod"`) {
		t.Fatalf("unexpected normalized raw manifest: %s", raw)
	}
}

func TestNewFileStore_RejectsEmptyRoot(t *testing.T) {
	if _, err := NewFileStore(" "); err == nil {
		t.Fatalf("expected empty root to fail")
	}
}
