package repo

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	ProjectMetaSchemaV1 = "dalek.project_meta.v1"
	ProjectMetaSchemaV0 = "dalek.project_meta.v0"
)

type ProjectMeta struct {
	Schema       string    `json:"schema"`
	Name         string    `json:"name"`
	Key          string    `json:"key"`
	RepoRoot     string    `json:"repo_root"`
	CreatedAt    time.Time `json:"created_at"`
	DalekVersion string    `json:"dalek_version,omitempty"`
	UpgradedAt   time.Time `json:"upgraded_at,omitempty"`
}

func ProjectMetaPath(layout Layout) string {
	return filepath.Join(strings.TrimSpace(layout.ProjectDir), ".dalek_project.json")
}

func NormalizeDalekVersion(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return "dev"
	}
	return v
}

func ShouldUpgradeProject(currentVersion, targetVersion string, force bool) bool {
	if force {
		return true
	}
	target := NormalizeDalekVersion(targetVersion)
	current := strings.TrimSpace(currentVersion)
	if current == "" {
		return true
	}
	return current != target
}

func LoadProjectMeta(path string) (ProjectMeta, bool, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return ProjectMeta{}, false, fmt.Errorf("project meta path 为空")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return ProjectMeta{}, false, nil
		}
		return ProjectMeta{}, false, err
	}
	var meta ProjectMeta
	if err := json.Unmarshal(raw, &meta); err != nil {
		return ProjectMeta{}, true, fmt.Errorf("解析 project meta 失败: %w", err)
	}
	return meta.WithDefaults(), true, nil
}

func WriteProjectMetaAtomic(path string, meta ProjectMeta) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return fmt.Errorf("project meta path 为空")
	}
	meta = meta.WithDefaults()
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".dalek_project.json.*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func EnsureProjectMeta(path, name, key, repoRoot string, createdAt time.Time) error {
	meta, exists, err := LoadProjectMeta(path)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	if createdAt.IsZero() {
		createdAt = time.Now()
	}
	meta = ProjectMeta{
		Schema:    ProjectMetaSchemaV1,
		Name:      strings.TrimSpace(name),
		Key:       strings.TrimSpace(key),
		RepoRoot:  strings.TrimSpace(repoRoot),
		CreatedAt: createdAt,
	}
	return WriteProjectMetaAtomic(path, meta)
}

func RecordProjectDalekVersion(path, projectName, projectKey, repoRoot, dalekVersion string, upgradedAt time.Time) (ProjectMeta, error) {
	meta, exists, err := LoadProjectMeta(path)
	if err != nil {
		return ProjectMeta{}, err
	}
	if !exists {
		meta = ProjectMeta{
			Schema:    ProjectMetaSchemaV1,
			Name:      strings.TrimSpace(projectName),
			Key:       strings.TrimSpace(projectKey),
			RepoRoot:  strings.TrimSpace(repoRoot),
			CreatedAt: time.Now(),
		}
	}
	if upgradedAt.IsZero() {
		upgradedAt = time.Now()
	}
	meta.Schema = ProjectMetaSchemaV1
	if strings.TrimSpace(projectName) != "" {
		meta.Name = strings.TrimSpace(projectName)
	}
	if strings.TrimSpace(projectKey) != "" {
		meta.Key = strings.TrimSpace(projectKey)
	}
	if strings.TrimSpace(repoRoot) != "" {
		meta.RepoRoot = strings.TrimSpace(repoRoot)
	}
	meta.DalekVersion = NormalizeDalekVersion(dalekVersion)
	meta.UpgradedAt = upgradedAt.UTC()
	if meta.CreatedAt.IsZero() {
		meta.CreatedAt = meta.UpgradedAt
	}
	if err := WriteProjectMetaAtomic(path, meta); err != nil {
		return ProjectMeta{}, err
	}
	return meta, nil
}

func (m ProjectMeta) WithDefaults() ProjectMeta {
	out := m
	out.Schema = strings.TrimSpace(out.Schema)
	if out.Schema == "" || out.Schema == ProjectMetaSchemaV0 {
		out.Schema = ProjectMetaSchemaV1
	}
	out.Name = strings.TrimSpace(out.Name)
	out.Key = strings.TrimSpace(out.Key)
	out.RepoRoot = strings.TrimSpace(out.RepoRoot)
	out.DalekVersion = strings.TrimSpace(out.DalekVersion)
	return out
}
