package repo

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"dalek/internal/infra"
)

// FindRepoRoot 返回一个“跨 worktree 稳定”的 repo root。
// 实现严格依赖 `git rev-parse --path-format=absolute --git-common-dir`，
// 从 common .git 反推主 repo 根目录（<root>/.git 的父目录）。
func FindRepoRoot(startDir string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	out, err := infra.Run(ctx, startDir, "git", "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return "", fmt.Errorf("无法定位 git common-dir（需要支持 --path-format 的 git）: %w", err)
	}
	common := strings.TrimSpace(out)
	if common == "" {
		return "", fmt.Errorf("无法定位 git common-dir（输出为空）")
	}
	if !filepath.IsAbs(common) {
		if absStart, aerr := filepath.Abs(startDir); aerr == nil && absStart != "" {
			common = filepath.Join(absStart, common)
		}
	}
	if absCommon, aerr := filepath.Abs(common); aerr == nil && absCommon != "" {
		common = absCommon
	}
	if filepath.Base(common) != ".git" {
		return "", fmt.Errorf("git common-dir 不是 .git 路径: %s", common)
	}
	root := filepath.Dir(common)
	if strings.TrimSpace(root) == "" {
		return "", fmt.Errorf("无法从 common-dir 反推 repo root")
	}
	return root, nil
}
