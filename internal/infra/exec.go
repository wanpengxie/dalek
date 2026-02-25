package infra

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// CommandRunner 抽象了最小命令执行能力，便于上层注入 mock。
type CommandRunner interface {
	Run(ctx context.Context, dir string, name string, args ...string) (string, error)
	RunExitCode(ctx context.Context, dir string, name string, args ...string) (int, string, string, error)
}

type ExecRunner struct{}

func NewExecRunner() *ExecRunner {
	return &ExecRunner{}
}

func (r *ExecRunner) Run(ctx context.Context, dir string, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	if strings.TrimSpace(dir) != "" {
		cmd.Dir = dir
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = strings.TrimSpace(stdout.String())
		}
		if msg != "" {
			msg = ": " + msg
		}
		return "", fmt.Errorf("%s %s 失败: %w%s", name, strings.Join(args, " "), err, msg)
	}
	return strings.TrimSpace(stdout.String()), nil
}

func (r *ExecRunner) RunExitCode(ctx context.Context, dir string, name string, args ...string) (int, string, string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	if strings.TrimSpace(dir) != "" {
		cmd.Dir = dir
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err == nil {
		return 0, strings.TrimSpace(stdout.String()), strings.TrimSpace(stderr.String()), nil
	}
	var ee *exec.ExitError
	if ok := errors.As(err, &ee); ok {
		return ee.ExitCode(), strings.TrimSpace(stdout.String()), strings.TrimSpace(stderr.String()), nil
	}
	return -1, strings.TrimSpace(stdout.String()), strings.TrimSpace(stderr.String()), err
}

func Run(ctx context.Context, dir string, name string, args ...string) (string, error) {
	return NewExecRunner().Run(ctx, dir, name, args...)
}

func RunExitCode(ctx context.Context, dir string, name string, args ...string) (int, string, string, error) {
	return NewExecRunner().RunExitCode(ctx, dir, name, args...)
}
