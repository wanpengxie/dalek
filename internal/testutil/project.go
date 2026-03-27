package testutil

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"dalek/internal/infra"
	"dalek/internal/repo"
	"dalek/internal/services/core"
	tasksvc "dalek/internal/services/task"
	"dalek/internal/store"
)

type FakeGitClient struct {
	AddCalls           int
	CurrentBranchValue string
	CurrentBranchErr   error
	LastBaseBranch     string
	PruneFn            func(repoRoot string) error
	WorktreeDirtyFn    func(path string) (bool, error)
	WorktreeDirtyValue bool
	WorktreeDirtyErr   error
	AddErr             error
	AfterAdd           func(path string) error
	RemoveErr          error
	RemoveCalls        int
	RemovedPaths       []string
}

type FakeWorkerRuntime struct {
	StartCalls     int
	StopCalls      int
	InterruptCalls int
	IsAliveCalls   int

	StartErr     error
	StopErr      error
	InterruptErr error
	IsAliveErr   error
	CaptureErr   error

	NextPID int

	StartSpecs       []infra.WorkerProcessSpec
	StopHandles      []infra.WorkerProcessHandle
	InterruptHandles []infra.WorkerProcessHandle
	CaptureHandles   []infra.WorkerProcessHandle

	AliveByPID  map[int]bool
	CaptureText string
}

func (f *FakeWorkerRuntime) ensure() {
	if f.AliveByPID == nil {
		f.AliveByPID = map[int]bool{}
	}
	if f.NextPID <= 0 {
		f.NextPID = 1000
	}
}

func (f *FakeWorkerRuntime) StartProcess(ctx context.Context, spec infra.WorkerProcessSpec) (infra.WorkerProcessHandle, error) {
	_ = ctx
	f.ensure()
	f.StartCalls++
	f.StartSpecs = append(f.StartSpecs, spec)
	if f.StartErr != nil {
		return infra.WorkerProcessHandle{}, f.StartErr
	}
	pid := f.NextPID
	f.NextPID++
	f.AliveByPID[pid] = true
	return infra.WorkerProcessHandle{
		PID:       pid,
		Command:   strings.TrimSpace(spec.Command),
		Args:      append([]string(nil), spec.Args...),
		WorkDir:   strings.TrimSpace(spec.WorkDir),
		LogPath:   strings.TrimSpace(spec.LogPath),
		StartedAt: time.Now(),
	}, nil
}

func (f *FakeWorkerRuntime) StopProcess(ctx context.Context, handle infra.WorkerProcessHandle, timeout time.Duration) error {
	_ = ctx
	_ = timeout
	f.ensure()
	f.StopCalls++
	f.StopHandles = append(f.StopHandles, handle)
	if f.StopErr != nil {
		return f.StopErr
	}
	if handle.PID > 0 {
		f.AliveByPID[handle.PID] = false
	}
	return nil
}

func (f *FakeWorkerRuntime) InterruptProcess(ctx context.Context, handle infra.WorkerProcessHandle) error {
	_ = ctx
	f.ensure()
	f.InterruptCalls++
	f.InterruptHandles = append(f.InterruptHandles, handle)
	if f.InterruptErr != nil {
		return f.InterruptErr
	}
	return nil
}

func (f *FakeWorkerRuntime) IsAlive(ctx context.Context, handle infra.WorkerProcessHandle) (bool, error) {
	_ = ctx
	f.ensure()
	f.IsAliveCalls++
	if f.IsAliveErr != nil {
		return false, f.IsAliveErr
	}
	return f.AliveByPID[handle.PID], nil
}

func (f *FakeWorkerRuntime) CaptureOutput(ctx context.Context, handle infra.WorkerProcessHandle, lines int) (string, error) {
	_ = ctx
	_ = lines
	f.CaptureHandles = append(f.CaptureHandles, handle)
	if f.CaptureErr != nil {
		return "", f.CaptureErr
	}
	return f.CaptureText, nil
}

func (f *FakeWorkerRuntime) AttachCmd(handle infra.WorkerProcessHandle) *exec.Cmd {
	logPath := strings.TrimSpace(handle.LogPath)
	if logPath == "" {
		return exec.Command("true")
	}
	return exec.Command("tail", "-n", "200", "-F", logPath)
}

func (f *FakeGitClient) CurrentBranch(repoRoot string) (string, error) {
	_ = repoRoot
	if f.CurrentBranchErr != nil {
		return "", f.CurrentBranchErr
	}
	cur := strings.TrimSpace(f.CurrentBranchValue)
	if cur == "" {
		cur = "main"
	}
	return cur, nil
}

func (f *FakeGitClient) AddWorktree(repoRoot, path, branch, baseBranch string) error {
	_ = repoRoot
	_ = branch
	if f.AddErr != nil {
		return f.AddErr
	}
	f.AddCalls++
	f.LastBaseBranch = strings.TrimSpace(baseBranch)
	if err := os.MkdirAll(path, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(path, ".git"), []byte("gitdir: /tmp/fake\n"), 0o644); err != nil {
		return err
	}
	if f.AfterAdd != nil {
		return f.AfterAdd(path)
	}
	return nil
}

