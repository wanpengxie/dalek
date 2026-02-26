package run

import (
	"bytes"
	"context"
	"dalek/internal/contracts"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"

	"dalek/internal/agent/provider"
	"dalek/internal/services/core"
)

type ProcessConfig struct {
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
	Timeout time.Duration
}

type ProcessExecutor struct {
	cfg ProcessConfig
}

func NewProcessExecutor(cfg ProcessConfig) *ProcessExecutor {
	return &ProcessExecutor{cfg: cfg}
}

func (e *ProcessExecutor) Execute(ctx context.Context, prompt string) (AgentRunHandle, error) {
	if e == nil {
		return nil, fmt.Errorf("process executor 为空")
	}
	if e.cfg.Provider == nil {
		return nil, fmt.Errorf("process executor 缺少 provider")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	execCtx := ctx
	cancel := func() {}
	if e.cfg.Timeout > 0 {
		execCtx, cancel = context.WithTimeout(ctx, e.cfg.Timeout)
	}

	bin, args := e.cfg.Provider.BuildCommand(prompt)
	bin = strings.TrimSpace(bin)
	if bin == "" {
		cancel()
		return nil, fmt.Errorf("provider 返回空命令")
	}

	runID := uint(0)
	if e.cfg.Runtime != nil {
		req := newRequestID("arun")
		payload := marshalJSON(map[string]any{
			"provider":       e.cfg.Provider.Name(),
			"bin":            bin,
			"args":           args,
			"prompt_preview": truncateRunes(prompt, 256),
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
	}

	cmd := exec.CommandContext(execCtx, bin, args...)
	if strings.TrimSpace(e.cfg.WorkDir) != "" {
		cmd.Dir = strings.TrimSpace(e.cfg.WorkDir)
	}
	cmd.Env = mergeEnv(e.cfg.Env)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		_ = markProcessRunFailed(e.cfg.Runtime, runID, "agent_start_failed", err.Error())
		cancel()
		return nil, fmt.Errorf("启动 agent 失败: %w", err)
	}

	if e.cfg.Runtime != nil && runID != 0 {
		now := time.Now()
		var lease *time.Time
		if e.cfg.Timeout > 0 {
			l := now.Add(e.cfg.Timeout)
			lease = &l
		}
		runnerID := fmt.Sprintf("pid:%d", cmd.Process.Pid)
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
			Note:      "process executor started",
			CreatedAt: now,
		})
	}

	return &processHandle{
		runID:     runID,
		runtime:   e.cfg.Runtime,
		provider:  e.cfg.Provider,
		cmd:       cmd,
		cancel:    cancel,
		execCtx:   execCtx,
		stdoutBuf: &stdout,
		stderrBuf: &stderr,
	}, nil
}

type processHandle struct {
	runID    uint
	runtime  core.TaskRuntime
	provider provider.Provider

	cmd       *exec.Cmd
	cancel    context.CancelFunc
	execCtx   context.Context
	stdoutBuf *bytes.Buffer
	stderrBuf *bytes.Buffer

	once   sync.Once
	doneCh chan struct{}

	waitRes AgentRunResult
	waitErr error
}

func (h *processHandle) RunID() uint {
	if h == nil {
		return 0
	}
	return h.runID
}

func (h *processHandle) Wait() (AgentRunResult, error) {
	if h == nil {
		return AgentRunResult{}, fmt.Errorf("process handle 为空")
	}
	h.once.Do(func() {
		h.doneCh = make(chan struct{})
		h.waitRes, h.waitErr = h.waitOnce()
		close(h.doneCh)
	})
	if h.doneCh != nil {
		<-h.doneCh
	}
	return h.waitRes, h.waitErr
}

func (h *processHandle) waitOnce() (AgentRunResult, error) {
	if h.cancel != nil {
		defer h.cancel()
	}
	if h.cmd == nil {
		return AgentRunResult{}, fmt.Errorf("process handle 缺少 cmd")
	}

	err := h.cmd.Wait()
	stdout := strings.TrimSpace(h.stdoutBuf.String())
	stderr := strings.TrimSpace(h.stderrBuf.String())
	exitCode := 0
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			exitCode = ee.ExitCode()
		} else {
			exitCode = -1
		}
	}
	res := AgentRunResult{
		ExitCode: exitCode,
		Stdout:   stdout,
		Stderr:   stderr,
	}
	if h.provider != nil {
		res.Parsed = h.provider.ParseOutput(stdout)
	}

	if h.runtime != nil && h.runID != 0 {
		now := time.Now()
		if err == nil && exitCode == 0 {
			_ = h.runtime.MarkRunSucceeded(h.execCtx, h.runID, marshalJSON(res), now)
			_ = h.runtime.AppendEvent(h.execCtx, core.TaskRuntimeEventInput{
				TaskRunID: h.runID,
				EventType: "task_succeeded",
				FromState: map[string]any{"orchestration_state": contracts.TaskRunning},
				ToState:   map[string]any{"orchestration_state": contracts.TaskSucceeded},
				Note:      "process executor finished",
				CreatedAt: now,
			})
		} else {
			msg := strings.TrimSpace(errStringWithOutput(err, stdout, stderr))
			if errors.Is(h.execCtx.Err(), context.Canceled) || errors.Is(h.execCtx.Err(), context.DeadlineExceeded) {
				_ = h.runtime.MarkRunCanceled(h.execCtx, h.runID, "agent_canceled", msg, now)
				_ = h.runtime.AppendEvent(h.execCtx, core.TaskRuntimeEventInput{
					TaskRunID: h.runID,
					EventType: "task_canceled",
					FromState: map[string]any{"orchestration_state": contracts.TaskRunning},
					ToState:   map[string]any{"orchestration_state": contracts.TaskCanceled},
					Note:      msg,
					CreatedAt: now,
				})
			} else {
				_ = h.runtime.MarkRunFailed(h.execCtx, h.runID, "agent_exit_failed", msg, now)
				_ = h.runtime.AppendEvent(h.execCtx, core.TaskRuntimeEventInput{
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

	if err != nil {
		return res, fmt.Errorf("agent 执行失败: %s", errStringWithOutput(err, stdout, stderr))
	}
	return res, nil
}

func (h *processHandle) Cancel() error {
	if h == nil {
		return fmt.Errorf("process handle 为空")
	}
	if h.cancel != nil {
		h.cancel()
	}
	return nil
}

func markProcessRunFailed(rt core.TaskRuntime, runID uint, code, msg string) error {
	if rt == nil || runID == 0 {
		return nil
	}
	return rt.MarkRunFailed(context.Background(), runID, strings.TrimSpace(code), strings.TrimSpace(msg), time.Now())
}

func errStringWithOutput(err error, stdout, stderr string) string {
	parts := make([]string, 0, 3)
	if err != nil {
		parts = append(parts, strings.TrimSpace(err.Error()))
	}
	if strings.TrimSpace(stderr) != "" {
		parts = append(parts, "stderr="+truncateRunes(stderr, 3000))
	}
	if strings.TrimSpace(stdout) != "" {
		parts = append(parts, "stdout="+truncateRunes(stdout, 3000))
	}
	return strings.Join(parts, " | ")
}
