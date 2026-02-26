package run

import (
	"context"
	"dalek/internal/contracts"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"dalek/internal/agent/provider"
	"dalek/internal/agent/sdkrunner"
	"dalek/internal/infra"
	"dalek/internal/services/core"
)

type SDKConfig struct {
	Provider        string
	Model           string
	ReasoningEffort string
	Command         string
	Runner          sdkrunner.TaskRunner
	Runtime         core.TaskRuntime

	OwnerType contracts.TaskOwnerType
	TaskType  string

	ProjectKey  string
	TicketID    uint
	WorkerID    uint
	SubjectType string
	SubjectID   string

	WorkDir   string
	Env       map[string]string
	SessionID string
	Timeout   time.Duration

	Tmux        infra.TmuxClient
	TmuxSocket  string
	TmuxSession string
	TmuxTarget  string
	TmuxLogPath string

	AppendEvent          TmuxAppendEventFunc
	RequestSemanticWatch TmuxSemanticWatchFunc
}

type SDKExecutor struct {
	cfg SDKConfig
}

func NewSDKExecutor(cfg SDKConfig) *SDKExecutor {
	return &SDKExecutor{cfg: cfg}
}

func (e *SDKExecutor) Execute(ctx context.Context, prompt string) (AgentRunHandle, error) {
	if e == nil {
		return nil, fmt.Errorf("sdk executor 为空")
	}
	if strings.TrimSpace(e.cfg.Provider) == "" {
		return nil, fmt.Errorf("sdk executor 缺少 provider")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	execCtx := ctx
	cancel := func() {}
	if e.cfg.Timeout > 0 {
		execCtx, cancel = context.WithTimeout(ctx, e.cfg.Timeout)
	}

	runID := uint(0)
	if e.cfg.Runtime != nil {
		req := newRequestID("arun")
		payload := marshalJSON(map[string]any{
			"provider":         strings.TrimSpace(strings.ToLower(e.cfg.Provider)),
			"mode":             "sdk",
			"model":            strings.TrimSpace(e.cfg.Model),
			"session_id":       strings.TrimSpace(e.cfg.SessionID),
			"tmux_socket":      strings.TrimSpace(e.cfg.TmuxSocket),
			"tmux_session":     strings.TrimSpace(e.cfg.TmuxSession),
			"tmux_target":      strings.TrimSpace(e.cfg.TmuxTarget),
			"tmux_log_path":    strings.TrimSpace(e.cfg.TmuxLogPath),
			"prompt_preview":   truncateRunes(prompt, 256),
			"reasoning_effort": strings.TrimSpace(strings.ToLower(e.cfg.ReasoningEffort)),
		})
		created, err := e.cfg.Runtime.CreateRun(execCtx, core.TaskRuntimeCreateRunInput{
			OwnerType:          e.cfg.OwnerType,
			TaskType:           strings.TrimSpace(e.cfg.TaskType),
			ProjectKey:         strings.TrimSpace(e.cfg.ProjectKey),
			TicketID:           e.cfg.TicketID,
			WorkerID:           e.cfg.WorkerID,
			SubjectType:        strings.TrimSpace(e.cfg.SubjectType),
			SubjectID:          strings.TrimSpace(e.cfg.SubjectID),
			RequestID:          req,
			OrchestrationState: contracts.TaskPending,
			RequestPayloadJSON: payload,
		})
		if err != nil {
			cancel()
			return nil, err
		}
		runID = created.ID
		now := time.Now()
		var lease *time.Time
		if e.cfg.Timeout > 0 {
			l := now.Add(e.cfg.Timeout)
			lease = &l
		}
		runnerID := "sdk:" + strings.TrimSpace(strings.ToLower(e.cfg.Provider))
		if err := e.cfg.Runtime.MarkRunRunning(execCtx, runID, runnerID, lease, now, true); err != nil {
			_ = markProcessRunFailed(e.cfg.Runtime, runID, "agent_mark_running_failed", err.Error())
			cancel()
			return nil, err
		}
		_ = e.cfg.Runtime.AppendEvent(execCtx, core.TaskRuntimeEventInput{
			TaskRunID: runID,
			EventType: "task_started",
			ToState: map[string]any{
				"orchestration_state": contracts.TaskRunning,
				"runner_id":           runnerID,
			},
			Note:      "sdk executor started",
			CreatedAt: now,
		})
	}

	h := &sdkHandle{
		runID:   runID,
		runtime: e.cfg.Runtime,
		cfg:     e.cfg,
		prompt:  strings.TrimSpace(prompt),
		execCtx: execCtx,
		cancel:  cancel,
		done:    make(chan struct{}),
	}
	h.start()
	return h, nil
}

type sdkHandle struct {
	runID   uint
	runtime core.TaskRuntime
	cfg     SDKConfig
	prompt  string

	execCtx context.Context
	cancel  context.CancelFunc

	once sync.Once
	done chan struct{}

	waitRes AgentRunResult
	waitErr error
}

func (h *sdkHandle) RunID() uint {
	if h == nil {
		return 0
	}
	return h.runID
}

func (h *sdkHandle) Wait() (AgentRunResult, error) {
	if h == nil {
		return AgentRunResult{}, fmt.Errorf("sdk handle 为空")
	}
	h.start()
	<-h.done
	return h.waitRes, h.waitErr
}

func (h *sdkHandle) Cancel() error {
	if h == nil {
		return fmt.Errorf("sdk handle 为空")
	}
	if h.cancel != nil {
		h.cancel()
	}
	return nil
}

func (h *sdkHandle) start() {
	h.once.Do(func() {
		go h.run()
	})
}

func (h *sdkHandle) run() {
	defer close(h.done)
	if h.cancel != nil {
		defer h.cancel()
	}
	playback, playbackErr := startSDKTmuxPlayback(h.execCtx, h.cfg, h.runID)
	if playback != nil {
		defer playback.Close(context.Background())
	}
	if playbackErr != nil {
		msg := "sdk tmux playback 不可用: " + trimOneLine(playbackErr.Error())
		h.appendTaskStreamEvent(msg, map[string]any{
			"type":  "sdk_playback_error",
			"error": trimOneLine(playbackErr.Error()),
		})
		if h.cfg.AppendEvent != nil {
			h.cfg.AppendEvent(context.Background(), "sdk_stream_error", msg, map[string]any{
				"error": trimOneLine(playbackErr.Error()),
			}, time.Now())
		}
	}
	if playback != nil && h.cfg.AppendEvent != nil {
		h.cfg.AppendEvent(context.Background(), "sdk_stream_started", fmt.Sprintf("target=%s log=%s", playback.targetPane(), playback.logFilePath()), map[string]any{
			"target":   playback.targetPane(),
			"log_path": playback.logFilePath(),
		}, time.Now())
	}
	if playback != nil && h.cfg.RequestSemanticWatch != nil {
		h.cfg.RequestSemanticWatch(context.Background(), time.Now())
	}

	playbackWriteFailed := false
	onEvent := func(ev sdkrunner.Event) {
		if playback != nil {
			if err := playback.AppendEvent(ev); err != nil && !playbackWriteFailed {
				playbackWriteFailed = true
				msg := "sdk tmux playback 写入失败: " + trimOneLine(err.Error())
				h.appendTaskStreamEvent(msg, map[string]any{
					"type":  "sdk_playback_error",
					"error": trimOneLine(err.Error()),
				})
				if h.cfg.AppendEvent != nil {
					h.cfg.AppendEvent(context.Background(), "sdk_stream_error", msg, map[string]any{
						"error": trimOneLine(err.Error()),
					}, time.Now())
				}
			}
		}
		note := trimOneLine(ev.Text)
		if note == "" {
			note = trimOneLine(ev.Type)
		}
		payload := map[string]any{
			"type":       strings.TrimSpace(ev.Type),
			"text":       strings.TrimSpace(ev.Text),
			"raw_json":   strings.TrimSpace(ev.RawJSON),
			"session_id": strings.TrimSpace(ev.SessionID),
		}
		h.appendTaskStreamEvent(note, payload)
	}

	runner := h.cfg.Runner
	if runner == nil {
		runner = sdkrunner.DefaultTaskRunner{}
	}
	r, err := runner.Run(h.execCtx, sdkrunner.Request{
		Provider:        strings.TrimSpace(strings.ToLower(h.cfg.Provider)),
		Model:           strings.TrimSpace(h.cfg.Model),
		ReasoningEffort: strings.TrimSpace(strings.ToLower(h.cfg.ReasoningEffort)),
		Command:         strings.TrimSpace(h.cfg.Command),
		Prompt:          strings.TrimSpace(h.prompt),
		SessionID:       strings.TrimSpace(h.cfg.SessionID),
		WorkDir:         strings.TrimSpace(h.cfg.WorkDir),
		Env:             h.cfg.Env,
	}, onEvent)

	parsedEvents := make([]any, 0, len(r.Events))
	for _, ev := range r.Events {
		parsedEvents = append(parsedEvents, map[string]any{
			"type":       strings.TrimSpace(ev.Type),
			"text":       strings.TrimSpace(ev.Text),
			"raw_json":   strings.TrimSpace(ev.RawJSON),
			"session_id": strings.TrimSpace(ev.SessionID),
		})
	}
	res := AgentRunResult{
		ExitCode: 0,
		Stdout:   strings.TrimSpace(r.Stdout),
		Stderr:   strings.TrimSpace(r.Stderr),
		Parsed: provider.ParsedOutput{
			Text:   strings.TrimSpace(r.Text),
			Events: parsedEvents,
		},
	}
	finalErr := err
	if finalErr == nil {
		if execErr := h.execCtx.Err(); errors.Is(execErr, context.Canceled) || errors.Is(execErr, context.DeadlineExceeded) {
			finalErr = execErr
		}
	}
	if finalErr != nil {
		res.ExitCode = 1
	}
	h.waitRes = res
	if playback != nil && h.cfg.AppendEvent != nil {
		eventType := "sdk_stream_finished"
		note := "sdk stream finished"
		if finalErr != nil {
			eventType = "sdk_stream_failed"
			note = trimOneLine(errStringWithOutput(finalErr, res.Stdout, res.Stderr))
		}
		h.cfg.AppendEvent(context.Background(), eventType, note, map[string]any{
			"target":   playback.targetPane(),
			"log_path": playback.logFilePath(),
		}, time.Now())
	}

	now := time.Now()
	if h.runtime != nil && h.runID != 0 {
		if finalErr == nil {
			_ = h.runtime.MarkRunSucceeded(context.Background(), h.runID, marshalJSON(res), now)
			_ = h.runtime.AppendEvent(context.Background(), core.TaskRuntimeEventInput{
				TaskRunID: h.runID,
				EventType: "task_succeeded",
				FromState: map[string]any{"orchestration_state": contracts.TaskRunning},
				ToState:   map[string]any{"orchestration_state": contracts.TaskSucceeded},
				Note:      "sdk executor finished",
				CreatedAt: now,
			})
		} else {
			msg := strings.TrimSpace(errStringWithOutput(finalErr, res.Stdout, res.Stderr))
			if errors.Is(finalErr, context.Canceled) || errors.Is(finalErr, context.DeadlineExceeded) ||
				errors.Is(h.execCtx.Err(), context.Canceled) || errors.Is(h.execCtx.Err(), context.DeadlineExceeded) {
				_ = h.runtime.MarkRunCanceled(context.Background(), h.runID, "agent_canceled", msg, now)
				_ = h.runtime.AppendEvent(context.Background(), core.TaskRuntimeEventInput{
					TaskRunID: h.runID,
					EventType: "task_canceled",
					FromState: map[string]any{"orchestration_state": contracts.TaskRunning},
					ToState:   map[string]any{"orchestration_state": contracts.TaskCanceled},
					Note:      msg,
					CreatedAt: now,
				})
			} else {
				_ = h.runtime.MarkRunFailed(context.Background(), h.runID, "agent_exit_failed", msg, now)
				_ = h.runtime.AppendEvent(context.Background(), core.TaskRuntimeEventInput{
					TaskRunID: h.runID,
					EventType: "task_failed",
					FromState: map[string]any{"orchestration_state": contracts.TaskRunning},
					ToState:   map[string]any{"orchestration_state": contracts.TaskFailed},
					Note:      msg,
					CreatedAt: now,
				})
			}
		}
	}

	if finalErr != nil {
		h.waitErr = fmt.Errorf("agent 执行失败: %s", errStringWithOutput(finalErr, res.Stdout, res.Stderr))
	}
}

func (h *sdkHandle) appendTaskStreamEvent(note string, payload map[string]any) {
	if h == nil || h.runtime == nil || h.runID == 0 {
		return
	}
	summary := truncateRunes(trimOneLine(note), 180)
	if summary == "" {
		summary = "-"
	}
	_ = h.runtime.AppendEvent(context.Background(), core.TaskRuntimeEventInput{
		TaskRunID: h.runID,
		EventType: "task_stream",
		Note:      summary,
		Payload:   payload,
		CreatedAt: time.Now(),
	})
}
