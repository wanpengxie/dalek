package repo

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

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

	metaPath := ProjectMetaPath(layout)
	if err := EnsureProjectMeta(metaPath, projectName, key, repoRoot, time.Now()); err != nil {
		return err
	}

	return nil
}
