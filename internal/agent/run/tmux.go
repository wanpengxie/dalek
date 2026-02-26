package run

import (
	"context"
	"dalek/internal/contracts"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"dalek/internal/agent/provider"
	"dalek/internal/infra"
	"dalek/internal/services/core"
)

type TmuxAppendEventFunc func(ctx context.Context, eventType, note string, payload any, createdAt time.Time)
type TmuxSemanticWatchFunc func(ctx context.Context, requestedAt time.Time)

type TmuxConfig struct {
	Provider provider.Provider
	Runtime  core.TaskRuntime

	OwnerType contracts.TaskOwnerType
	TaskType  string

	ProjectKey  string
	TicketID    uint
	WorkerID    uint
	SubjectType string
	SubjectID   string

	WorkDir string
	Env     map[string]string

	Tmux        infra.TmuxClient
	TmuxSocket  string
	TmuxSession string
	TmuxTarget  string

	ScriptPath string
	LogPath    string

	KeepaliveTTL time.Duration
	BinPath      string

	AppendEvent          TmuxAppendEventFunc
	RequestSemanticWatch TmuxSemanticWatchFunc
}

type TmuxExecutor struct {
	cfg TmuxConfig
}

func NewTmuxExecutor(cfg TmuxConfig) *TmuxExecutor {
	return &TmuxExecutor{cfg: cfg}
}

func (e *TmuxExecutor) Execute(ctx context.Context, prompt string) (AgentRunHandle, error) {
	if e == nil {
		return nil, fmt.Errorf("tmux executor 为空")
	}
	if e.cfg.Provider == nil {
		return nil, fmt.Errorf("tmux executor 缺少 provider")
	}
	if e.cfg.Tmux == nil {
		return nil, fmt.Errorf("tmux executor 缺少 tmux client")
	}
	if strings.TrimSpace(e.cfg.ScriptPath) == "" {
		return nil, fmt.Errorf("tmux executor 缺少 script_path")
	}
	if strings.TrimSpace(e.cfg.WorkDir) == "" {
		return nil, fmt.Errorf("tmux executor 缺少 work_dir")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	socket := strings.TrimSpace(e.cfg.TmuxSocket)
	if socket == "" {
		socket = "dalek"
	}
	target := strings.TrimSpace(e.cfg.TmuxTarget)
	pane := infra.PaneInfo{}
	if target == "" {
		session := strings.TrimSpace(e.cfg.TmuxSession)
		if session == "" {
			return nil, fmt.Errorf("tmux target/session 不能为空")
		}
		tctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		var err error
		target, pane, err = infra.PickObservationTarget(e.cfg.Tmux, tctx, socket, session)
		if err != nil && strings.TrimSpace(target) == "" {
			return nil, err
		}
		if strings.TrimSpace(target) == "" {
			target = session + ":0.0"
		}
	}
	if pane.InputOff {
		return nil, fmt.Errorf("目标 pane input_off=1（pane=%s），无法注入输入", strings.TrimSpace(target))
	}
	if pane.InMode {
		return nil, fmt.Errorf("目标 pane 处于 mode=%s（pane=%s），请退出后再派发", strings.TrimSpace(pane.Mode), strings.TrimSpace(target))
	}

	bin, args := e.cfg.Provider.BuildCommand(prompt)
	bin = strings.TrimSpace(bin)
	if bin == "" {
		return nil, fmt.Errorf("provider 返回空命令")
	}
	commandLine := shellJoin(bin, args)

	runID := uint(0)
	if e.cfg.Runtime != nil {
		req := newRequestID("arun")
		created, err := e.cfg.Runtime.CreateRun(ctx, core.TaskRuntimeCreateRunInput{
			OwnerType:          e.cfg.OwnerType,
			TaskType:           strings.TrimSpace(e.cfg.TaskType),
			ProjectKey:         strings.TrimSpace(e.cfg.ProjectKey),
			TicketID:           e.cfg.TicketID,
			WorkerID:           e.cfg.WorkerID,
			SubjectType:        strings.TrimSpace(e.cfg.SubjectType),
			SubjectID:          strings.TrimSpace(e.cfg.SubjectID),
			RequestID:          req,
			OrchestrationState: contracts.TaskPending,
			RequestPayloadJSON: marshalJSON(map[string]any{
				"provider":       e.cfg.Provider.Name(),
				"tmux_target":    strings.TrimSpace(target),
				"prompt_preview": truncateRunes(prompt, 256),
			}),
		})
		if err != nil {
			return nil, err
		}
		runID = created.ID
		now := time.Now()
		lease := now.Add(defaultKeepaliveTTL(e.cfg.KeepaliveTTL))
		if err := e.cfg.Runtime.MarkRunRunning(ctx, runID, strings.TrimSpace(target), &lease, now, true); err != nil {
			_ = markProcessRunFailed(e.cfg.Runtime, runID, "agent_mark_running_failed", err.Error())
			return nil, err
		}
		_ = e.cfg.Runtime.AppendEvent(ctx, core.TaskRuntimeEventInput{
			TaskRunID: runID,
			EventType: "task_started",
			ToState: map[string]any{
				"orchestration_state": contracts.TaskRunning,
				"runner_id":           strings.TrimSpace(target),
			},
			Note:      "tmux executor injected",
			CreatedAt: now,
		})
	}

	if strings.TrimSpace(e.cfg.LogPath) != "" {
		_ = os.MkdirAll(filepath.Dir(strings.TrimSpace(e.cfg.LogPath)), 0o755)
		_ = e.cfg.Tmux.PipePaneToFile(ctx, socket, target, strings.TrimSpace(e.cfg.LogPath))
	}

	scriptPath := strings.TrimSpace(e.cfg.ScriptPath)
	if err := os.MkdirAll(filepath.Dir(scriptPath), 0o755); err != nil {
		_ = markProcessRunFailed(e.cfg.Runtime, runID, "agent_prepare_script_failed", err.Error())
		return nil, err
	}
	script := buildTmuxInjectScript(tmuxScriptInput{
		WorkDir:     strings.TrimSpace(e.cfg.WorkDir),
		Env:         e.cfg.Env,
		RunID:       runID,
		WorkerID:    e.cfg.WorkerID,
		BinPath:     strings.TrimSpace(e.cfg.BinPath),
		CommandLine: commandLine,
	})
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		_ = markProcessRunFailed(e.cfg.Runtime, runID, "agent_write_script_failed", err.Error())
		return nil, err
	}

	injected := "bash " + shellQuote(scriptPath)
	if err := e.cfg.Tmux.SendLine(ctx, socket, target, injected); err != nil {
		_ = markProcessRunFailed(e.cfg.Runtime, runID, "agent_sendline_failed", err.Error())
		now := time.Now()
		if e.cfg.AppendEvent != nil {
			e.cfg.AppendEvent(ctx, "dispatch_error", fmt.Sprintf("send-keys 失败: %v", err), map[string]any{
				"target": strings.TrimSpace(target),
			}, now)
		}
		return nil, err
	}

	now := time.Now()
	if e.cfg.AppendEvent != nil {
		e.cfg.AppendEvent(ctx, "dispatch_injected", fmt.Sprintf("target=%s script=%s", strings.TrimSpace(target), scriptPath), map[string]any{
			"target":      strings.TrimSpace(target),
			"script_path": strings.TrimSpace(scriptPath),
		}, now)
	}
	if e.cfg.RequestSemanticWatch != nil {
		e.cfg.RequestSemanticWatch(ctx, time.Now())
	}

	confirmAt := time.Now()
	time.Sleep(150 * time.Millisecond)
	confirmCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	screen, cerr := e.cfg.Tmux.CapturePane(confirmCtx, socket, target, 30)
	if e.cfg.AppendEvent != nil {
		if cerr != nil {
			e.cfg.AppendEvent(ctx, "dispatch_confirm", fmt.Sprintf("capture-pane 失败: %v", cerr), map[string]any{
				"target": strings.TrimSpace(target),
			}, confirmAt)
		} else {
			last := trimOneLine(infra.LastNonEmptyLine(screen))
			if last == "" {
				last = "-"
			}
			e.cfg.AppendEvent(ctx, "dispatch_confirm", fmt.Sprintf("tail_last=%s", last), map[string]any{
				"target": strings.TrimSpace(target),
				"last":   last,
			}, confirmAt)
		}
	}

	return &TmuxHandle{
		runID:       runID,
		socket:      socket,
		target:      strings.TrimSpace(target),
		injectedCmd: injected,
		tmux:        e.cfg.Tmux,
		runtime:     e.cfg.Runtime,
	}, nil
}

