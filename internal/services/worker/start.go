package worker

import (
	"context"
	"crypto/rand"
	"dalek/internal/contracts"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"dalek/internal/repo"

	"gorm.io/gorm"
)

func (s *Service) defaultBranchPrefix() (string, error) {
	p, err := s.require()
	if err != nil {
		return "", err
	}
	prefix := strings.TrimSpace(p.Config.BranchPrefix)
	if prefix == "" {
		key := strings.TrimSpace(p.Key)
		if key == "" {
			key = "default"
		}
		prefix = fmt.Sprintf("ts/%s/", key)
	}
	return prefix, nil
}

func ticketSlug(title string) string {
	title = strings.TrimSpace(strings.ToLower(title))
	if title == "" {
		return ""
	}
	var b strings.Builder
	prevDash := false
	for _, r := range title {
		isAlphaNum := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if isAlphaNum {
			_, _ = b.WriteRune(r)
			prevDash = false
			continue
		}
		if b.Len() > 0 && !prevDash {
			_ = b.WriteByte('-')
			prevDash = true
		}
	}
	slug := strings.Trim(b.String(), "-")
	if len(slug) > 24 {
		slug = strings.Trim(slug[:24], "-")
	}
	return slug
}

func ticketRunNonce() string {
	buf := make([]byte, 4)
	if _, err := rand.Read(buf); err == nil {
		return hex.EncodeToString(buf)
	}
	return strconv.FormatInt(time.Now().UnixNano(), 36)
}

func (s *Service) newTicketRunName(t contracts.Ticket) string {
	base := fmt.Sprintf("t%d", t.ID)
	slug := ticketSlug(t.Title)
	if slug != "" {
		base += "-" + slug
	}
	return base + "-" + ticketRunNonce()
}

func (s *Service) ticketBranchName(t contracts.Ticket, runName string) (string, error) {
	prefix, err := s.defaultBranchPrefix()
	if err != nil {
		return "", err
	}
	runName = strings.TrimSpace(runName)
	if runName == "" {
		runName = fmt.Sprintf("t%d", t.ID)
	}
	return prefix + runName, nil
}

func (s *Service) ticketWorktreePath(runName string) (string, error) {
	p, err := s.require()
	if err != nil {
		return "", err
	}
	runName = strings.TrimSpace(runName)
	if runName == "" {
		return "", fmt.Errorf("runName 不能为空")
	}
	return filepath.Join(p.WorktreesDir, "ticket-"+runName), nil
}

type StartOptions struct {
	BaseBranch string
}

// StartTicketResources 启动一个 ticket 的执行资源（当前默认为 worktree + worker runtime 进程）。
//
// 注意：
// - worker 只负责“资源启动”，不做额外初始化脚本。
// - 这里返回的 worker 通常处于 creating，后续由 PM 的 start 流程标记为 running。
func (s *Service) StartTicketResources(ctx context.Context, ticketID uint) (*contracts.Worker, error) {
	return s.StartTicketResourcesWithOptions(ctx, ticketID, StartOptions{})
}

