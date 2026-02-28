package infra

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	defaultStopTimeout = 5 * time.Second
	stopPollInterval   = 50 * time.Millisecond
)

type DaemonProcessManager struct {
	mu        sync.RWMutex
	processes map[int]*processState
}

func NewDaemonProcessManager() *DaemonProcessManager {
	return &DaemonProcessManager{
		processes: map[int]*processState{},
	}
}

type processState struct {
	cmd  *exec.Cmd
	done chan struct{}
}

func (m *DaemonProcessManager) StartProcess(ctx context.Context, spec WorkerProcessSpec) (WorkerProcessHandle, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return WorkerProcessHandle{}, err
	}

	cmdName := strings.TrimSpace(spec.Command)
	if cmdName == "" {
		return WorkerProcessHandle{}, fmt.Errorf("worker 启动命令不能为空")
	}
	logPath := strings.TrimSpace(spec.LogPath)
	if logPath == "" {
		return WorkerProcessHandle{}, fmt.Errorf("worker 日志路径不能为空")
	}
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		return WorkerProcessHandle{}, fmt.Errorf("创建日志目录失败: %w", err)
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return WorkerProcessHandle{}, fmt.Errorf("打开日志文件失败: %w", err)
	}

	cmd := exec.Command(cmdName, spec.Args...)
	if wd := strings.TrimSpace(spec.WorkDir); wd != "" {
		cmd.Dir = wd
	}
	if len(spec.Env) > 0 {
		cmd.Env = mergeEnvWithCurrent(spec.Env)
	}
	cmd.Stdout = logFile
	cmd.Stderr = logFile

	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		return WorkerProcessHandle{}, fmt.Errorf("启动 worker 进程失败: %w", err)
	}
	_ = logFile.Close()

	st := &processState{
		cmd:  cmd,
		done: make(chan struct{}),
	}
	m.registerProcess(cmd.Process.Pid, st)
	go m.waitProcess(cmd.Process.Pid, st)

	return WorkerProcessHandle{
		PID:       cmd.Process.Pid,
		Command:   cmdName,
		Args:      append([]string(nil), spec.Args...),
		WorkDir:   strings.TrimSpace(spec.WorkDir),
		LogPath:   logPath,
		StartedAt: time.Now(),
	}, nil
}

func (m *DaemonProcessManager) StopProcess(ctx context.Context, handle WorkerProcessHandle, timeout time.Duration) error {
	pid := handle.PID
	if pid <= 0 {
		return fmt.Errorf("worker pid 非法: %d", pid)
	}
	if timeout <= 0 {
		timeout = defaultStopTimeout
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if st, ok := m.lookupProcess(pid); ok {
		if processDone(st.done) {
			return nil
		}
		if err := st.cmd.Process.Signal(syscall.SIGTERM); err != nil && !errors.Is(err, os.ErrProcessDone) {
			return fmt.Errorf("发送 SIGTERM 失败(pid=%d): %w", pid, err)
		}
		if waitDone(ctx, st.done, timeout) {
			return nil
		}
		if err := st.cmd.Process.Signal(syscall.SIGKILL); err != nil && !errors.Is(err, os.ErrProcessDone) {
			return fmt.Errorf("发送 SIGKILL 失败(pid=%d): %w", pid, err)
		}
		if waitDone(ctx, st.done, 2*time.Second) {
			return nil
		}
		return fmt.Errorf("worker 进程停止超时(pid=%d)", pid)
	}

	alive, err := isProcessAlivePID(pid)
	if err != nil {
		return err
	}
	if !alive {
		return nil
	}

	proc, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("查找 worker 进程失败: %w", err)
	}
	if err := proc.Signal(syscall.SIGTERM); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return fmt.Errorf("发送 SIGTERM 失败(pid=%d): %w", pid, err)
	}
	if waitProcessExit(ctx, pid, timeout) {
		return nil
	}

	if err := proc.Signal(syscall.SIGKILL); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return fmt.Errorf("发送 SIGKILL 失败(pid=%d): %w", pid, err)
	}
	if waitProcessExit(ctx, pid, 2*time.Second) {
		return nil
	}
	return fmt.Errorf("worker 进程停止超时(pid=%d)", pid)
}

