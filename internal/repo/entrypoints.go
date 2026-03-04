package repo

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"dalek/internal/infra"
)

const (
	agentKernelRefLine = "@.dalek/agent-kernel.md"
	injectBlockBegin   = "<!-- DALEK:INJECT:BEGIN -->"
	injectBlockEnd     = "<!-- DALEK:INJECT:END -->"
)

// EnsureRepoAgentEntryPoints 在 repo root 里确保入口文件存在且两者都能工作：
//
// - CLAUDE.md / AGENTS.md 只在“项目初始化”阶段处理一次（避免每个 worktree/分支都被动改动 tracked 文件）。
// - 若缺少其中一个入口文件，则用 symlink 指向另一个，保证两者内容一致。
// - 两者都存在时不做链接，仅补齐 include 行与注入分区（幂等）。
func EnsureRepoAgentEntryPoints(repoRoot string) error {
	repoRoot = strings.TrimSpace(repoRoot)
	if repoRoot == "" {
		return fmt.Errorf("repo_root 为空")
	}

	claudePath := filepath.Join(repoRoot, "CLAUDE.md")
	agentsPath := filepath.Join(repoRoot, "AGENTS.md")

	claudeExists, claudeIsDir, err := pathExists(claudePath)
	if err != nil {
		return err
	}
	if claudeIsDir {
		return fmt.Errorf("路径是目录，无法作为入口文件: %s", claudePath)
	}

	agentsExists, agentsIsDir, err := pathExists(agentsPath)
	if err != nil {
		return err
	}
	if agentsIsDir {
		return fmt.Errorf("路径是目录，无法作为入口文件: %s", agentsPath)
	}

	switch {
	case claudeExists && agentsExists:
		if err := ensureEntryInjection(claudePath); err != nil {
			return err
		}
		if err := ensureEntryInjection(agentsPath); err != nil {
			return err
		}
		return nil

	case claudeExists && !agentsExists:
		if err := os.Symlink("CLAUDE.md", agentsPath); err != nil {
			return fmt.Errorf("创建 AGENTS.md -> CLAUDE.md 链接失败: %w", err)
		}
		return ensureEntryInjection(claudePath)

	case !claudeExists && agentsExists:
		if err := os.Symlink("AGENTS.md", claudePath); err != nil {
			return fmt.Errorf("创建 CLAUDE.md -> AGENTS.md 链接失败: %w", err)
		}
		return ensureEntryInjection(agentsPath)

	default:
		min := `# 项目指令入口

`
		if err := os.WriteFile(agentsPath, []byte(min), 0o644); err != nil {
			return err
		}
		if err := os.Symlink("AGENTS.md", claudePath); err != nil {
			return fmt.Errorf("创建 CLAUDE.md -> AGENTS.md 链接失败: %w", err)
		}
		return ensureEntryInjection(agentsPath)
	}
}

func ensureEntryInjection(path string) error {
	if err := infra.EnsureLineInFile(path, agentKernelRefLine); err != nil {
		return err
	}
	return ensureInjectedBlock(path, defaultRepoEntrypointInjectBlock())
}

func defaultRepoEntrypointInjectBlock() string {
	block := strings.TrimSpace(mustReadSeedTemplate("templates/project/ENTRYPOINT_INJECT.md"))
	if !strings.Contains(block, injectBlockBegin) || !strings.Contains(block, injectBlockEnd) {
		panic("templates/project/ENTRYPOINT_INJECT.md 缺少注入分区标记")
	}
	return block
}

func ensureInjectedBlock(path, block string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	doc := string(raw)
	base := strings.TrimSpace(stripInjectBlocks(doc))
	updated := block
	if base != "" {
		updated += "\n\n" + base
	}

	updated = strings.TrimRight(updated, "\n") + "\n"
	if updated == doc {
		return nil
	}
	return os.WriteFile(path, []byte(updated), 0o644)
}