func (s *Service) StartTicketResourcesWithOptions(ctx context.Context, ticketID uint, opt StartOptions) (w *contracts.Worker, err error) {
	p, err := s.require()
	if err != nil {
		return nil, err
	}
	db := p.DB
	if ctx == nil {
		ctx = context.Background()
	}
	if ticketID == 0 {
		return nil, fmt.Errorf("ticket_id 不能为空")
	}
	ticketSvc, err := s.ticketSvc()
	if err != nil {
		return nil, err
	}

	worktreeCreated := false
	rollbackWorktree := ""
	defer func() {
		if err == nil {
			return
		}
		if worktreeCreated && strings.TrimSpace(rollbackWorktree) != "" {
			_ = p.Git.RemoveWorktree(p.RepoRoot, rollbackWorktree, true)
		}
	}()

	t, err := ticketSvc.GetByID(ctx, ticketID)
	if err != nil {
		return nil, err
	}

	w, err = s.LatestWorker(ctx, ticketID)
	if err != nil {
		return nil, err
	}

	branch := ""
	worktreePath := ""
	if w != nil {
		if strings.TrimSpace(w.Branch) != "" {
			branch = strings.TrimSpace(w.Branch)
		}
		if strings.TrimSpace(w.WorktreePath) != "" {
			worktreePath = strings.TrimSpace(w.WorktreePath)
		}
	}
	if strings.TrimSpace(branch) == "" || strings.TrimSpace(worktreePath) == "" {
		runName := s.newTicketRunName(*t)
		if strings.TrimSpace(branch) == "" {
			branch, err = s.ticketBranchName(*t, runName)
			if err != nil {
				return nil, err
			}
		}
		if strings.TrimSpace(worktreePath) == "" {
			worktreePath, err = s.ticketWorktreePath(runName)
			if err != nil {
				return nil, err
			}
		}
	}
	rollbackWorktree = worktreePath

	// 语义收敛后：一个 ticket 只绑定一个 worker 记录。
	// 若已在 running 且 runtime 句柄可用，start 直接返回，不重复初始化。
	if w != nil && w.Status == contracts.WorkerRunning {
		if hasWorkerRuntimeHandle(*w) {
			return w, nil
		}
	}

	if _, err := os.Stat(worktreePath); err == nil {
		if !p.Git.IsWorktreeDir(worktreePath) {
			return nil, fmt.Errorf("worktree 路径已存在但不是 git worktree: %s", worktreePath)
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	} else {
		has, herr := p.Git.HasCommit(p.RepoRoot)
		if herr != nil {
			return nil, herr
		}
		if !has {
			return nil, fmt.Errorf("当前仓库还没有任何 commit，无法创建 worktree；请先提交一次初始 commit")
		}

		// 防御：同一 repo 内，一个分支只能被一个 worktree checkout。
		if co, wtPath, cerr := p.Git.WorktreeBranchCheckedOut(p.RepoRoot, branch); cerr == nil && co {
			// 常见问题：用户/测试直接删除 worktree 目录，但 git 的 worktree metadata 仍残留，
			// 导致分支被视为“已 checkout”。这里做一次 best-effort prune 再重试。
			wtPath = strings.TrimSpace(wtPath)
			if wtPath != "" {
				if _, serr := os.Stat(wtPath); serr != nil && os.IsNotExist(serr) {
					_ = p.Git.PruneWorktrees(p.RepoRoot)
					if co2, wtPath2, cerr2 := p.Git.WorktreeBranchCheckedOut(p.RepoRoot, branch); cerr2 == nil {
						co = co2
						wtPath = strings.TrimSpace(wtPath2)
					}
				}
			}
			if !co {
				// prune 后不再冲突，继续执行 worktree add。
				goto addWorktree
			}
			if strings.TrimSpace(wtPath) == "" {
				wtPath = "(unknown)"
			}
			return nil, fmt.Errorf("分支已在另一个 worktree checkout：branch=%s at %s", strings.TrimSpace(branch), wtPath)
		}

	addWorktree:
		baseRef := strings.TrimSpace(opt.BaseBranch)
		if baseRef == "" {
			baseRef = "HEAD"
			if cur, cerr := p.Git.CurrentBranch(p.RepoRoot); cerr == nil && strings.TrimSpace(cur) != "" {
				baseRef = strings.TrimSpace(cur)
			}
		}
		if err := p.Git.AddWorktree(p.RepoRoot, worktreePath, branch, baseRef); err != nil {
			return nil, err
		}
		worktreeCreated = true
	}

	// worktree contract：无论 worktree 是新建还是复用，都尽量补齐 .dalek/ 契约骨架。
	_, cerr := repo.EnsureWorktreeContract(worktreePath)
	if cerr != nil {
		return nil, cerr
	}

	if w == nil {
		fresh := contracts.Worker{
			TicketID:     t.ID,
			Status:       contracts.WorkerStopped,
			WorktreePath: worktreePath,
			Branch:       branch,
			ProcessPID:   0,
			LogPath:      "",
			StartedAt:    nil,
			StoppedAt:    nil,
			LastError:    "",
		}
		now := time.Now()
		if err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			if err := tx.Create(&fresh).Error; err != nil {
				return err
			}
			return s.appendWorkerStatusEventTx(ctx, tx, fresh.ID, fresh.TicketID, contracts.WorkerStatus(""), contracts.WorkerStopped, "worker.start", "创建 worker 初始状态", map[string]any{
				"ticket_id": fresh.TicketID,
			}, now)
		}); err != nil {
			return nil, err
		}
		w = &fresh
	}

	logPath := repo.WorkerStreamLogPath(p.WorkersDir, w.ID)

	prevStatus := w.Status
	statusNow := time.Now()
	if err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&contracts.Worker{}).Where("id = ?", w.ID).Updates(map[string]any{
			"status":        contracts.WorkerCreating,
			"worktree_path": worktreePath,
			"branch":        branch,
			"process_pid":   0,
			"log_path":      logPath,
			"started_at":    nil,
			"stopped_at":    nil,
			"last_error":    "",
		}).Error; err != nil {
			return err
		}
		return s.appendWorkerStatusEventTx(ctx, tx, w.ID, w.TicketID, prevStatus, contracts.WorkerCreating, "worker.start", "start 进入 creating", map[string]any{
			"ticket_id": w.TicketID,
			"log_path":  strings.TrimSpace(logPath),
		}, statusNow)
	}); err != nil {
		return nil, err
	}
	w.Status = contracts.WorkerCreating

	// runtime-first：start 仅准备运行锚点（log path），不再拉起壳进程。
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		return nil, err
	}
	f, ferr := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if ferr != nil {
		return nil, ferr
	}
	_ = f.Close()

	startedAt := time.Now()
	if uerr := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&contracts.Worker{}).Where("id = ?", w.ID).Updates(map[string]any{
			"process_pid": 0,
			"log_path":    strings.TrimSpace(logPath),
			"started_at":  &startedAt,
			"stopped_at":  nil,
			"last_error":  "",
		}).Error; err != nil {
			return err
		}
		return s.appendWorkerStatusEventTx(ctx, tx, w.ID, w.TicketID, contracts.WorkerCreating, contracts.WorkerCreating, "worker.start", "runtime 锚点已准备（无壳进程）", map[string]any{
			"ticket_id": w.TicketID,
			"log_path":  strings.TrimSpace(logPath),
		}, startedAt)
	}); uerr != nil {
		return nil, uerr
	}
	w.WorktreePath = worktreePath
	w.Branch = branch
	w.ProcessPID = 0
	w.LogPath = strings.TrimSpace(logPath)

	var out contracts.Worker
	if err := db.First(&out, w.ID).Error; err != nil {
		return w, nil
	}
	return &out, nil
}

func (s *Service) StopTicket(ctx context.Context, ticketID uint) error {
	if _, err := s.require(); err != nil {
		return err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	w, err := s.LatestWorker(ctx, ticketID)
	if err != nil {
		return err
	}
	if w == nil || !hasWorkerRuntimeHandle(*w) {
		return fmt.Errorf("该 ticket 尚无可停止的 worker")
	}
	return s.StopWorker(ctx, w.ID)
}

// AttachCmd 返回该 ticket 最新 worker 的 attach 命令（runtime 日志 attach）。
func (s *Service) AttachCmd(ctx context.Context, ticketID uint) (*exec.Cmd, error) {
	p, err := s.require()
	if err != nil {
		return nil, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	w, err := s.LatestWorker(ctx, ticketID)
	if err != nil {
		return nil, err
	}
	if w == nil {
		return nil, fmt.Errorf("该 ticket 尚无可 attach 的 worker session")
	}
	logPath := strings.TrimSpace(w.LogPath)
	if logPath == "" {
		return nil, fmt.Errorf("该 ticket 尚无可 attach 的 worker 日志")
	}
	return p.WorkerRuntime.AttachCmd(workerRuntimeHandle(*w)), nil
}
