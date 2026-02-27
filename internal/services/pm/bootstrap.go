package pm

import (
	"context"
	"fmt"
	"os"
	"strings"

	"dalek/internal/contracts"
	"dalek/internal/infra"
)

func (s *Service) executePMBootstrapEntrypoint(ctx context.Context, t contracts.Ticket, w contracts.Worker) error {
	p, _, err := s.require()
	if err != nil {
		return err
	}
	if ctx == nil {
		ctx = context.Background()
	}

	scriptPath := p.Layout.ProjectBootstrapPath
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

	env := buildBaseEnv(p, t, w)
	// non-agent-exec: bootstrap 是系统启动脚本，不属于 agent 命令执行链路。
	script := infra.BuildBashScriptWithEnv(env, "bash "+infra.ShellQuote(scriptPath))
	if _, err := infra.Run(ctx, workDir, "bash", "-lc", script); err != nil {
		return fmt.Errorf("执行 bootstrap 失败: %w", err)
	}
	return nil
}