func stripInjectBlocks(doc string) string {
	lines := strings.Split(doc, "\n")
	out := make([]string, 0, len(lines))
	inBlock := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == injectBlockBegin {
			inBlock = true
			continue
		}
		if trimmed == injectBlockEnd {
			inBlock = false
			continue
		}
		if inBlock {
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

// OverwriteRepoAgentEntryPoints upgrade 专用：完整覆写 CLAUDE.md/AGENTS.md 为最新模板内容。
// symlink 逻辑不变（缺失一个就创建 symlink）。
func OverwriteRepoAgentEntryPoints(repoRoot string) error {
	repoRoot = strings.TrimSpace(repoRoot)
	if repoRoot == "" {
		return fmt.Errorf("repo_root 为空")
	}

	claudePath := filepath.Join(repoRoot, "CLAUDE.md")
	agentsPath := filepath.Join(repoRoot, "AGENTS.md")

	block := defaultRepoEntrypointInjectBlock()
	content := block + "\n\n" + agentKernelRefLine + "\n"

	claudeExists, _, err := pathExists(claudePath)
	if err != nil {
		return err
	}
	agentsExists, _, err := pathExists(agentsPath)
	if err != nil {
		return err
	}

	switch {
	case claudeExists && agentsExists:
		if isSymlink(claudePath) {
			return os.WriteFile(agentsPath, []byte(content), 0o644)
		}
		if isSymlink(agentsPath) {
			return os.WriteFile(claudePath, []byte(content), 0o644)
		}
		if err := os.WriteFile(claudePath, []byte(content), 0o644); err != nil {
			return err
		}
		return os.WriteFile(agentsPath, []byte(content), 0o644)

	case claudeExists && !agentsExists:
		if err := os.WriteFile(claudePath, []byte(content), 0o644); err != nil {
			return err
		}
		return os.Symlink("CLAUDE.md", agentsPath)

	case !claudeExists && agentsExists:
		if err := os.WriteFile(agentsPath, []byte(content), 0o644); err != nil {
			return err
		}
		return os.Symlink("AGENTS.md", claudePath)

	default:
		if err := os.WriteFile(agentsPath, []byte(content), 0o644); err != nil {
			return err
		}
		return os.Symlink("AGENTS.md", claudePath)
	}
}

func isSymlink(path string) bool {
	fi, err := os.Lstat(path)
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeSymlink != 0
}

func EnsureRepoAgentEntryPointsVersioned(repoRoot string) error {
	repoRoot = strings.TrimSpace(repoRoot)
	if repoRoot == "" {
		return fmt.Errorf("repo_root 为空")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := infra.Run(ctx, repoRoot, "git", "rev-parse", "--is-inside-work-tree"); err != nil {
		return nil
	}
	if _, err := infra.Run(ctx, repoRoot, "git", "add", "-f", "--", "AGENTS.md", "CLAUDE.md"); err != nil {
		return fmt.Errorf("git add 入口文件失败: %w", err)
	}
	staged, err := infra.Run(ctx, repoRoot, "git", "diff", "--cached", "--name-only", "--", "AGENTS.md", "CLAUDE.md")
	if err != nil {
		return fmt.Errorf("检查入口文件 staged 变更失败: %w", err)
	}
	if strings.TrimSpace(staged) == "" {
		return nil
	}
	if _, err := infra.Run(
		ctx,
		repoRoot,
		"git",
		"-c", "user.name=dalek",
		"-c", "user.email=dalek@local",
		"commit",
		"-m", "chore(dalek): ensure repo agent entry points",
		"--", "AGENTS.md", "CLAUDE.md",
	); err != nil {
		return fmt.Errorf("提交入口文件失败: %w", err)
	}
	return nil
}

func pathExists(path string) (exists bool, isDir bool, err error) {
	st, err := os.Lstat(path)
	if err == nil {
		return true, st.IsDir(), nil
	}
	if os.IsNotExist(err) {
		return false, false, nil
	}
	return false, false, err
}
