package pm

import (
	"context"
	"dalek/internal/contracts"
	"fmt"
	"strings"
	"time"

	"dalek/internal/agent/provider"
	"dalek/internal/repo"
	"dalek/internal/services/agentexec"
)

// launchWorkerSDKHandle 创建 SDK executor 并启动 agent，返回 handle 供调用方 Wait()。
// 底层统一使用 progress timeout：30 分钟无进展超时，任意事件会重置计时。
func (s *Service) launchWorkerSDKHandle(
	ctx context.Context,
	t contracts.Ticket,
	w contracts.Worker,
	entryPrompt string,
) (agentexec.AgentRunHandle, error) {
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
	agentCfg := repo.AgentConfigFromExecConfig(cfg.WorkerAgent)
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

	env := buildBaseEnv(p, t, w)
	env[dispatchDepthEnvKey] = nextDispatchDepthEnvValue()

	executor := agentexec.NewSDKExecutor(agentexec.SDKConfig{
		AgentConfig: agentCfg,
		Runner:      s.taskRunner(),
		BaseConfig: agentexec.BaseConfig{
			Runtime:     rt,
			OwnerType:   contracts.TaskOwnerWorker,
			TaskType:    "deliver_ticket",
			ProjectKey:  strings.TrimSpace(p.Key),
			TicketID:    t.ID,
			WorkerID:    w.ID,
			SubjectType: "ticket",
			SubjectID:   fmt.Sprintf("%d", t.ID),
			WorkDir:     strings.TrimSpace(w.WorktreePath),
			Env:         env,
		},
		StreamLogPath: repo.WorkerSDKStreamLogPath(p.WorkersDir, w.ID),
		AppendEvent: func(evtCtx context.Context, eventType, note string, payload any, createdAt time.Time) {
			_ = s.worker.AppendWorkerTaskEvent(evtCtx, w.ID, eventType, note, payload, createdAt)
		},
		RequestSemanticWatch: func(evtCtx context.Context, requestedAt time.Time) {
			_ = s.worker.RequestWorkerSemanticWatch(evtCtx, w.ID, requestedAt)
		},
	})

	handle, err := executor.Execute(ctx, buildWorkerEntrypointPrompt(entryPrompt))
	if err != nil {
		return nil, err
	}
	return handle, nil
}