func (m *DaemonProcessManager) InterruptProcess(ctx context.Context, handle WorkerProcessHandle) error {
	pid := handle.PID
	if pid <= 0 {
		return fmt.Errorf("worker pid 非法: %d", pid)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if st, ok := m.lookupProcess(pid); ok {
		if processDone(st.done) {
			return nil
		}
		if err := st.cmd.Process.Signal(os.Interrupt); err != nil && !errors.Is(err, os.ErrProcessDone) {
			return fmt.Errorf("发送 SIGINT 失败(pid=%d): %w", pid, err)
		}
		return nil
	}

	alive, err := isProcessAlivePID(pid)
	if err != nil {
		return err
	}
	if !alive {
		return nil
	}

	proc, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("查找 worker 进程失败: %w", err)
	}
	if err := proc.Signal(os.Interrupt); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return fmt.Errorf("发送 SIGINT 失败(pid=%d): %w", pid, err)
	}
	return nil
}

func (m *DaemonProcessManager) IsAlive(ctx context.Context, handle WorkerProcessHandle) (bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if handle.PID <= 0 {
		return false, fmt.Errorf("worker pid 非法: %d", handle.PID)
	}
	if st, ok := m.lookupProcess(handle.PID); ok {
		return !processDone(st.done), nil
	}
	return isProcessAlivePID(handle.PID)
}

func (m *DaemonProcessManager) CaptureOutput(ctx context.Context, handle WorkerProcessHandle, lines int) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	logPath := strings.TrimSpace(handle.LogPath)
	if logPath == "" {
		return "", fmt.Errorf("worker 日志路径为空")
	}
	if lines <= 0 {
		lines = 200
	}
	b, err := os.ReadFile(logPath)
	if err != nil {
		return "", fmt.Errorf("读取 worker 日志失败: %w", err)
	}
	return tailTextLines(string(b), lines), nil
}

func (m *DaemonProcessManager) AttachCmd(handle WorkerProcessHandle) *exec.Cmd {
	logPath := strings.TrimSpace(handle.LogPath)
	if logPath == "" {
		logPath = "/dev/null"
	}
	return exec.Command("tail", "-n", "200", "-F", logPath)
}

func mergeEnvWithCurrent(extra map[string]string) []string {
	merged := map[string]string{}
	for _, entry := range os.Environ() {
		k, v, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		merged[k] = v
	}
	for k, v := range extra {
		key := strings.TrimSpace(k)
		if key == "" {
			continue
		}
		merged[key] = v
	}
	out := make([]string, 0, len(merged))
	for k, v := range merged {
		out = append(out, k+"="+v)
	}
	return out
}

func processDone(done <-chan struct{}) bool {
	select {
	case <-done:
		return true
	default:
		return false
	}
}

func waitDone(ctx context.Context, done <-chan struct{}, timeout time.Duration) bool {
	if timeout <= 0 {
		timeout = defaultStopTimeout
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-done:
		return true
	case <-ctx.Done():
		return false
	case <-timer.C:
		return false
	}
}

func isProcessAlivePID(pid int) (bool, error) {
	if pid <= 0 {
		return false, fmt.Errorf("worker pid 非法: %d", pid)
	}
	err := syscall.Kill(pid, 0)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, syscall.ESRCH) {
		return false, nil
	}
	if errors.Is(err, syscall.EPERM) {
		return true, nil
	}
	return false, fmt.Errorf("探测 worker 进程存活失败(pid=%d): %w", pid, err)
}

func waitProcessExit(ctx context.Context, pid int, timeout time.Duration) bool {
	if timeout <= 0 {
		timeout = defaultStopTimeout
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	ticker := time.NewTicker(stopPollInterval)
	defer ticker.Stop()

	for {
		alive, err := isProcessAlivePID(pid)
		if err == nil && !alive {
			return true
		}
		if ctx != nil {
			select {
			case <-ctx.Done():
				return false
			default:
			}
		}
		select {
		case <-timer.C:
			return false
		case <-ticker.C:
		}
	}
}

func (m *DaemonProcessManager) registerProcess(pid int, st *processState) {
	if pid <= 0 || st == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.processes == nil {
		m.processes = map[int]*processState{}
	}
	m.processes[pid] = st
}

func (m *DaemonProcessManager) lookupProcess(pid int) (*processState, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	st, ok := m.processes[pid]
	return st, ok
}

func (m *DaemonProcessManager) waitProcess(pid int, st *processState) {
	if st == nil || st.cmd == nil {
		return
	}
	_ = st.cmd.Wait()
	close(st.done)

	m.mu.Lock()
	defer m.mu.Unlock()
	cur, ok := m.processes[pid]
	if ok && cur == st {
		delete(m.processes, pid)
	}
}

func tailTextLines(raw string, lines int) string {
	if lines <= 0 {
		lines = 200
	}
	raw = strings.ReplaceAll(raw, "\r\n", "\n")
	parts := strings.Split(raw, "\n")
	if len(parts) > 0 && parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	if len(parts) <= lines {
		return strings.Join(parts, "\n")
	}
	return strings.Join(parts[len(parts)-lines:], "\n")
}
