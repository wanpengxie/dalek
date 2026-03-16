package snapshot

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

type Manifest struct {
	BaseCommit          string         `json:"base_commit,omitempty"`
	WorkspaceGeneration string         `json:"workspace_generation,omitempty"`
	Files               []ManifestFile `json:"files,omitempty"`
}

type ManifestFile struct {
	Path   string `json:"path"`
	Size   int64  `json:"size,omitempty"`
	Digest string `json:"digest,omitempty"`
	Mode   int64  `json:"mode,omitempty"`
}

func NormalizeManifest(in Manifest) Manifest {
	out := Manifest{
		BaseCommit:          strings.TrimSpace(in.BaseCommit),
		WorkspaceGeneration: strings.TrimSpace(in.WorkspaceGeneration),
		Files:               make([]ManifestFile, 0, len(in.Files)),
	}
	for _, file := range in.Files {
		path := normalizeManifestPath(file.Path)
		if path == "" {
			continue
		}
		out.Files = append(out.Files, ManifestFile{
			Path:   path,
			Size:   file.Size,
			Digest: strings.TrimSpace(strings.ToLower(file.Digest)),
			Mode:   file.Mode,
		})
	}
	sort.Slice(out.Files, func(i, j int) bool {
		return out.Files[i].Path < out.Files[j].Path
	})
	return out
}

func ValidateManifest(in Manifest) error {
	normalized := NormalizeManifest(in)
	if normalized.BaseCommit == "" {
		return fmt.Errorf("manifest base_commit 不能为空")
	}
	if normalized.WorkspaceGeneration == "" {
		return fmt.Errorf("manifest workspace_generation 不能为空")
	}
	if len(normalized.Files) == 0 {
		return fmt.Errorf("manifest files 不能为空")
	}
	seen := make(map[string]struct{}, len(normalized.Files))
	for _, file := range normalized.Files {
		if file.Path == "" {
			return fmt.Errorf("manifest file path 不能为空")
		}
		if !strings.HasPrefix(file.Digest, "sha256:") {
			return fmt.Errorf("manifest file digest 非法: %s", file.Path)
		}
		if _, ok := seen[file.Path]; ok {
			return fmt.Errorf("manifest file path 重复: %s", file.Path)
		}
		seen[file.Path] = struct{}{}
	}
	return nil
}

func ComputeManifestDigest(in Manifest) (string, string, error) {
	normalized := NormalizeManifest(in)
	if err := ValidateManifest(normalized); err != nil {
		return "", "", err
	}
	raw, err := json.Marshal(normalized)
	if err != nil {
		return "", "", err
	}
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:]), string(raw), nil
}

func decodeManifestJSON(raw []byte, out *Manifest) error {
	if out == nil {
		return fmt.Errorf("manifest out 不能为空")
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return err
	}
	if err := ValidateManifest(*out); err != nil {
		return err
	}
	return nil
}

func parseManifestForUpload(raw, baseCommit, workspaceGeneration string) (Manifest, error) {
	var manifest Manifest
	if err := decodeManifestJSON([]byte(strings.TrimSpace(raw)), &manifest); err != nil {
		return Manifest{}, err
	}
	if trimmed := strings.TrimSpace(baseCommit); trimmed != "" {
		manifest.BaseCommit = trimmed
	}
	if trimmed := strings.TrimSpace(workspaceGeneration); trimmed != "" {
		manifest.WorkspaceGeneration = trimmed
	}
	normalized := NormalizeManifest(manifest)
	if err := ValidateManifest(normalized); err != nil {
		return Manifest{}, err
	}
	return normalized, nil
}

func normalizeManifestPath(raw string) string {
	path := strings.TrimSpace(raw)
	if path == "" {
		return ""
	}
	path = filepath.ToSlash(path)
	path = strings.TrimPrefix(path, "./")
	path = strings.TrimPrefix(path, "/")
	if path == "." {
		return ""
	}
	if strings.HasPrefix(path, "../") {
		return ""
	}
	cleaned := filepath.ToSlash(filepath.Clean(path))
	if cleaned == "." || strings.HasPrefix(cleaned, "../") {
		return ""
	}
	return cleaned
}
