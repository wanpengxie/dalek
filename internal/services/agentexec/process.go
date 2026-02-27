package agentexec

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
	"sync"
	"time"

	"dalek/internal/agent/provider"
	"dalek/internal/services/core"
)

type ProcessConfig struct {
	Provider provider.Provider
	BaseConfig
	Stdin   string
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
	lifecycle := NewRunLifecycleTracker(e.cfg.BaseConfig)

	bin, args := e.cfg.Provider.BuildCommand(prompt)
	bin = strings.TrimSpace(bin)
	if bin == "" {
		cancel()
		return nil, fmt.Errorf("provider 返回空命令")
	}

	runID, err := lifecycle.CreatePending(execCtx, marshalJSON(map[string]any{
		"provider":       e.cfg.Provider.Name(),
		"bin":            bin,
		"args":           args,
		"prompt_preview": truncateRunes(prompt, 256),
	}))
	if err != nil {
		cancel()
		return nil, err
	}

	cmd := exec.CommandContext(execCtx, bin, args...)
	if strings.TrimSpace(e.cfg.WorkDir) != "" {
		cmd.Dir = strings.TrimSpace(e.cfg.WorkDir)
	}
	if e.cfg.Stdin != "" {
		cmd.Stdin = strings.NewReader(e.cfg.Stdin)
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

	var lease *time.Time
	if e.cfg.Timeout > 0 {
		l := time.Now().Add(e.cfg.Timeout)
		lease = &l
	}
	if err := lifecycle.MarkRunning(execCtx, fmt.Sprintf("pid:%d", cmd.Process.Pid), lease, "process executor started"); err != nil {
		cancel()
		return nil, err
	}

	return &processHandle{
		runID:     runID,
		provider:  e.cfg.Provider,
		lifecycle: lifecycle,
		cmd:       cmd,
		cancel:    cancel,
		execCtx:   execCtx,
		stdoutBuf: &stdout,
		stderrBuf: &stderr,
		doneCh:    make(chan struct{}),
	}, nil
}

type processHandle struct {
	runID     uint
	provider  provider.Provider
	lifecycle *RunLifecycleTracker

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

func (h *processHandle) Wait(ctx context.Context) (AgentRunResult, error) {
	if h == nil {
		return AgentRunResult{}, fmt.Errorf("process handle 为空")
	}
	h.start()
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-h.doneCh:
		return h.waitRes, h.waitErr
	case <-ctx.Done():
		slog.Info("process_handle: context canceled during wait, canceling process",
			"run_id", h.runID, "err", ctx.Err())
		if h.cancel != nil {
			h.cancel()
		}
		return AgentRunResult{}, ctx.Err()
	}
}

func (h *processHandle) start() {
	h.once.Do(func() {
		go func() {
			h.waitRes, h.waitErr = h.waitOnce()
			close(h.doneCh)
		}()
	})
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

	h.lifecycle.Finish(h.execCtx, res, err, "process executor finished")

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
	slog.Warn("process: marking run as failed", "run_id", runID, "code", code, "msg", msg)
	writeCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return rt.MarkRunFailed(writeCtx, runID, strings.TrimSpace(code), strings.TrimSpace(msg), time.Now())
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
