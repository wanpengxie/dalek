package pm

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"dalek/internal/agent/provider"
	"dalek/internal/agent/run"
	"dalek/internal/repo"
	"dalek/internal/store"
)

type dispatchPromptBuildResult struct {
	TemplatePath string
	EntryPrompt  string
}

const (
	dispatchPromptTemplateID = "builtin://pm_dispatch_prompt_v1"
)

func (s *Service) executePMDispatchAgent(ctx context.Context, requestID string, t store.Ticket, w store.Worker, entryPromptOverride string) (dispatchPromptBuildResult, error) {
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
			DBPath:   strings.TrimSpace(p.DBPath),
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

	agentCfg := provider.AgentConfig{
		Provider:        strings.TrimSpace(cfg.PMAgent.Provider),
		Model:           strings.TrimSpace(cfg.PMAgent.Model),
		ReasoningEffort: strings.TrimSpace(cfg.PMAgent.ReasoningEffort),
		ExtraFlags:      append([]string(nil), cfg.PMAgent.ExtraFlags...),
		Command:         strings.TrimSpace(cfg.PMAgent.Command),
	}
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

	env := map[string]string{
		"DALEK_PROJECT_KEY":              strings.TrimSpace(p.Key),
		"DALEK_REPO_ROOT":                strings.TrimSpace(p.RepoRoot),
		"DALEK_DB_PATH":                  strings.TrimSpace(p.DBPath),
		"DALEK_WORKTREE_PATH":            strings.TrimSpace(w.WorktreePath),
		"DALEK_BRANCH":                   strings.TrimSpace(w.Branch),
		"DALEK_TMUX_SOCKET":              strings.TrimSpace(w.TmuxSocket),
		"DALEK_TMUX_SESSION":             strings.TrimSpace(w.TmuxSession),
		"DALEK_TICKET_ID":                fmt.Sprintf("%d", t.ID),
		"DALEK_WORKER_ID":                fmt.Sprintf("%d", w.ID),
		"DALEK_TICKET_TITLE":             strings.TrimSpace(t.Title),
		"DALEK_TICKET_DESCRIPTION":       strings.TrimSpace(t.Description),
		"DALEK_DISPATCH_REQUEST_ID":      strings.TrimSpace(requestID),
		"DALEK_DISPATCH_ENTRY_PROMPT":    entryPrompt,
		"DALEK_DISPATCH_PROMPT_TEMPLATE": dispatchPromptTemplateID,
		"DALEK_DISPATCH_DEPTH":           nextDispatchDepthEnvValue(),
	}
	rt, _ := s.taskRuntime()
	execMode := strings.TrimSpace(strings.ToLower(cfg.PMAgent.Mode))
	if execMode == "" {
		execMode = "sdk"
	}
	var executor run.Executor
	if execMode == "sdk" {
		executor = run.NewSDKExecutor(run.SDKConfig{
			Provider:        strings.TrimSpace(agentCfg.Provider),
			Model:           strings.TrimSpace(agentCfg.Model),
			ReasoningEffort: strings.TrimSpace(agentCfg.ReasoningEffort),
			Command:         strings.TrimSpace(agentCfg.Command),
			Runtime:         rt,
			OwnerType:       store.TaskOwnerPM,
			TaskType:        "pm_dispatch_agent",
			ProjectKey:      strings.TrimSpace(p.Key),
			TicketID:        t.ID,
			WorkerID:        w.ID,
			SubjectType:     "ticket",
			SubjectID:       fmt.Sprintf("%d", t.ID),
			WorkDir:         strings.TrimSpace(p.RepoRoot),
			Env:             env,
			Timeout:         timeout,
			Tmux:            p.Tmux,
			TmuxSocket:      strings.TrimSpace(w.TmuxSocket),
			TmuxSession:     strings.TrimSpace(w.TmuxSession),
			TmuxLogPath:     repo.WorkerSDKStreamLogPath(p.WorkersDir, w.ID),
			AppendEvent: func(evtCtx context.Context, eventType, note string, payload any, createdAt time.Time) {
				_ = s.worker.AppendWorkerTaskEvent(evtCtx, w.ID, eventType, note, payload, createdAt)
			},
			RequestSemanticWatch: func(evtCtx context.Context, requestedAt time.Time) {
				_ = s.worker.RequestWorkerSemanticWatch(evtCtx, w.ID, requestedAt)
			},
		})
	} else {
		executor = run.NewProcessExecutor(run.ProcessConfig{
			Provider:    agentProvider,
			Runtime:     rt,
			OwnerType:   store.TaskOwnerPM,
			TaskType:    "pm_dispatch_agent",
			ProjectKey:  strings.TrimSpace(p.Key),
			TicketID:    t.ID,
			WorkerID:    w.ID,
			SubjectType: "ticket",
			SubjectID:   fmt.Sprintf("%d", t.ID),
			WorkDir:     strings.TrimSpace(p.RepoRoot),
			Env:         env,
			Timeout:     timeout,
		})
	}
	handle, err := executor.Execute(runCtx, prompt)
	if err != nil {
		return dispatchPromptBuildResult{}, err
	}
	result, err := handle.Wait()
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
	prompt := fmt.Sprintf(
		`<pm_dispatch_single_mode>
  <role>
    你是 PM agent。本次只执行一次 dispatch job，不做双模式分叉。
  </role>

  <inputs>
    <entry_prompt>
%s
    </entry_prompt>
    <structured_context_json>
%s
    </structured_context_json>
  </inputs>

  <rules>
    <rule>必须先完整读取 structured_context_json，再基于上下文执行本次 dispatch。</rule>
    <rule>优先使用结构化上下文中的路径、ticket、title、description、worktree 等信息。</rule>
    <rule>initialize 阶段必须把 .dalek/control/skills/dispatch-new-ticket/assets 按规则复制并且重命名到 "$DALEK_WORKTREE_PATH/.dalek"下合适的文件。</rule>
    <rule>initialize 阶段只复制模板，不替换 placeholder；placeholder 替换只在 edit 阶段进行。</rule>
    <rule>PLAN.md 仅承载需求、设计、规划叙事；不要把 phases.current_id/phases.next_action/blockers 等结构化状态键写入 PLAN.md。</rule>
    <rule>PLAN.md 不要求固定章节模板，但必须先探索需求与约束，再给出可执行规划与验证口径。</rule>
    <rule>必须在 AGENTS.md 的 task_context 中写明业务语义：为谁、什么场景、解决什么问题、成功标准、范围边界、业务约束。</rule>
    <rule>必须在 Worker AGENTS.md 中定义并维护 &lt;current_state&gt; 与 &lt;state_update_protocol&gt;，让 Worker 启动时强制读取 state.json 并做三方对账（current_state + state.json + git）；状态不一致时以 git 本地仓库为真相源并自行修正。</rule>
    <rule>Worker AGENTS.md 必须显式声明系统语义契约：report 是状态推进主信号、state.json 是辅助快照、next_action 枚举固定为 continue|done|wait_user。</rule>
    <rule>&lt;current_state&gt; 必须是人类可读的文本摘要，不使用 JSON/YAML 或固定键值结构。</rule>
    <rule>state.json 至少维护 phases.current_id、phases.current_status、phases.next_action、phases.summary、phases.items、blockers、code.head_sha、code.working_tree、updated_at，并与 &lt;current_state&gt; 语义一致。</rule>
    <rule>当环境变量 DALEK_DISPATCH_DEPTH 不为 0 时，禁止调用 dalek ticket dispatch 或 dalek worker run；若任务仍需推进，必须在当前 ticket/worktree 直接执行所需 skills/命令，不得二次派发。</rule>
    <rule>如果 entry_prompt 非空，视为本轮人工补充意图，优先满足。</rule>
    <rule>聚焦“本轮任务拆解与下一步执行动作”，避免输出与当前任务无关的长文档。</rule>
  </rules>

  <output_contract>
    完成后仅输出一行：dispatch_done request_id=<id>
  </output_contract>
</pm_dispatch_single_mode>`,
		entryPrompt,
		string(b),
	)
	return strings.TrimSpace(prompt), nil
}