type TmuxHandle struct {
	runID       uint
	socket      string
	target      string
	injectedCmd string

	tmux    infra.TmuxClient
	runtime core.TaskRuntime
}

func (h *TmuxHandle) RunID() uint {
	if h == nil {
		return 0
	}
	return h.runID
}

func (h *TmuxHandle) InjectedCmd() string {
	if h == nil {
		return ""
	}
	return strings.TrimSpace(h.injectedCmd)
}

func (h *TmuxHandle) TargetPane() string {
	if h == nil {
		return ""
	}
	return strings.TrimSpace(h.target)
}

func (h *TmuxHandle) Wait() (AgentRunResult, error) {
	if h == nil {
		return AgentRunResult{}, fmt.Errorf("tmux handle 为空")
	}
	if h.runtime == nil || h.runID == 0 {
		return AgentRunResult{}, nil
	}
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		run, err := h.runtime.FindRunByID(context.Background(), h.runID)
		if err != nil {
			return AgentRunResult{}, err
		}
		if run != nil {
			switch run.OrchestrationState {
			case contracts.TaskSucceeded:
				return AgentRunResult{ExitCode: 0}, nil
			case contracts.TaskFailed:
				return AgentRunResult{ExitCode: 1}, fmt.Errorf("%s", strings.TrimSpace(run.ErrorMessage))
			case contracts.TaskCanceled:
				return AgentRunResult{ExitCode: 1}, fmt.Errorf("%s", strings.TrimSpace(run.ErrorMessage))
			}
		}
		select {
		case <-ticker.C:
		}
	}
}

func (h *TmuxHandle) Cancel() error {
	if h == nil {
		return fmt.Errorf("tmux handle 为空")
	}
	if h.tmux == nil {
		return fmt.Errorf("tmux handle 缺少 tmux client")
	}
	if strings.TrimSpace(h.target) == "" {
		return fmt.Errorf("tmux handle 缺少 target")
	}
	return h.tmux.SendKeys(context.Background(), strings.TrimSpace(h.socket), strings.TrimSpace(h.target), "C-c")
}

func defaultKeepaliveTTL(ttl time.Duration) time.Duration {
	if ttl <= 0 {
		return 30 * time.Minute
	}
	return ttl
}
