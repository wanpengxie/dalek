package repo

import (
	"context"
	"strings"
	"time"

	"dalek/internal/infra"
)

type WorktreeGitBaseline struct {
	HeadSHA           string
	WorkingTreeStatus string
	LastCommitSubject string
}

func InspectWorktreeGitBaseline(ctx context.Context, worktreePath string, git infra.GitClient) WorktreeGitBaseline {
	facts := WorktreeGitBaseline{
		HeadSHA:           "unknown",
		WorkingTreeStatus: "unknown",
		LastCommitSubject: "unknown",
	}
	if ctx == nil {
		ctx = context.Background()
	}
	worktreePath = strings.TrimSpace(worktreePath)
	if worktreePath == "" {
		return facts
	}
	if git != nil {
		if dirty, err := git.WorktreeDirty(worktreePath); err == nil {
			if dirty {
				facts.WorkingTreeStatus = "dirty"
			} else {
				facts.WorkingTreeStatus = "clean"
			}
		}
	}
	if out, err := runWorktreeGitCommand(ctx, worktreePath, "rev-parse", "HEAD"); err == nil {
		facts.HeadSHA = out
	}
	if out, err := runWorktreeGitCommand(ctx, worktreePath, "log", "-1", "--pretty=%s"); err == nil {
		facts.LastCommitSubject = out
	}
	return facts
}

func runWorktreeGitCommand(ctx context.Context, worktreePath string, args ...string) (string, error) {
	gitCtx := ctx
	if gitCtx == nil {
		gitCtx = context.Background()
	}
	timeoutCtx, cancel := context.WithTimeout(gitCtx, 2*time.Second)
	defer cancel()
	cmdArgs := append([]string{"-C", strings.TrimSpace(worktreePath)}, args...)
	out, err := infra.Run(timeoutCtx, "", "git", cmdArgs...)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}
