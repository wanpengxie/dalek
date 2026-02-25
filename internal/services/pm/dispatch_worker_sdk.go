package pm

import (
	"context"
	"fmt"
	"strings"
	"time"

	"dalek/internal/agent/provider"
	"dalek/internal/agent/run"
	"dalek/internal/infra"
	"dalek/internal/repo"
	"dalek/internal/store"
)

// launchWorkerSDKHandle 创建 SDK executor 并启动 agent，返回 handle 供调用方 Wait()。
// 无超时限制（agent 可持续运行数小时）。
func (s *Service) launchWorkerSDKHandle(
	ctx context.Context,
	t store.Ticket,
	w store.Worker,
	entryPrompt string,
) (run.AgentRunHandle, error) {
	p, _, err := s.require()
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(entryPrompt) == "" {
		return nil, fmt.Errorf("entry_prompt 为空")
	}
	if w.ID == 0 || t.ID == 0 {
		return nil, fmt.Errorf("worker/ticket 不能为空")
	}
	cfg := p.Config.WithDefaults()
	agentCfg := provider.AgentConfig{
		Provider:        strings.TrimSpace(cfg.WorkerAgent.Provider),
		Model:           strings.TrimSpace(cfg.WorkerAgent.Model),
		ReasoningEffort: strings.TrimSpace(cfg.WorkerAgent.ReasoningEffort),
		ExtraFlags:      append([]string(nil), cfg.WorkerAgent.ExtraFlags...),
		Command:         strings.TrimSpace(cfg.WorkerAgent.Command),
	}
	if _, err := provider.NewFromConfig(agentCfg); err != nil {
		return nil, fmt.Errorf("worker_agent 配置非法: %w", err)
	}
	if strings.TrimSpace(strings.ToLower(cfg.WorkerAgent.Mode)) != "sdk" {
		return nil, fmt.Errorf("worker_agent.mode 不是 sdk")
	}

	rt, err := s.taskRuntime()
	if err != nil {
		return nil, err
	}
	now := time.Now()
	reason := fmt.Sprintf("worker_loop supersede at %s", now.Format(time.RFC3339))
	if err := rt.CancelActiveWorkerRuns(ctx, w.ID, reason, now); err != nil {
		return nil, err
	}

	socket := strings.TrimSpace(w.TmuxSocket)
	if socket == "" {
		socket = strings.TrimSpace(cfg.TmuxSocket)
	}
	session := strings.TrimSpace(w.TmuxSession)
	target := ""
	if p.Tmux != nil && session != "" {
		tctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		picked, _, _ := infra.PickObservationTarget(p.Tmux, tctx, socket, session)
		cancel()
		target = strings.TrimSpace(picked)
	}
	if target == "" && session != "" {
		target = session + ":0.0"
	}

	env := map[string]string{
		"DALEK_PROJECT_KEY":        strings.TrimSpace(p.Key),
		"DALEK_REPO_ROOT":          strings.TrimSpace(p.RepoRoot),
		"DALEK_DB_PATH":            strings.TrimSpace(p.DBPath),
		"DALEK_WORKTREE_PATH":      strings.TrimSpace(w.WorktreePath),
		"DALEK_BRANCH":             strings.TrimSpace(w.Branch),
		"DALEK_TMUX_SOCKET":        strings.TrimSpace(socket),
		"DALEK_TMUX_SESSION":       strings.TrimSpace(session),
		"DALEK_TICKET_ID":          fmt.Sprintf("%d", t.ID),
		"DALEK_WORKER_ID":          fmt.Sprintf("%d", w.ID),
		"DALEK_TICKET_TITLE":       strings.TrimSpace(t.Title),
		"DALEK_TICKET_DESCRIPTION": strings.TrimSpace(t.Description),
		"DALEK_DISPATCH_DEPTH":     nextDispatchDepthEnvValue(),
	}

	executor := run.NewSDKExecutor(run.SDKConfig{
		Provider:        strings.TrimSpace(agentCfg.Provider),
		Model:           strings.TrimSpace(agentCfg.Model),
		ReasoningEffort: strings.TrimSpace(agentCfg.ReasoningEffort),
		Command:         strings.TrimSpace(agentCfg.Command),
		Runtime:         rt,
		OwnerType:       store.TaskOwnerWorker,
		TaskType:        "deliver_ticket",
		ProjectKey:      strings.TrimSpace(p.Key),
		TicketID:        t.ID,
		WorkerID:        w.ID,
		SubjectType:     "ticket",
		SubjectID:       fmt.Sprintf("%d", t.ID),
		WorkDir:         strings.TrimSpace(w.WorktreePath),
		Env:             env,
		Tmux:            p.Tmux,
		TmuxSocket:      strings.TrimSpace(socket),
		TmuxSession:     strings.TrimSpace(session),
		TmuxTarget:      strings.TrimSpace(target),
		TmuxLogPath:     repo.WorkerSDKStreamLogPath(p.WorkersDir, w.ID),
		AppendEvent: func(evtCtx context.Context, eventType, note string, payload any, createdAt time.Time) {
			_ = s.worker.AppendWorkerTaskEvent(evtCtx, w.ID, eventType, note, payload, createdAt)
		},
		RequestSemanticWatch: func(evtCtx context.Context, requestedAt time.Time) {
			_ = s.worker.RequestWorkerSemanticWatch(evtCtx, w.ID, requestedAt)
		},
	})

	handle, err := executor.Execute(ctx, strings.TrimSpace(entryPrompt))
	if err != nil {
		return nil, err
	}
	return handle, nil
}
