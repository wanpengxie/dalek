package pm

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"dalek/internal/infra"
	"dalek/internal/store"
)

func (s *Service) executePMBootstrapEntrypoint(ctx context.Context, t store.Ticket, w store.Worker) error {
	p, _, err := s.require()
	if err != nil {
		return err
	}
	if ctx == nil {
		ctx = context.Background()
	}

	scriptPath := filepath.Join(p.Layout.ControlWorkerDir, "bootstrap.sh")
	if _, err := os.Stat(scriptPath); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("读取 bootstrap 脚本失败: %w", err)
	}

	workDir := strings.TrimSpace(w.WorktreePath)
	if workDir == "" {
		workDir = strings.TrimSpace(p.RepoRoot)
	}
	if workDir == "" {
		workDir = strings.TrimSpace(p.Layout.ProjectDir)
	}

	env := map[string]string{
		"DALEK_PROJECT_KEY":        strings.TrimSpace(p.Key),
		"DALEK_REPO_ROOT":          strings.TrimSpace(p.RepoRoot),
		"DALEK_DB_PATH":            strings.TrimSpace(p.DBPath),
		"DALEK_WORKTREE_PATH":      strings.TrimSpace(w.WorktreePath),
		"DALEK_BRANCH":             strings.TrimSpace(w.Branch),
		"DALEK_TMUX_SOCKET":        strings.TrimSpace(w.TmuxSocket),
		"DALEK_TMUX_SESSION":       strings.TrimSpace(w.TmuxSession),
		"DALEK_TICKET_ID":          fmt.Sprintf("%d", t.ID),
		"DALEK_WORKER_ID":          fmt.Sprintf("%d", w.ID),
		"DALEK_TICKET_TITLE":       strings.TrimSpace(t.Title),
		"DALEK_TICKET_DESCRIPTION": strings.TrimSpace(t.Description),
	}
	// non-agent-exec: bootstrap 是系统启动脚本，不属于 agent 命令执行链路。
	script := infra.BuildBashScriptWithEnv(env, "bash "+infra.ShellQuote(scriptPath))
	if _, err := infra.Run(ctx, workDir, "bash", "-lc", script); err != nil {
		return fmt.Errorf("执行 bootstrap 失败: %w", err)
	}
	return nil
}
