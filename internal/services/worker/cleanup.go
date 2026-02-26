package worker

import (
	"context"
	"dalek/internal/contracts"
	"fmt"
	"os"
	"strings"
	"time"

	"dalek/internal/store"

	"gorm.io/gorm"
)

type CleanupWorktreeOptions struct {
	Force  bool
	DryRun bool
}

type CleanupWorktreeResult struct {
	TicketID    uint
	WorkerID    uint
	Worktree    string
	Branch      string
	RequestedAt *time.Time
	CleanedAt   *time.Time

	DryRun      bool
	Pending     bool
	Cleaned     bool
	Dirty       bool
	SessionLive bool
	Message     string
}

func (s *Service) CountPendingWorktreeCleanup(ctx context.Context) (int64, error) {
	db, err := s.db()
	if err != nil {
		return 0, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	var cnt int64
	if err := db.WithContext(ctx).
		Table("workers AS w").
		Joins("JOIN tickets AS t ON t.id = w.ticket_id").
		Where("t.workflow_status = ?", contracts.TicketArchived).
		Where("w.worktree_gc_requested_at IS NOT NULL").
		Where("w.worktree_gc_cleaned_at IS NULL").
		Where("TRIM(COALESCE(w.worktree_path, '')) != ''").
		Count(&cnt).Error; err != nil {
		return 0, err
	}
	return cnt, nil
}

func (s *Service) CleanupTicketWorktree(ctx context.Context, ticketID uint, opt CleanupWorktreeOptions) (CleanupWorktreeResult, error) {
	p, err := s.require()
	if err != nil {
		return CleanupWorktreeResult{}, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if ticketID == 0 {
		return CleanupWorktreeResult{}, fmt.Errorf("ticket_id 不能为空")
	}

	var t store.Ticket
	if err := p.DB.WithContext(ctx).First(&t, ticketID).Error; err != nil {
		return CleanupWorktreeResult{}, err
	}
	if t.WorkflowStatus != contracts.TicketArchived && !opt.Force {
		return CleanupWorktreeResult{}, fmt.Errorf("ticket 不是 archived，拒绝清理（可用 --force）")
	}

	var w store.Worker
	if err := p.DB.WithContext(ctx).Where("ticket_id = ?", ticketID).Order("id DESC").First(&w).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return CleanupWorktreeResult{
				TicketID: ticketID,
				Message:  "ticket 没有关联 worker，无需清理",
			}, nil
		}
		return CleanupWorktreeResult{}, err
	}

	result := CleanupWorktreeResult{
		TicketID: ticketID,
		WorkerID: w.ID,
		Worktree: strings.TrimSpace(w.WorktreePath),
		Branch:   strings.TrimSpace(w.Branch),
		DryRun:   opt.DryRun,
		Pending:  true,
	}

	now := time.Now()
	requestedAt := w.WorktreeGCRequestedAt
	if requestedAt == nil {
		requestedAt = &now
		if err := s.persistCleanupState(ctx, w.ID, requestedAt, nil, ""); err != nil {
			return result, err
		}
	}
	result.RequestedAt = requestedAt

	var activeDispatch int64
	if err := p.DB.WithContext(ctx).
		Model(&store.PMDispatchJob{}).
		Where("ticket_id = ? AND status IN ?", ticketID, []contracts.PMDispatchJobStatus{contracts.PMDispatchPending, contracts.PMDispatchRunning}).
		Count(&activeDispatch).Error; err != nil {
		return result, err
	}
	if activeDispatch > 0 {
		return result, fmt.Errorf("ticket 存在进行中的 dispatch，拒绝清理")
	}
	if w.Status == contracts.WorkerRunning {
		return result, fmt.Errorf("worker 仍在 running，拒绝清理")
	}

	socket := strings.TrimSpace(w.TmuxSocket)
	if socket == "" {
		cfg, err := s.cfg()
		if err != nil {
			return result, err
		}
		socket = strings.TrimSpace(cfg.TmuxSocket)
	}
	session := strings.TrimSpace(w.TmuxSession)
	if socket != "" && session != "" {
		sessions, err := p.Tmux.ListSessions(ctx, socket)
		if err != nil && !opt.Force {
			return result, err
		}
		if err == nil && sessions[session] {
			result.SessionLive = true
			if !opt.Force {
				return result, fmt.Errorf("tmux session 仍存活：%s（可用 --force）", session)
			}
			if err := p.Tmux.KillSession(ctx, socket, session); err != nil {
				return result, err
			}
		}
	}

	worktreePath := strings.TrimSpace(w.WorktreePath)
	if worktreePath == "" {
		cleanedAt := time.Now()
		if err := s.persistCleanupState(ctx, w.ID, requestedAt, &cleanedAt, ""); err != nil {
			return result, err
		}
		result.Cleaned = true
		result.Pending = false
		result.CleanedAt = &cleanedAt
		result.Message = "worker 无 worktree_path，按已清理处理"
		return result, nil
	}

	if _, err := os.Stat(worktreePath); err != nil {
		if os.IsNotExist(err) {
			cleanedAt := time.Now()
			if err := s.persistCleanupState(ctx, w.ID, requestedAt, &cleanedAt, ""); err != nil {
				return result, err
			}
			_ = p.Git.PruneWorktrees(p.RepoRoot)
			result.Cleaned = true
			result.Pending = false
			result.CleanedAt = &cleanedAt
			result.Message = "worktree 目录不存在，按已清理处理"
			return result, nil
		}
		return result, err
	}

	if !p.Git.IsWorktreeDir(worktreePath) {
		return result, fmt.Errorf("目标路径不是 git worktree: %s", worktreePath)
	}

	dirty, err := p.Git.WorktreeDirty(worktreePath)
	if err != nil && !opt.Force {
		return result, err
	}
	result.Dirty = dirty
	if dirty && !opt.Force {
		return result, fmt.Errorf("worktree 存在未提交改动，拒绝清理（可用 --force）")
	}

	if opt.DryRun {
		if err := s.persistCleanupState(ctx, w.ID, requestedAt, nil, ""); err != nil {
			return result, err
		}
		result.Message = "dry-run：已标记待清理，未执行删除"
		return result, nil
	}

	if err := p.Git.RemoveWorktree(p.RepoRoot, worktreePath, opt.Force); err != nil {
		_ = s.persistCleanupState(ctx, w.ID, requestedAt, nil, err.Error())
		return result, err
	}
	if err := p.Git.PruneWorktrees(p.RepoRoot); err != nil {
		_ = s.persistCleanupState(ctx, w.ID, requestedAt, nil, err.Error())
		return result, err
	}

	cleanedAt := time.Now()
	if err := s.persistCleanupState(ctx, w.ID, requestedAt, &cleanedAt, ""); err != nil {
		return result, err
	}
	result.Cleaned = true
	result.Pending = false
	result.CleanedAt = &cleanedAt
	result.Message = "worktree 清理完成"
	return result, nil
}

func (s *Service) persistCleanupState(ctx context.Context, workerID uint, requestedAt, cleanedAt *time.Time, cleanupErr string) error {
	db, err := s.db()
	if err != nil {
		return err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return db.WithContext(ctx).Model(&store.Worker{}).Where("id = ?", workerID).Updates(map[string]any{
		"worktree_gc_requested_at": requestedAt,
		"worktree_gc_cleaned_at":   cleanedAt,
		"worktree_cleanup_error":   strings.TrimSpace(cleanupErr),
		"updated_at":               time.Now(),
	}).Error
}
