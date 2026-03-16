package snapshot

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildManifestFromWorkspace_BuildsRegularFilesAndSkipsRuntimeDirs(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module demo\n"), 0o644); err != nil {
		t.Fatalf("write go.mod failed: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "subdir"), 0o755); err != nil {
		t.Fatalf("mkdir subdir failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "subdir", "main.go"), []byte("package main\n"), 0o600); err != nil {
		t.Fatalf("write main.go failed: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir .git failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, ".git", "HEAD"), []byte("ref: refs/heads/main\n"), 0o644); err != nil {
		t.Fatalf("write git HEAD failed: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".dalek", "runtime"), 0o755); err != nil {
		t.Fatalf("mkdir .dalek/runtime failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, ".dalek", "runtime", "dalek.sqlite3"), []byte("sqlite"), 0o644); err != nil {
		t.Fatalf("write runtime sqlite failed: %v", err)
	}

	manifest, err := BuildManifestFromWorkspace(BuildManifestInput{
		WorkspaceDir:        root,
		BaseCommit:          "abc123",
		WorkspaceGeneration: "wg-1",
	})
	if err != nil {
		t.Fatalf("BuildManifestFromWorkspace failed: %v", err)
	}
	if manifest.BaseCommit != "abc123" || manifest.WorkspaceGeneration != "wg-1" {
		t.Fatalf("unexpected manifest header: %+v", manifest)
	}
	if len(manifest.Files) != 2 {
		t.Fatalf("unexpected file count: %+v", manifest.Files)
	}
	for _, file := range manifest.Files {
		if strings.HasPrefix(file.Path, ".git/") {
			t.Fatalf("manifest should skip .git files: %+v", manifest.Files)
		}
		if strings.HasPrefix(file.Path, ".dalek/runtime/") {
			t.Fatalf("manifest should skip runtime files: %+v", manifest.Files)
		}
		if !strings.HasPrefix(file.Digest, "sha256:") {
			t.Fatalf("unexpected digest: %+v", file)
		}
	}
}

func TestBuildManifestFromWorkspace_RequiresIdentityFields(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module demo\n"), 0o644); err != nil {
		t.Fatalf("write go.mod failed: %v", err)
	}
	if _, err := BuildManifestFromWorkspace(BuildManifestInput{WorkspaceDir: root, WorkspaceGeneration: "wg-1"}); err == nil {
		t.Fatalf("expected base_commit validation error")
	}
	if _, err := BuildManifestFromWorkspace(BuildManifestInput{WorkspaceDir: root, BaseCommit: "abc123"}); err == nil {
		t.Fatalf("expected workspace_generation validation error")
	}
}