func (f *FakeGitClient) RemoveWorktree(repoRoot, path string, force bool) error {
	_ = repoRoot
	_ = force
	f.RemoveCalls++
	f.RemovedPaths = append(f.RemovedPaths, strings.TrimSpace(path))
	if f.RemoveErr != nil {
		return f.RemoveErr
	}
	return os.RemoveAll(path)
}

func (f *FakeGitClient) WorktreeDirty(path string) (bool, error) {
	if f.WorktreeDirtyFn != nil {
		return f.WorktreeDirtyFn(path)
	}
	if f.WorktreeDirtyErr != nil {
		return false, f.WorktreeDirtyErr
	}
	return f.WorktreeDirtyValue, nil
}

func (f *FakeGitClient) HasCommit(repoRoot string) (bool, error) {
	_ = repoRoot
	return true, nil
}

func (f *FakeGitClient) WorktreeBranchCheckedOut(repoRoot, branch string) (bool, string, error) {
	_ = repoRoot
	_ = branch
	return false, "", nil
}

func (f *FakeGitClient) PruneWorktrees(repoRoot string) error {
	if f.PruneFn != nil {
		return f.PruneFn(repoRoot)
	}
	return nil
}

func (f *FakeGitClient) IsWorktreeDir(path string) bool {
	st, err := os.Stat(filepath.Join(path, ".git"))
	return err == nil && !st.IsDir()
}

func NewTestProject(t testing.TB) (*core.Project, *FakeGitClient) {
	t.Helper()

	repoRoot := t.TempDir()
	layout := repo.NewLayout(repoRoot)
	worktreesDir := filepath.Join(t.TempDir(), "worktrees")
	if err := os.MkdirAll(worktreesDir, 0o755); err != nil {
		t.Fatalf("mkdir worktrees failed: %v", err)
	}

	if err := repo.EnsureControlPlaneSeed(layout, "demo"); err != nil {
		t.Fatalf("EnsureControlPlaneSeed failed: %v", err)
	}

	if err := os.MkdirAll(filepath.Dir(layout.DBPath), 0o755); err != nil {
		t.Fatalf("mkdir runtime failed: %v", err)
	}
	db, err := store.OpenAndMigrate(layout.DBPath)
	if err != nil {
		t.Fatalf("OpenAndMigrate failed: %v", err)
	}

	dispatchScript := writeExecutable(t, "test_pm_agent_dispatch", `#!/usr/bin/env bash
set -euo pipefail
if [[ -n "${DALEK_TEST_PROMPT_PATH:-}" ]]; then
  prompt=""
  if [[ "$#" -gt 0 ]]; then
    prompt="${@: -1}"
  fi
  printf "%s\n" "$prompt" > "${DALEK_TEST_PROMPT_PATH}"
fi
echo "dispatch_done request_id=${DALEK_DISPATCH_REQUEST_ID:-}"
`)
	workerScript := writeExecutable(t, "test_worker_agent", `#!/usr/bin/env bash
set -euo pipefail
cat >/dev/null
echo '{"type":"thread.started","thread_id":"thread-worker-default"}'
echo '{"type":"item.completed","item":{"id":"msg-worker-default","type":"agent_message","text":"worker default ok"}}'
`)

	_ = workerScript   // v3: command 不再由 role 配置，始终走 SDK
	_ = dispatchScript // v3: command 不再由 role 配置

	cfg := repo.Config{
		BranchPrefix:      "",
		RefreshIntervalMS: 1000,
		WorkerAgent: repo.RoleConfig{
			Provider: "codex",
		},
		PMAgent: repo.RoleConfig{
			Provider: "codex",
		},
	}.WithDefaults()

	fGit := &FakeGitClient{
		CurrentBranchValue: "main",
	}
	fRuntime := &FakeWorkerRuntime{}

	cp, err := core.NewProject(core.NewProjectInput{
		Name:          "demo",
		Key:           "demo",
		RepoRoot:      repoRoot,
		Layout:        layout,
		WorktreesDir:  worktreesDir,
		WorkersDir:    layout.RuntimeWorkersDir,
		Config:        cfg,
		Providers:     repo.DefaultProviders(),
		DB:            db,
		Logger:        core.DiscardLogger(),
		WorkerRuntime: fRuntime,
		Git:           fGit,
		TaskRuntime:   tasksvc.NewRuntimeFactory(),
	})
	if err != nil {
		t.Fatalf("NewProject failed: %v", err)
	}
	return cp, fGit
}

func writeExecutable(t testing.TB, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), strings.TrimSpace(name))
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatalf("write executable %s failed: %v", name, err)
	}
	_ = os.Chmod(path, 0o755)
	return path
}
