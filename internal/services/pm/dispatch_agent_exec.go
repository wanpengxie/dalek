package pm

import (
	"context"
	"dalek/internal/contracts"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"dalek/internal/agent/provider"
	"dalek/internal/repo"
	"dalek/internal/services/agentexec"
)

type dispatchPromptBuildResult struct {
	TemplatePath string
	EntryPrompt  string
}

func (s *Service) executePMDispatchAgent(ctx context.Context, requestID string, t contracts.Ticket, w contracts.Worker, entryPromptOverride string) (dispatchPromptBuildResult, error) {
	p, _, err := s.require()
	if err != nil {
		return dispatchPromptBuildResult{}, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	cfg := p.Config.WithDefaults()
	entryPrompt := strings.TrimSpace(entryPromptOverride)
	prompt, err := buildDispatchPrompt(dispatchStructuredContext{
		GeneratedAt: time.Now().Format(time.RFC3339),
		RequestID:   strings.TrimSpace(requestID),
		Project: dispatchProjectContext{
			Key:      strings.TrimSpace(p.Key),
			RepoRoot: strings.TrimSpace(p.RepoRoot),
			DBPath:   strings.TrimSpace(p.DBPath()),
		},
		Ticket: dispatchTicketContext{
			ID:          t.ID,
			Title:       strings.TrimSpace(t.Title),
			Description: strings.TrimSpace(t.Description),
			Status:      strings.TrimSpace(string(t.WorkflowStatus)),
		},
		Worker: dispatchWorkerContext{
			ID:           w.ID,
			Status:       strings.TrimSpace(string(w.Status)),
			WorktreePath: strings.TrimSpace(w.WorktreePath),
			Branch:       strings.TrimSpace(w.Branch),
			TmuxSocket:   strings.TrimSpace(w.TmuxSocket),
			TmuxSession:  strings.TrimSpace(w.TmuxSession),
		},
		EntryPrompt: entryPrompt,
	})
	if err != nil {
		return dispatchPromptBuildResult{}, err
	}

	agentCfg := repo.AgentConfigFromExecConfig(cfg.PMAgent)
	agentProvider, err := provider.NewFromConfig(agentCfg)
	if err != nil {
		return dispatchPromptBuildResult{}, fmt.Errorf("pm_agent 配置非法: %w", err)
	}

	timeout := time.Duration(cfg.PMDispatchTimeoutMS) * time.Millisecond
	runCtx := ctx
	cancel := func() {}
	if timeout > 0 {
		runCtx, cancel = context.WithTimeout(ctx, timeout)
	} else {
		runCtx, cancel = context.WithCancel(ctx)
	}
	defer cancel()

	env := buildBaseEnv(p, t, w)
	env[envDispatchRequestID] = strings.TrimSpace(requestID)
	env[envDispatchEntryPrompt] = entryPrompt
	env[envDispatchPromptTpl] = dispatchPromptTemplateID
	env[dispatchDepthEnvKey] = nextDispatchDepthEnvValue()
	rt, _ := s.taskRuntime()
	execMode := strings.TrimSpace(strings.ToLower(cfg.PMAgent.Mode))
	if execMode == "" {
		execMode = "sdk"
	}
	var executor agentexec.Executor
	if execMode == "sdk" {
		executor = agentexec.NewSDKExecutor(agentexec.SDKConfig{
			Provider:        agentCfg.Provider,
			Model:           agentCfg.Model,
			ReasoningEffort: agentCfg.ReasoningEffort,
			Command:         agentCfg.Command,
			BaseConfig: agentexec.BaseConfig{
				Runtime:     rt,
				OwnerType:   contracts.TaskOwnerPM,
				TaskType:    "pm_dispatch_agent",
				ProjectKey:  strings.TrimSpace(p.Key),
				TicketID:    t.ID,
				WorkerID:    w.ID,
				SubjectType: "ticket",
				SubjectID:   fmt.Sprintf("%d", t.ID),
				WorkDir:     strings.TrimSpace(p.RepoRoot),
				Env:         env,
			},
			Timeout:     timeout,
			Tmux:        p.Tmux,
			TmuxSocket:  strings.TrimSpace(w.TmuxSocket),
			TmuxSession: strings.TrimSpace(w.TmuxSession),
			TmuxLogPath: repo.WorkerSDKStreamLogPath(p.WorkersDir, w.ID),
			AppendEvent: func(evtCtx context.Context, eventType, note string, payload any, createdAt time.Time) {
				_ = s.worker.AppendWorkerTaskEvent(evtCtx, w.ID, eventType, note, payload, createdAt)
			},
			RequestSemanticWatch: func(evtCtx context.Context, requestedAt time.Time) {
				_ = s.worker.RequestWorkerSemanticWatch(evtCtx, w.ID, requestedAt)
			},
		})
	} else {
		executor = agentexec.NewProcessExecutor(agentexec.ProcessConfig{
			Provider: agentProvider,
			BaseConfig: agentexec.BaseConfig{
				Runtime:     rt,
				OwnerType:   contracts.TaskOwnerPM,
				TaskType:    "pm_dispatch_agent",
				ProjectKey:  strings.TrimSpace(p.Key),
				TicketID:    t.ID,
				WorkerID:    w.ID,
				SubjectType: "ticket",
				SubjectID:   fmt.Sprintf("%d", t.ID),
				WorkDir:     strings.TrimSpace(p.RepoRoot),
				Env:         env,
			},
			Timeout: timeout,
		})
	}
	handle, err := executor.Execute(runCtx, prompt)
	if err != nil {
		return dispatchPromptBuildResult{}, err
	}
	result, err := handle.Wait(runCtx)
	if err != nil {
		return dispatchPromptBuildResult{}, err
	}
	if result.ExitCode != 0 {
		return dispatchPromptBuildResult{}, fmt.Errorf("PM agent 退出码非 0: %d", result.ExitCode)
	}
	return dispatchPromptBuildResult{
		TemplatePath: dispatchPromptTemplateID,
		EntryPrompt:  entryPrompt,
	}, nil
}

type dispatchStructuredContext struct {
	GeneratedAt string                 `json:"generated_at"`
	RequestID   string                 `json:"request_id"`
	Project     dispatchProjectContext `json:"project"`
	Ticket      dispatchTicketContext  `json:"ticket"`
	Worker      dispatchWorkerContext  `json:"worker"`
	EntryPrompt string                 `json:"entry_prompt,omitempty"`
}

type dispatchProjectContext struct {
	Key      string `json:"key"`
	RepoRoot string `json:"repo_root"`
	DBPath   string `json:"db_path"`
}

type dispatchTicketContext struct {
	ID          uint   `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Status      string `json:"status"`
}

type dispatchWorkerContext struct {
	ID           uint   `json:"id"`
	Status       string `json:"status"`
	WorktreePath string `json:"worktree_path"`
	Branch       string `json:"branch"`
	TmuxSocket   string `json:"tmux_socket"`
	TmuxSession  string `json:"tmux_session"`
}

func buildDispatchPrompt(ctx dispatchStructuredContext) (string, error) {
	b, err := json.MarshalIndent(ctx, "", "  ")
	if err != nil {
		return "", fmt.Errorf("构造 dispatch 上下文失败: %w", err)
	}
	entryPrompt := strings.TrimSpace(ctx.EntryPrompt)
	if entryPrompt == "" {
		entryPrompt = "-"
	}
	prompt, err := repo.RenderSeedTemplate(dispatchPromptTemplate, dispatchPromptTemplateData{
		EntryPrompt:           entryPrompt,
		StructuredContextJSON: string(b),
	})
	if err != nil {
		return "", fmt.Errorf("渲染 dispatch prompt 模板失败: %w", err)
	}
	return strings.TrimSpace(prompt), nil
}

type dispatchPromptTemplateData struct {
	EntryPrompt           string
	StructuredContextJSON string
}
