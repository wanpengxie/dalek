package infra

import (
	"context"
	"os/exec"
	"time"
)

// WorkerProcessSpec 描述待启动的 worker 进程参数。
type WorkerProcessSpec struct {
	Command string
	Args    []string
	WorkDir string
	Env     map[string]string
	LogPath string
}

// WorkerProcessHandle 表示一个已启动 worker 进程的运行句柄。
type WorkerProcessHandle struct {
	PID       int
	Command   string
	Args      []string
	WorkDir   string
	LogPath   string
	StartedAt time.Time
}

// WorkerRuntime 抽象 worker 进程运行时能力，供后续从 tmux 渐进迁移。
type WorkerRuntime interface {
	StartProcess(ctx context.Context, spec WorkerProcessSpec) (WorkerProcessHandle, error)
	StopProcess(ctx context.Context, handle WorkerProcessHandle, timeout time.Duration) error
	InterruptProcess(ctx context.Context, handle WorkerProcessHandle) error
	IsAlive(ctx context.Context, handle WorkerProcessHandle) (bool, error)
	CaptureOutput(ctx context.Context, handle WorkerProcessHandle, lines int) (string, error)
	AttachCmd(handle WorkerProcessHandle) *exec.Cmd
}
