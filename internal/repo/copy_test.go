package repo

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCopyDirRecursive_CopiesFilesAndSubdirs(t *testing.T) {
	src := t.TempDir()
	// Create structure: src/a.txt, src/sub/b.txt
	if err := os.MkdirAll(filepath.Join(src, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "a.txt"), []byte("aaa"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "sub", "b.txt"), []byte("bbb"), 0o644); err != nil {
		t.Fatal(err)
	}

	dst := filepath.Join(t.TempDir(), "dst")
	if err := CopyDirRecursive(src, dst); err != nil {
		t.Fatalf("CopyDirRecursive failed: %v", err)
	}

	assertFileContent(t, filepath.Join(dst, "a.txt"), "aaa")
	assertFileContent(t, filepath.Join(dst, "sub", "b.txt"), "bbb")
}

func TestCopyDirRecursive_SrcNotExist_Silent(t *testing.T) {
	dst := filepath.Join(t.TempDir(), "dst")
	if err := CopyDirRecursive("/nonexistent/path", dst); err != nil {
		t.Fatalf("expected nil error for missing src, got: %v", err)
	}
	if _, err := os.Stat(dst); !os.IsNotExist(err) {
		t.Fatalf("dst should not be created when src does not exist")
	}
}

func TestCopyDirRecursive_OverwritesExistingDst(t *testing.T) {
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "new.txt"), []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}

	dst := filepath.Join(t.TempDir(), "dst")
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dst, "old.txt"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := CopyDirRecursive(src, dst); err != nil {
		t.Fatalf("CopyDirRecursive failed: %v", err)
	}

	// old.txt should be gone
	if _, err := os.Stat(filepath.Join(dst, "old.txt")); !os.IsNotExist(err) {
		t.Fatalf("old.txt should be removed after overwrite")
	}
	assertFileContent(t, filepath.Join(dst, "new.txt"), "new")
}

func TestCopyDirRecursive_EmptyDir(t *testing.T) {
	src := t.TempDir()
	// src is empty dir
	dst := filepath.Join(t.TempDir(), "dst")
	if err := CopyDirRecursive(src, dst); err != nil {
		t.Fatalf("CopyDirRecursive failed on empty dir: %v", err)
	}
	info, err := os.Stat(dst)
	if err != nil {
		t.Fatalf("dst should exist: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("dst should be a directory")
	}
}

func assertFileContent(t *testing.T, path, expected string) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s failed: %v", path, err)
	}
	if string(raw) != expected {
		t.Fatalf("content mismatch in %s: got=%q want=%q", path, string(raw), expected)
	}
}
