package pm

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

type mergeResult int

const (
	mergeSuccess  mergeResult = iota
	mergeConflict             // 可解决的冲突
	mergeError                // 其他错误
)

// gitMergeTicketBranch 在 repo root 上执行 git merge。
func (s *Service) gitMergeTicketBranch(ctx context.Context, workerBranch, targetBranch string) (mergeResult, error) {
	repoRoot := s.p.RepoRoot

	// 确保在 target 分支上
	if out, err := runGit(ctx, repoRoot, "checkout", targetBranch); err != nil {
		return mergeError, fmt.Errorf("git checkout %s 失败: %s: %w", targetBranch, out, err)
	}

	// 尝试 merge
	out, err := runGit(ctx, repoRoot, "merge", workerBranch, "--no-edit")
	if err == nil {
		return mergeSuccess, nil
	}
	if strings.Contains(strings.ToLower(out), "conflict") || strings.Contains(strings.ToLower(out), "merge_msg") {
		return mergeConflict, nil
	}
	return mergeError, fmt.Errorf("git merge %s 失败: %s: %w", workerBranch, out, err)
}

// gitMergeAbort 取消正在进行的 merge（确保 git 状态干净）。
func (s *Service) gitMergeAbort(ctx context.Context) {
	repoRoot := s.p.RepoRoot
	_, _ = runGit(ctx, repoRoot, "merge", "--abort")
}

// gitHasConflicts 检查当前 repo root 是否有 unmerged 文件。
func (s *Service) gitHasConflicts(ctx context.Context) bool {
	repoRoot := s.p.RepoRoot
	out, _ := runGit(ctx, repoRoot, "diff", "--name-only", "--diff-filter=U")
	return strings.TrimSpace(out) != ""
}

// gitConflictFiles 返回冲突文件列表。
func (s *Service) gitConflictFiles(ctx context.Context) []string {
	repoRoot := s.p.RepoRoot
	out, _ := runGit(ctx, repoRoot, "diff", "--name-only", "--diff-filter=U")
	out = strings.TrimSpace(out)
	if out == "" {
		return nil
	}
	return strings.Split(out, "\n")
}

// workerBranchForTicket 返回 ticket 对应的 worker 分支名。
func (s *Service) workerBranchForTicket(ctx context.Context, ticketID uint) (string, error) {
	_, db, err := s.require()
	if err != nil {
		return "", err
	}
	var w struct {
		GitBranch string
	}
	err = db.WithContext(ctx).
		Table("workers").
		Select("git_branch").
		Where("ticket_id = ?", ticketID).
		Order("id desc").
		Limit(1).
		Scan(&w).Error
	if err != nil {
		return "", fmt.Errorf("查询 ticket %d worker 分支失败: %w", ticketID, err)
	}
	if strings.TrimSpace(w.GitBranch) == "" {
		return "", fmt.Errorf("ticket %d 无 worker 分支", ticketID)
	}
	return strings.TrimSpace(w.GitBranch), nil
}

// targetBranchForTicket 返回 ticket 的目标分支（默认 main）。
func (s *Service) targetBranchForTicket(ctx context.Context, ticketID uint) string {
	_, db, err := s.require()
	if err != nil {
		return "main"
	}
	var t struct {
		TargetBranch string
	}
	err = db.WithContext(ctx).
		Table("tickets").
		Select("target_branch").
		Where("id = ?", ticketID).
		Scan(&t).Error
	if err != nil || strings.TrimSpace(t.TargetBranch) == "" {
		return "main"
	}
	return strings.TrimSpace(t.TargetBranch)
}

func runGit(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return string(out), err
}
