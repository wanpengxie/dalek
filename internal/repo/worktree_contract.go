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

// ContractPaths 描述 worktree 下 `.dalek/` 的最小运行态契约路径集合。
// 这些路径是跨 services 的协议，不应该散落在某个具体领域包里。
// 与 worker `.dalek/agent-kernel.md` 主文件约定保持一致。
type ContractPaths struct {
	Dir           string
	AgentKernelMD string
	StateJSON     string
}

func contractPaths(worktreeRoot string) ContractPaths {
	dir := filepath.Join(worktreeRoot, ".dalek")
	return ContractPaths{
		Dir:           dir,
		AgentKernelMD: filepath.Join(dir, "agent-kernel.md"),
		StateJSON:     filepath.Join(dir, "state.json"),
	}
}

// EnsureWorktreeContract 确保 worktree 下 `.dalek/` 目录存在，并为 worker worktree
// 安装本地 git 保护：
// - `.git/info/exclude` 忽略未跟踪的 `.dalek/`
// - 对已跟踪的 `.dalek/*` 标记 skip-worktree，避免 worker 临时改动进入 worktree diff
//
// 约束：
// - 该函数不生成任何运行态文件或额外子目录。
// - PM/worker 的语义产物由各自执行链路按需写入。
func EnsureWorktreeContract(worktreeRoot string) (ContractPaths, error) {
	worktreeRoot = strings.TrimSpace(worktreeRoot)
	if worktreeRoot == "" {
		return ContractPaths{}, fmt.Errorf("worktreeRoot 为空")
	}
	cp := contractPaths(worktreeRoot)
	if err := os.MkdirAll(cp.Dir, 0o755); err != nil {
		return ContractPaths{}, fmt.Errorf("创建目录失败: %w", err)
	}
	if err := ensureWorktreeDalekGitProtection(worktreeRoot); err != nil {
		return ContractPaths{}, err
	}

	return cp, nil
}

func ensureWorktreeDalekGitProtection(worktreeRoot string) error {
	ok, err := isGitWorktree(worktreeRoot)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	if err := infra.EnsureRepoLocalIgnore(worktreeRoot, ".dalek/"); err != nil {
		return fmt.Errorf("写入 worktree .git/info/exclude 失败: %w", err)
	}
	paths, err := trackedDalekPaths(worktreeRoot)
	if err != nil {
		return err
	}
	if len(paths) == 0 {
		return nil
	}
	args := append([]string{"-C", worktreeRoot, "update-index", "--skip-worktree", "--"}, paths...)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := infra.Run(ctx, "", "git", args...); err != nil {
		return fmt.Errorf("标记 .dalek skip-worktree 失败: %w", err)
	}
	return nil
}

func isGitWorktree(worktreeRoot string) (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	code, out, _, err := infra.RunExitCode(ctx, worktreeRoot, "git", "rev-parse", "--is-inside-work-tree")
	if err != nil {
		return false, fmt.Errorf("检查 worktree git 上下文失败: %w", err)
	}
	return code == 0 && strings.TrimSpace(out) == "true", nil
}

func trackedDalekPaths(worktreeRoot string) ([]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := infra.Run(ctx, worktreeRoot, "git", "ls-files", "--", ".dalek")
	if err != nil {
		return nil, fmt.Errorf("列出已跟踪 .dalek 路径失败: %w", err)
	}
	if strings.TrimSpace(out) == "" {
		return nil, nil
	}
	lines := strings.Split(out, "\n")
	paths := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		paths = append(paths, line)
	}
	return paths, nil
}
