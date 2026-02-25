package worker

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"dalek/internal/infra"
	"dalek/internal/repo"
	"dalek/internal/store"

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

func (s *Service) newTicketRunName(t store.Ticket) string {
	base := fmt.Sprintf("t%d", t.ID)
	slug := ticketSlug(t.Title)
	if slug != "" {
		base += "-" + slug
	}
	return base + "-" + ticketRunNonce()
}

func (s *Service) ticketBranchName(t store.Ticket, runName string) (string, error) {
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

// StartTicketResources 启动一个 ticket 的“执行资源”（worktree + tmux session）。
//
// 注意：
// - worker 只负责“资源启动”，不做额外初始化脚本。
// - 这里返回的 worker 通常处于 creating，后续由 PM 的 start 流程标记为 running。
func (s *Service) StartTicketResources(ctx context.Context, ticketID uint) (*store.Worker, error) {
	return s.StartTicketResourcesWithOptions(ctx, ticketID, StartOptions{})
}

func (s *Service) StartTicketResourcesWithOptions(ctx context.Context, ticketID uint, opt StartOptions) (w *store.Worker, err error) {
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

	worktreeCreated := false
	sessionRollbackNeeded := false
	rollbackWorktree := ""
	rollbackSocket := ""
	rollbackSession := ""
	defer func() {
		if err == nil {
			return
		}
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if sessionRollbackNeeded && strings.TrimSpace(rollbackSession) != "" {
			socket := strings.TrimSpace(rollbackSocket)
			if socket == "" {
				socket = strings.TrimSpace(p.Config.TmuxSocket)
			}
			_ = p.Tmux.KillSession(cleanupCtx, socket, rollbackSession)
		}
		if worktreeCreated && strings.TrimSpace(rollbackWorktree) != "" {
			_ = p.Git.RemoveWorktree(p.RepoRoot, rollbackWorktree, true)
		}
	}()

	var t store.Ticket
	if err := db.WithContext(ctx).First(&t, ticketID).Error; err != nil {
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
		runName := s.newTicketRunName(t)
		if strings.TrimSpace(branch) == "" {
			branch, err = s.ticketBranchName(t, runName)
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
	// 若已在 running 且 session 存活，start 直接返回，不重复拉起。
	if w != nil && w.Status == store.WorkerRunning && strings.TrimSpace(w.TmuxSession) != "" {
		socket := strings.TrimSpace(w.TmuxSocket)
		if socket == "" {
			socket = strings.TrimSpace(p.Config.TmuxSocket)
		}
		if socket != "" {
			if sessions, serr := p.Tmux.ListSessions(ctx, socket); serr == nil && sessions[strings.TrimSpace(w.TmuxSession)] {
				return w, nil
			}
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
		fresh := store.Worker{
			TicketID:     t.ID,
			Status:       store.WorkerStopped,
			WorktreePath: worktreePath,
			Branch:       branch,
			TmuxSocket:   strings.TrimSpace(p.Config.TmuxSocket),
			TmuxSession:  "",
			StartedAt:    nil,
			StoppedAt:    nil,
			LastError:    "",
		}
		now := time.Now()
		if err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			if err := tx.Create(&fresh).Error; err != nil {
				return err
			}
			return s.appendWorkerStatusEventTx(ctx, tx, fresh.ID, fresh.TicketID, store.WorkerStatus(""), store.WorkerStopped, "worker.start", "创建 worker 初始状态", map[string]any{
				"ticket_id": fresh.TicketID,
			}, now)
		}); err != nil {
			return nil, err
		}
		w = &fresh
	}

	oldSession := strings.TrimSpace(w.TmuxSession)
	oldSocket := strings.TrimSpace(w.TmuxSocket)

	socket := strings.TrimSpace(p.Config.TmuxSocket)
	if socket == "" {
		socket = oldSocket
	}

	// 多 project 共用一个 tmux socket 时，session 名必须全局唯一。
	session := fmt.Sprintf("ts-%s-t%d-w%d", strings.TrimSpace(p.Key), t.ID, w.ID)
	if strings.TrimSpace(p.Key) == "" {
		session = fmt.Sprintf("ts-t%d-w%d", t.ID, w.ID)
	}

	prevStatus := w.Status
	statusNow := time.Now()
	if err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&store.Worker{}).Where("id = ?", w.ID).Updates(map[string]any{
			"status":        store.WorkerCreating,
			"worktree_path": worktreePath,
			"branch":        branch,
			"tmux_socket":   socket,
			"tmux_session":  session,
			"started_at":    nil,
			"stopped_at":    nil,
			"last_error":    "",
		}).Error; err != nil {
			return err
		}
		return s.appendWorkerStatusEventTx(ctx, tx, w.ID, w.TicketID, prevStatus, store.WorkerCreating, "worker.start", "start 进入 creating", map[string]any{
			"ticket_id":    w.TicketID,
			"tmux_socket":  strings.TrimSpace(socket),
			"tmux_session": strings.TrimSpace(session),
		}, statusNow)
	}); err != nil {
		return nil, err
	}
	w.Status = store.WorkerCreating

	if oldSession != "" {
		if oldSocket == "" {
			oldSocket = socket
		}
		_ = p.Tmux.KillSession(ctx, oldSocket, oldSession)
	}
	// 防御：无论 worker 记录是否 fresh，都对目标 session 名做一次 best-effort 清理。
	// 这样可以回收“DB fresh 但 tmux socket 残留同名 session”的脏状态，避免 duplicate session。
	if oldSession == "" || oldSocket != socket || oldSession != session {
		shouldKill := true
		if sessions, serr := p.Tmux.ListSessions(ctx, socket); serr == nil {
			shouldKill = sessions[strings.TrimSpace(session)]
		}
		if shouldKill {
			_ = p.Tmux.KillSession(ctx, socket, session)
		}
	}

	// 在 tmux session 启动时注入 DALEK_* 环境变量（供 worker 内 CLI 使用）。
	contractDir := filepath.Join(worktreePath, ".dalek")

	// worker session 内必须能调用到 dalek（report 等依赖）。
	// 在开发模式下 binary 可能不在 PATH，因此注入绝对路径并把其所在目录 prepend 到 PATH。
	tsBinPath := ""
	if exe, err := os.Executable(); err == nil {
		tsBinPath = strings.TrimSpace(exe)
	}
	if strings.TrimSpace(tsBinPath) == "" {
		if b, err := os.ReadFile(filepath.Join(p.Layout.ProjectDir, ".dalek_bin_path")); err == nil {
			tsBinPath = strings.TrimSpace(string(b))
		}
	}
	tsBinDir := ""
	if strings.TrimSpace(tsBinPath) != "" {
		tsBinDir = filepath.Dir(tsBinPath)
	}

	parts := []string{
		"export DALEK_WORKER_ID=" + infra.ShellQuote(fmt.Sprintf("%d", w.ID)),
		"export DALEK_TICKET_ID=" + infra.ShellQuote(fmt.Sprintf("%d", t.ID)),
		"export DALEK_PROJECT_KEY=" + infra.ShellQuote(strings.TrimSpace(p.Key)),
		"export DALEK_WORKTREE_PATH=" + infra.ShellQuote(strings.TrimSpace(worktreePath)),
		"export DALEK_DB_PATH=" + infra.ShellQuote(strings.TrimSpace(p.DBPath)),
		"export DALEK_CONTRACT_DIR=" + infra.ShellQuote(strings.TrimSpace(contractDir)),
	}
	if strings.TrimSpace(tsBinPath) != "" {
		parts = append(parts, "export DALEK_BIN_PATH="+infra.ShellQuote(strings.TrimSpace(tsBinPath)))
		if strings.TrimSpace(tsBinDir) != "" {
			parts = append(parts, "export PATH="+infra.ShellQuote(strings.TrimSpace(tsBinDir))+":$PATH")
		}
	}
	parts = append(parts, "exec ${SHELL:-bash} -l")
	injected := strings.Join(parts, "; ")
	sessionRollbackNeeded = true
	rollbackSocket = socket
	rollbackSession = session
	if err := p.Tmux.NewSessionWithCommand(ctx, socket, session, worktreePath, []string{"bash", "-lc", injected}); err != nil {
		failedAt := time.Now()
		_ = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			if uerr := tx.Model(&store.Worker{}).Where("id = ?", w.ID).Updates(map[string]any{
				"status":     store.WorkerFailed,
				"last_error": err.Error(),
				"stopped_at": &failedAt,
			}).Error; uerr != nil {
				return uerr
			}
			return s.appendWorkerStatusEventTx(ctx, tx, w.ID, w.TicketID, w.Status, store.WorkerFailed, "worker.start", "tmux session 启动失败", map[string]any{
				"ticket_id":    w.TicketID,
				"tmux_socket":  strings.TrimSpace(socket),
				"tmux_session": strings.TrimSpace(session),
				"error":        strings.TrimSpace(err.Error()),
			}, failedAt)
		})
		return nil, err
	}

	w.WorktreePath = worktreePath
	w.Branch = branch
	w.TmuxSocket = socket
	w.TmuxSession = session

	logPath := repo.WorkerStreamLogPath(p.WorkersDir, w.ID)
	_ = os.MkdirAll(filepath.Dir(logPath), 0o755)
	_ = p.Tmux.PipePaneToFile(ctx, socket, session+":0.0", logPath)

	var out store.Worker
	if err := db.First(&out, w.ID).Error; err != nil {
		return w, nil
	}
	return &out, nil
}

