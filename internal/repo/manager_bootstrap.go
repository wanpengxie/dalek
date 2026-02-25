package repo

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type managerProjectMeta struct {
	Schema   string    `json:"schema"`
	Name     string    `json:"name"`
	Key      string    `json:"key"`
	RepoRoot string    `json:"repo_root"`
	Created  time.Time `json:"created_at"`
}

// EnsureManagerBootstrap 在项目状态目录（<repoRoot>/.dalek/）下补齐元信息文件。
//
// 约束：
// - 尽量只“补齐缺失”，不覆盖用户已有文件。
func EnsureManagerBootstrap(layout Layout, projectName string) error {
	if strings.TrimSpace(layout.ProjectDir) == "" {
		return fmt.Errorf("project_dir 为空")
	}
	if err := os.MkdirAll(layout.ProjectDir, 0o755); err != nil {
		return fmt.Errorf("创建目录失败: %w", err)
	}

	repoRoot := strings.TrimSpace(layout.RepoRoot)
	key := ProjectKey(repoRoot)

	_, _ = writeFileIfMissing(filepath.Join(layout.ProjectDir, ".dalek_project_name"), strings.TrimSpace(projectName)+"\n", 0o644)
	_, _ = writeFileIfMissing(filepath.Join(layout.ProjectDir, ".dalek_repo_path"), repoRoot+"\n", 0o644)
	if exe, err := os.Executable(); err == nil && strings.TrimSpace(exe) != "" {
		_, _ = writeFileIfMissing(filepath.Join(layout.ProjectDir, ".dalek_bin_path"), strings.TrimSpace(exe)+"\n", 0o644)
	}

	metaPath := filepath.Join(layout.ProjectDir, ".dalek_project.json")
	if _, err := os.Stat(metaPath); err != nil && os.IsNotExist(err) {
		m := managerProjectMeta{
			Schema:   "dalek.project_meta.v0",
			Name:     strings.TrimSpace(projectName),
			Key:      key,
			RepoRoot: repoRoot,
			Created:  time.Now(),
		}
		if b, err := json.MarshalIndent(m, "", "  "); err == nil {
			b = append(b, '\n')
			_ = os.WriteFile(metaPath, b, 0o644)
		}
	}

	return nil
}
