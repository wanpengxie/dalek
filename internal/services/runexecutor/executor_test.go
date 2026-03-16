package runexecutor

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestStubExecutor_Execute_AcceptsKnownStageAndTarget(t *testing.T) {
	exec := NewStubExecutor(New(nil))
	fixedNow := time.Date(2026, 3, 14, 15, 0, 0, 0, time.UTC)
	exec.now = func() time.Time { return fixedNow }

	res, err := exec.Execute(context.Background(), ExecuteRequest{
		ProjectKey:   "demo",
		RunID:        42,
		TaskRunID:    42,
		TargetName:   "test",
		Stage:        StageVerify,
		WorkspaceDir: "/tmp/run-42",
		SnapshotID:   "snap-1",
		BaseCommit:   "abc123",
		Attempt:      1,
	})
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if !res.Accepted || res.Stage != StageVerify {
		t.Fatalf("unexpected execute result: %+v", res)
	}
	if res.Target.Name != "test" {
		t.Fatalf("unexpected target: %+v", res.Target)
	}
	if len(res.Command) != 3 || res.Command[0] != "go" {
		t.Fatalf("unexpected command: %+v", res.Command)
	}
	if res.WorkspaceDir != "/tmp/run-42" || res.SnapshotID != "snap-1" || res.BaseCommit != "abc123" {
		t.Fatalf("unexpected execution context: %+v", res)
	}
	if res.StartedAt != fixedNow || res.FinishedAt != fixedNow {
		t.Fatalf("unexpected timestamps: %+v", res)
	}
}

func TestStubExecutor_Execute_RejectsInvalidStage(t *testing.T) {
	exec := NewStubExecutor(New(nil))

	_, err := exec.Execute(context.Background(), ExecuteRequest{
		TargetName: "test",
		Stage:      Stage("shell"),
	})
	if err == nil || !strings.Contains(err.Error(), "stage") {
		t.Fatalf("expected invalid stage error, got=%v", err)
	}
}

func TestStubExecutor_Execute_RejectsUnknownTarget(t *testing.T) {
	exec := NewStubExecutor(New(nil))

	_, err := exec.Execute(context.Background(), ExecuteRequest{
		TargetName: "go test ./...",
		Stage:      StageVerify,
	})
	if err == nil || !strings.Contains(err.Error(), "verify_target") {
		t.Fatalf("expected verify target validation error, got=%v", err)
	}
}

type fakeRunner struct {
	exitCode int
	stdout   string
	stderr   string
	err      error

	calls   int
	lastDir string
	lastCmd string
	lastArg []string
}

func (f *fakeRunner) Run(ctx context.Context, dir string, name string, args ...string) (string, error) {
	f.calls++
	f.lastDir = dir
	f.lastCmd = name
	f.lastArg = append([]string(nil), args...)
	if f.err != nil {
		return "", f.err
	}
	if f.exitCode != 0 {
		return "", errors.New("non-zero exit")
	}
	return f.stdout, nil
}

func (f *fakeRunner) RunExitCode(ctx context.Context, dir string, name string, args ...string) (int, string, string, error) {
	f.calls++
	f.lastDir = dir
	f.lastCmd = name
	f.lastArg = append([]string(nil), args...)
	return f.exitCode, f.stdout, f.stderr, f.err
}

func TestLocalExecutor_Execute_Success(t *testing.T) {
	runner := &fakeRunner{exitCode: 0, stdout: "ok"}
	exec := NewLocalExecutor(New([]TargetSpec{{Name: "test", Command: []string{"echo", "ok"}}}), runner)
	fixedNow := time.Date(2026, 3, 14, 16, 0, 0, 0, time.UTC)
	exec.now = func() time.Time { return fixedNow }

	res, err := exec.Execute(context.Background(), ExecuteRequest{
		TargetName:   "test",
		Stage:        StageVerify,
		WorkspaceDir: "/tmp/run-1",
	})
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if !res.Accepted || res.Stage != StageVerify {
		t.Fatalf("unexpected result: %+v", res)
	}
	if res.Summary == "" || !strings.Contains(res.Summary, "exit=0") {
		t.Fatalf("unexpected summary: %q", res.Summary)
	}
	if runner.lastCmd != "echo" || len(runner.lastArg) != 1 || runner.lastArg[0] != "ok" {
		t.Fatalf("unexpected runner command: %s %+v", runner.lastCmd, runner.lastArg)
	}
}

func TestLocalExecutor_Execute_UsesStageCommandWhenProvided(t *testing.T) {
	runner := &fakeRunner{exitCode: 0, stdout: "ok"}
	exec := NewLocalExecutor(New([]TargetSpec{{
		Name:    "test",
		Command: []string{"echo", "verify"},
		Bootstrap: StageCommand{
			Command: []string{"echo", "bootstrap"},
		},
	}}), runner)

	_, err := exec.Execute(context.Background(), ExecuteRequest{
		TargetName: "test",
		Stage:      StageBootstrap,
	})
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if runner.lastCmd != "echo" || len(runner.lastArg) != 1 || runner.lastArg[0] != "bootstrap" {
		t.Fatalf("expected bootstrap command, got: %s %+v", runner.lastCmd, runner.lastArg)
	}
}

func TestLocalExecutor_Execute_SkipsStageWhenNoCommand(t *testing.T) {
	runner := &fakeRunner{exitCode: 0, stdout: "ok"}
	exec := NewLocalExecutor(New([]TargetSpec{{
		Name:    "test",
		Command: []string{"echo", "verify"},
	}}), runner)

	res, err := exec.Execute(context.Background(), ExecuteRequest{
		TargetName: "test",
		Stage:      StagePreflight,
	})
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if !res.Accepted || !strings.Contains(res.Summary, "skipped") {
		t.Fatalf("expected skipped summary, got: %+v", res)
	}
	if runner.calls != 0 {
		t.Fatalf("expected runner not to be called, got=%d", runner.calls)
	}
}

func TestLocalExecutor_Execute_Failure(t *testing.T) {
	runner := &fakeRunner{exitCode: 1, stderr: "boom"}
	exec := NewLocalExecutor(New([]TargetSpec{{Name: "test", Command: []string{"false"}}}), runner)

	_, err := exec.Execute(context.Background(), ExecuteRequest{
		TargetName: "test",
		Stage:      StageVerify,
	})
	if err == nil || !strings.Contains(err.Error(), "exit=1") {
		t.Fatalf("expected exit error, got=%v", err)
	}
}

func TestLocalExecutor_Execute_Timeout(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	runner := &fakeRunner{err: context.DeadlineExceeded}
	exec := NewLocalExecutor(New([]TargetSpec{{Name: "test", Command: []string{"sleep", "1"}, Timeout: 5 * time.Second}}), runner)

	_, err := exec.Execute(ctx, ExecuteRequest{
		TargetName: "test",
		Stage:      StageVerify,
	})
	if err == nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected timeout error, got=%v", err)
	}
}

func TestLocalExecutor_Execute_Canceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	runner := &fakeRunner{}
	exec := NewLocalExecutor(New([]TargetSpec{{Name: "test", Command: []string{"echo", "ok"}}}), runner)

	_, err := exec.Execute(ctx, ExecuteRequest{
		TargetName: "test",
		Stage:      StageVerify,
	})
	if err == nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("expected canceled error, got=%v", err)
	}
}
