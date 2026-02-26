package testutil

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"dalek/internal/infra"
	"dalek/internal/repo"
	"dalek/internal/services/core"
	tasksvc "dalek/internal/services/task"
	"dalek/internal/store"
)

type FakeTmuxClient struct {
	Sessions         map[string]bool
	SessionsBySocket map[string]map[string]bool
	ListErrBySocket  map[string]error
	SendLineHistory  []string

	NewSessionCalls  int
	KillSessionCalls int
	SendKeysCalls    int
	SendLineCalls    int

	NewSessionErr  error
	KillSessionErr error
}

func (f *FakeTmuxClient) Ensure() {
	if f.Sessions == nil {
		f.Sessions = map[string]bool{}
	}
	if f.SessionsBySocket == nil {
		f.SessionsBySocket = map[string]map[string]bool{}
	}
	if f.ListErrBySocket == nil {
		f.ListErrBySocket = map[string]error{}
	}
}

func (f *FakeTmuxClient) NewSession(ctx context.Context, socket, name, startDir string) error {
	_ = ctx
	_ = socket
	_ = startDir
	f.Ensure()
	name = strings.TrimSpace(name)
	if f.NewSessionErr != nil {
		return f.NewSessionErr
	}
	f.NewSessionCalls++
	f.Sessions[name] = true
	return nil
}

func (f *FakeTmuxClient) NewSessionWithCommand(ctx context.Context, socket, name, startDir string, cmd []string) error {
	_ = cmd
	return f.NewSession(ctx, socket, name, startDir)
}

func (f *FakeTmuxClient) KillSession(ctx context.Context, socket, name string) error {
	_ = ctx
	_ = socket
	f.Ensure()
	name = strings.TrimSpace(name)
	f.KillSessionCalls++
	if f.KillSessionErr != nil {
		return f.KillSessionErr
	}
	delete(f.Sessions, name)
	for _, m := range f.SessionsBySocket {
		delete(m, name)
	}
	return nil
}

func (f *FakeTmuxClient) KillServer(ctx context.Context, socket string) error {
	_ = ctx
	_ = socket
	f.Ensure()
	f.Sessions = map[string]bool{}
	f.SessionsBySocket = map[string]map[string]bool{}
	return nil
}

func (f *FakeTmuxClient) SendKeys(ctx context.Context, socket, target, keys string) error {
	_ = ctx
	_ = socket
	_ = target
	_ = keys
	f.SendKeysCalls++
	return nil
}

func (f *FakeTmuxClient) SendKeysLiteral(ctx context.Context, socket, target, text string) error {
	_ = ctx
	_ = socket
	_ = target
	_ = text
	return nil
}

func (f *FakeTmuxClient) SendLine(ctx context.Context, socket, target, line string) error {
	_ = ctx
	_ = socket
	_ = target
	f.SendLineCalls++
	f.SendLineHistory = append(f.SendLineHistory, strings.TrimSpace(line))
	return nil
}

func (f *FakeTmuxClient) CapturePane(ctx context.Context, socket, target string, lines int) (string, error) {
	_ = ctx
	_ = socket
	_ = target
	_ = lines
	return "ok", nil
}

func (f *FakeTmuxClient) PipePaneToFile(ctx context.Context, socket, target, filePath string) error {
	_ = ctx
	_ = socket
	_ = target
	_ = filePath
	return nil
}

func (f *FakeTmuxClient) StopPipePane(ctx context.Context, socket, target string) error {
	_ = ctx
	_ = socket
	_ = target
	return nil
}

func (f *FakeTmuxClient) ListSessions(ctx context.Context, socket string) (map[string]bool, error) {
	_ = ctx
	f.Ensure()
	socket = strings.TrimSpace(socket)
	if err := f.ListErrBySocket[socket]; err != nil {
		return nil, err
	}
	src := f.Sessions
	if m := f.SessionsBySocket[socket]; m != nil {
		src = m
	}
	out := make(map[string]bool, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out, nil
}

func (f *FakeTmuxClient) ListPanes(ctx context.Context, socket, session string) ([]infra.PaneInfo, error) {
	_ = ctx
	_ = socket
	_ = session
	return []infra.PaneInfo{
		{PaneID: "%1", CurrentCommand: "bash"},
	}, nil
}

func (f *FakeTmuxClient) ActivePane(ctx context.Context, socket, session string) (infra.PaneInfo, error) {
	_ = ctx
	_ = socket
	_ = session
	return infra.PaneInfo{PaneID: "%1", CurrentCommand: "bash"}, nil
}

func (f *FakeTmuxClient) AttachCmd(socket, session string) *exec.Cmd {
	_ = socket
	_ = session
	return exec.Command("true")
}

type FakeGitClient struct {
	AddCalls           int
	CurrentBranchValue string
	LastBaseBranch     string
	PruneFn            func(repoRoot string) error
	AddErr             error
	AfterAdd           func(path string) error
	RemoveErr          error
	RemoveCalls        int
	RemovedPaths       []string
}

func (f *FakeGitClient) CurrentBranch(repoRoot string) (string, error) {
	_ = repoRoot
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
	_ = path
	return false, nil
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

func NewTestProject(t testing.TB) (*core.Project, *FakeTmuxClient, *FakeGitClient) {
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

	cfg := repo.Config{
		TmuxSocket:        "dalek",
		BranchPrefix:      "",
		RefreshIntervalMS: 1000,
		WorkerAgent: repo.AgentExecConfig{
			Provider: "codex",
			Mode:     "sdk",
			Command:  workerScript,
		},
		PMAgent: repo.AgentExecConfig{
			Provider: "codex",
			Mode:     "cli",
			Command:  dispatchScript,
		},
	}.WithDefaults()

	fTmux := &FakeTmuxClient{
		Sessions: map[string]bool{},
	}
	fGit := &FakeGitClient{
		CurrentBranchValue: "main",
	}

	cp, err := core.NewProject(core.NewProjectInput{
		Name:         "demo",
		Key:          "demo",
		RepoRoot:     repoRoot,
		Layout:       layout,
		WorktreesDir: worktreesDir,
		WorkersDir:   layout.RuntimeWorkersDir,
		Config:       cfg,
		DB:           db,
		Logger:       core.DiscardLogger(),
		Tmux:         fTmux,
		Git:          fGit,
		TaskRuntime:  tasksvc.NewRuntimeFactory(),
	})
	if err != nil {
		t.Fatalf("NewProject failed: %v", err)
	}
	return cp, fTmux, fGit
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
