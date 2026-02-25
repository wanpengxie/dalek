package infra

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type GitClient interface {
	CurrentBranch(repoRoot string) (string, error)
	AddWorktree(repoRoot, path, branch, baseBranch string) error
	RemoveWorktree(repoRoot, path string, force bool) error
	WorktreeDirty(path string) (bool, error)
	HasCommit(repoRoot string) (bool, error)
	WorktreeBranchCheckedOut(repoRoot, branch string) (bool, string, error)
	PruneWorktrees(repoRoot string) error
	IsWorktreeDir(path string) bool
}

type GitExecClient struct {
	runner CommandRunner
}

func NewGitExecClient() *GitExecClient {
	return &GitExecClient{runner: NewExecRunner()}
}

func NewGitExecClientWithRunner(r CommandRunner) *GitExecClient {
	if r == nil {
		r = NewExecRunner()
	}
	return &GitExecClient{runner: r}
}

func (g *GitExecClient) CurrentBranch(repoRoot string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return g.runner.Run(ctx, repoRoot, "git", "branch", "--show-current")
}

func (g *GitExecClient) HasCommit(repoRoot string) (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	code, _, _, err := g.runner.RunExitCode(ctx, repoRoot, "git", "rev-parse", "--verify", "HEAD")
	if err != nil {
		return false, err
	}
	return code == 0, nil
}

func (g *GitExecClient) AddWorktree(repoRoot, path, branch, baseBranch string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	exists, err := g.branchExists(repoRoot, branch)
	if err != nil {
		return err
	}
	if exists {
		_, err := g.runner.Run(ctx, repoRoot, "git", "worktree", "add", path, branch)
		return err
	}

	baseBranch = strings.TrimSpace(baseBranch)
	if baseBranch == "" {
		baseBranch = "HEAD"
	}
	if _, err := g.runner.Run(ctx, repoRoot, "git", "worktree", "add", "-b", branch, path, baseBranch); err == nil {
		return nil
	}
	_, err = g.runner.Run(ctx, repoRoot, "git", "worktree", "add", "-b", branch, path, "HEAD")
	return err
}

func (g *GitExecClient) RemoveWorktree(repoRoot, path string, force bool) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	args := []string{"worktree", "remove"}
	if force {
		args = append(args, "--force")
	}
	args = append(args, strings.TrimSpace(path))
	_, err := g.runner.Run(ctx, repoRoot, "git", args...)
	return err
}

func (g *GitExecClient) WorktreeDirty(path string) (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := g.runner.Run(ctx, "", "git", "-C", strings.TrimSpace(path), "status", "--porcelain")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(out) != "", nil
}

func (g *GitExecClient) WorktreeBranchCheckedOut(repoRoot, branch string) (bool, string, error) {
	branch = strings.TrimSpace(branch)
	if branch == "" {
		return false, "", nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := g.runner.Run(ctx, repoRoot, "git", "worktree", "list", "--porcelain")
	if err != nil {
		return false, "", err
	}

	want := "refs/heads/" + branch
	worktreePath := ""
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			worktreePath = ""
			continue
		}
		if strings.HasPrefix(line, "worktree ") {
			worktreePath = strings.TrimSpace(strings.TrimPrefix(line, "worktree "))
			continue
		}
		if strings.HasPrefix(line, "branch ") {
			b := strings.TrimSpace(strings.TrimPrefix(line, "branch "))
			if b == want {
				return true, worktreePath, nil
			}
			continue
		}
	}
	return false, "", nil
}

func (g *GitExecClient) PruneWorktrees(repoRoot string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// 先用 expire now，尽快清理“已删除目录但 metadata 仍残留”的场景；失败则退回默认 prune。
	if _, err := g.runner.Run(ctx, repoRoot, "git", "worktree", "prune", "--expire", "now"); err == nil {
		return nil
	}
	_, err := g.runner.Run(ctx, repoRoot, "git", "worktree", "prune")
	return err
}

func (g *GitExecClient) IsWorktreeDir(path string) bool {
	st, err := os.Stat(filepath.Join(path, ".git"))
	if err != nil {
		return false
	}
	return !st.IsDir()
}

func (g *GitExecClient) branchExists(repoRoot, branch string) (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	code, _, _, err := g.runner.RunExitCode(ctx, repoRoot, "git", "show-ref", "--verify", "--quiet", "refs/heads/"+branch)
	if err != nil {
		return false, err
	}
	return code == 0, nil
}