func (s *Service) StopTicket(ctx context.Context, ticketID uint) error {
	p, err := s.require()
	if err != nil {
		return err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	w, err := s.LatestWorker(ctx, ticketID)
	if err != nil {
		return err
	}
	if w == nil || strings.TrimSpace(w.TmuxSession) == "" {
		// 防御：若用户手动清空了 .dalek/（DB）但没清理 tmux socket，
		// 可能出现“DB 无 worker，但 tmux 里仍有旧 session”的孤儿状态。
		// 这里做一次 best-effort 的按命名约定清理，避免 stop -ticket 永久不可用。
		cfg, cerr := s.cfg()
		if cerr == nil {
			socket := strings.TrimSpace(cfg.TmuxSocket)
			key := strings.TrimSpace(p.Key)
			if socket != "" && key != "" && ticketID != 0 {
				prefix := fmt.Sprintf("ts-%s-t%d-w", key, ticketID)
				if sessions, serr := p.Tmux.ListSessions(ctx, socket); serr == nil {
					killed := 0
					for name := range sessions {
						if strings.HasPrefix(name, prefix) {
							_ = p.Tmux.KillSession(ctx, socket, name)
							killed++
						}
					}
					if killed > 0 {
						return nil
					}
				}
			}
		}
		return fmt.Errorf("该 ticket 尚无可停止的 worker")
	}
	return s.StopWorker(ctx, w.ID)
}

// AttachCmd 返回该 ticket 最新 worker 的 tmux attach 命令（供 TUI/CLI 调起）。
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
	if w == nil || strings.TrimSpace(w.TmuxSession) == "" {
		return nil, fmt.Errorf("该 ticket 尚无可 attach 的 worker session")
	}
	return p.Tmux.AttachCmd(w.TmuxSocket, w.TmuxSession), nil
}
