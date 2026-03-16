package snapshot

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type FileStore struct {
	rootDir string
}

func NewFileStore(rootDir string) (*FileStore, error) {
	rootDir = strings.TrimSpace(rootDir)
	if rootDir == "" {
		return nil, fmt.Errorf("snapshot store rootDir 不能为空")
	}
	absRoot, err := filepath.Abs(rootDir)
	if err != nil {
		return nil, err
	}
	return &FileStore{rootDir: absRoot}, nil
}

func (s *FileStore) RootDir() string {
	if s == nil {
		return ""
	}
	return s.rootDir
}

func (s *FileStore) SnapshotDir(snapshotID string) (string, error) {
	if s == nil {
		return "", fmt.Errorf("snapshot file store 未初始化")
	}
	snapshotID = normalizeSnapshotPathSegment(snapshotID)
	if snapshotID == "" {
		return "", fmt.Errorf("snapshot_id 不能为空")
	}
	return filepath.Join(s.rootDir, snapshotID), nil
}

func (s *FileStore) ManifestPath(snapshotID string) (string, error) {
	dir, err := s.SnapshotDir(snapshotID)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "manifest.json"), nil
}

func (s *FileStore) WriteManifestPack(snapshotID string, manifest Manifest) (string, string, error) {
	if s == nil {
		return "", "", fmt.Errorf("snapshot file store 未初始化")
	}
	digest, raw, err := ComputeManifestDigest(manifest)
	if err != nil {
		return "", "", err
	}
	dir, err := s.SnapshotDir(snapshotID)
	if err != nil {
		return "", "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", "", err
	}
	path := filepath.Join(dir, "manifest.json")
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		return "", "", err
	}
	return path, digest, nil
}

func (s *FileStore) LoadManifestPack(snapshotID string) (Manifest, string, string, error) {
	if s == nil {
		return Manifest{}, "", "", fmt.Errorf("snapshot file store 未初始化")
	}
	path, err := s.ManifestPath(snapshotID)
	if err != nil {
		return Manifest{}, "", "", err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return Manifest{}, "", "", err
	}
	var manifest Manifest
	if err := decodeManifestJSON(raw, &manifest); err != nil {
		return Manifest{}, "", "", err
	}
	digest, normalizedRaw, err := ComputeManifestDigest(manifest)
	if err != nil {
		return Manifest{}, "", "", err
	}
	return NormalizeManifest(manifest), digest, normalizedRaw, nil
}

func normalizeSnapshotPathSegment(raw string) string {
	raw = strings.TrimSpace(raw)
	raw = filepath.Base(raw)
	raw = strings.TrimSpace(raw)
	switch raw {
	case "", ".", string(filepath.Separator):
		return ""
	default:
		return raw
	}
}

func (s *FileStore) ManifestUploadTempPath(snapshotID string) (string, error) {
	dir, err := s.SnapshotDir(snapshotID)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "manifest.upload"), nil
}

func (s *FileStore) ManifestUploadStatePath(snapshotID string) (string, error) {
	dir, err := s.SnapshotDir(snapshotID)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "manifest.upload.json"), nil
}
