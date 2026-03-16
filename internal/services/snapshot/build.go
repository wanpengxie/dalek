package snapshot

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

type BuildManifestInput struct {
	WorkspaceDir        string
	BaseCommit          string
	WorkspaceGeneration string
}

func BuildManifestFromWorkspace(in BuildManifestInput) (Manifest, error) {
	root := strings.TrimSpace(in.WorkspaceDir)
	if root == "" {
		return Manifest{}, fmt.Errorf("workspace_dir 不能为空")
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return Manifest{}, err
	}
	baseCommit := strings.TrimSpace(in.BaseCommit)
	if baseCommit == "" {
		return Manifest{}, fmt.Errorf("base_commit 不能为空")
	}
	workspaceGeneration := strings.TrimSpace(in.WorkspaceGeneration)
	if workspaceGeneration == "" {
		return Manifest{}, fmt.Errorf("workspace_generation 不能为空")
	}

	manifest := Manifest{
		BaseCommit:          baseCommit,
		WorkspaceGeneration: workspaceGeneration,
		Files:               []ManifestFile{},
	}
	err = filepath.WalkDir(absRoot, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		name := strings.TrimSpace(d.Name())
		if d.IsDir() {
			relDir, err := filepath.Rel(absRoot, path)
			if err != nil {
				return err
			}
			if shouldSkipManifestDir(normalizeManifestPath(relDir), name) {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(absRoot, path)
		if err != nil {
			return err
		}
		rel = normalizeManifestPath(rel)
		if rel == "" {
			return nil
		}
		if shouldSkipManifestFile(rel, name) {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		digest, err := hashFileSHA256(path)
		if err != nil {
			return err
		}
		manifest.Files = append(manifest.Files, ManifestFile{
			Path:   rel,
			Size:   info.Size(),
			Digest: digest,
			Mode:   int64(info.Mode().Perm()),
		})
		return nil
	})
	if err != nil {
		return Manifest{}, err
	}
	normalized := NormalizeManifest(manifest)
	if err := ValidateManifest(normalized); err != nil {
		return Manifest{}, err
	}
	return normalized, nil
}

func hashFileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()

	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
}

func shouldSkipManifestDir(rel, name string) bool {
	name = strings.TrimSpace(name)
	rel = strings.TrimSpace(rel)
	if name == ".git" {
		return true
	}
	if rel == ".dalek/runtime" {
		return true
	}
	return false
}

func shouldSkipManifestFile(rel, name string) bool {
	rel = strings.TrimSpace(rel)
	name = strings.TrimSpace(name)
	if strings.HasPrefix(rel, ".dalek/runtime/") {
		return true
	}
	switch {
	case strings.HasSuffix(name, ".sqlite3"),
		strings.HasSuffix(name, ".sqlite3-wal"),
		strings.HasSuffix(name, ".sqlite3-shm"),
		strings.HasSuffix(name, ".db-wal"),
		strings.HasSuffix(name, ".db-shm"):
		return true
	default:
		return false
	}
}
