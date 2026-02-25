package pm

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
	"dalek/internal/services/worker"
	"dalek/internal/store"

	"gorm.io/gorm"
)

type fakeTmuxClient struct {
	sessions         map[string]bool
	sessionsBySocket map[string]map[string]bool
	newSessionCalls  int
	killSessionCalls int
	sendKeysCalls    int
	sendLineCalls    int
	sendLineHistory  []string
}

func (f *fakeTmuxClient) ensure() {
	if f.sessions == nil {
		f.sessions = map[string]bool{}
	}
	if f.sessionsBySocket == nil {
		f.sessionsBySocket = map[string]map[string]bool{}
	}
}

func (f *fakeTmuxClient) NewSession(ctx context.Context, socket, name, startDir string) error {
	f.ensure()
	name = strings.TrimSpace(name)
	f.newSessionCalls++
	f.sessions[name] = true
	return nil
}

func (f *fakeTmuxClient) NewSessionWithCommand(ctx context.Context, socket, name, startDir string, cmd []string) error {
	return f.NewSession(ctx, socket, name, startDir)
}

func (f *fakeTmuxClient) KillSession(ctx context.Context, socket, name string) error {
	f.ensure()
	name = strings.TrimSpace(name)
	f.killSessionCalls++
	delete(f.sessions, name)
	for _, m := range f.sessionsBySocket {
		delete(m, name)
	}
	return nil
}

func (f *fakeTmuxClient) KillServer(ctx context.Context, socket string) error {
	f.ensure()
	f.sessions = map[string]bool{}
	f.sessionsBySocket = map[string]map[string]bool{}
	return nil
}

func (f *fakeTmuxClient) SendKeys(ctx context.Context, socket, target, keys string) error {
	f.sendKeysCalls++
	return nil
}

func (f *fakeTmuxClient) SendKeysLiteral(ctx context.Context, socket, target, text string) error {
	return nil
}

func (f *fakeTmuxClient) SendLine(ctx context.Context, socket, target, line string) error {
	f.sendLineCalls++
	f.sendLineHistory = append(f.sendLineHistory, strings.TrimSpace(line))
	return nil
}

func (f *fakeTmuxClient) CapturePane(ctx context.Context, socket, target string, lines int) (string, error) {
	return "ok", nil
}

func (f *fakeTmuxClient) PipePaneToFile(ctx context.Context, socket, target, filePath string) error {
	return nil
}

func (f *fakeTmuxClient) StopPipePane(ctx context.Context, socket, target string) error {
	return nil
}

func (f *fakeTmuxClient) ListSessions(ctx context.Context, socket string) (map[string]bool, error) {
	f.ensure()
	socket = strings.TrimSpace(socket)
	src := f.sessions
	if m := f.sessionsBySocket[socket]; m != nil {
		src = m
	}
	out := make(map[string]bool, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out, nil
}

func (f *fakeTmuxClient) ListPanes(ctx context.Context, socket, session string) ([]infra.PaneInfo, error) {
	return []infra.PaneInfo{
		{PaneID: "%1", CurrentCommand: "bash"},
	}, nil
}

func (f *fakeTmuxClient) ActivePane(ctx context.Context, socket, session string) (infra.PaneInfo, error) {
	return infra.PaneInfo{PaneID: "%1", CurrentCommand: "bash"}, nil
}

func (f *fakeTmuxClient) AttachCmd(socket, session string) *exec.Cmd {
	return exec.Command("true")
}

type fakeGitClient struct {
	addCalls       int
	currentBranch  string
	lastBaseBranch string
	pruneFn        func(repoRoot string) error
}

func (f *fakeGitClient) CurrentBranch(repoRoot string) (string, error) {
	cur := strings.TrimSpace(f.currentBranch)
	if cur == "" {
		cur = "main"
	}
	return cur, nil
}

func (f *fakeGitClient) AddWorktree(repoRoot, path, branch, baseBranch string) error {
	f.addCalls++
	f.lastBaseBranch = strings.TrimSpace(baseBranch)
	if err := os.MkdirAll(path, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(path, ".git"), []byte("gitdir: /tmp/fake\n"), 0o644)
}

func (f *fakeGitClient) RemoveWorktree(repoRoot, path string, force bool) error {
	return os.RemoveAll(path)
}

func (f *fakeGitClient) WorktreeDirty(path string) (bool, error) { return false, nil }

func (f *fakeGitClient) HasCommit(repoRoot string) (bool, error) { return true, nil }

func (f *fakeGitClient) WorktreeBranchCheckedOut(repoRoot, branch string) (bool, string, error) {
	return false, "", nil
}

func (f *fakeGitClient) PruneWorktrees(repoRoot string) error {
	if f.pruneFn != nil {
		return f.pruneFn(repoRoot)
	}
	return nil
}

func (f *fakeGitClient) IsWorktreeDir(path string) bool {
	st, err := os.Stat(filepath.Join(path, ".git"))
	return err == nil && !st.IsDir()
}

func newServiceForTest(t *testing.T) (*Service, *core.Project, *fakeTmuxClient, *fakeGitClient) {
	t.Helper()

	repoRoot := t.TempDir()
	layout := repo.NewLayout(repoRoot)
	worktreesDir := filepath.Join(t.TempDir(), "worktrees")
	if err := os.MkdirAll(worktreesDir, 0o755); err != nil {
		t.Fatalf("mkdir worktrees failed: %v", err)
	}

	// 控制面 seed（项目 kernel 等）
	if err := repo.EnsureControlPlaneSeed(layout, "demo"); err != nil {
		t.Fatalf("EnsureControlPlaneSeed failed: %v", err)
	}

	// DB
	if err := os.MkdirAll(filepath.Dir(layout.DBPath), 0o755); err != nil {
		t.Fatalf("mkdir runtime failed: %v", err)
	}
	db, err := store.OpenAndMigrate(layout.DBPath)
	if err != nil {
		t.Fatalf("OpenAndMigrate failed: %v", err)
	}

	// 测试环境默认不依赖本机 codex：
	// 注入一个确定性 PM agent 脚本，通过 pm_agent.command 覆盖二进制路径。
	testDispatch := filepath.Join(t.TempDir(), "test_pm_agent_dispatch")
	if err := os.WriteFile(testDispatch, []byte(testPMAgentDispatchScript()), 0o755); err != nil {
		t.Fatalf("write test pm agent dispatch failed: %v", err)
	}
	_ = os.Chmod(testDispatch, 0o755)
	testWorker := filepath.Join(t.TempDir(), "test_worker_agent")
	if err := os.WriteFile(testWorker, []byte(testWorkerAgentScript()), 0o755); err != nil {
		t.Fatalf("write test worker agent failed: %v", err)
	}
	_ = os.Chmod(testWorker, 0o755)

	cfg := repo.Config{
		TmuxSocket:        "dalek",
		BranchPrefix:      "",
		RefreshIntervalMS: 1000,
		WorkerAgent: repo.AgentExecConfig{
			Provider: "codex",
			Mode:     "sdk",
			Command:  testWorker,
		},
		PMAgent: repo.AgentExecConfig{
			Provider: "codex",
			Mode:     "cli",
			Command:  testDispatch,
		},
	}.WithDefaults()

	fTmux := &fakeTmuxClient{sessions: map[string]bool{}}
	fGit := &fakeGitClient{currentBranch: "main"}

	cp := &core.Project{
		Name:         "demo",
		Key:          "demo",
		RepoRoot:     repoRoot,
		Layout:       layout,
		ProjectDir:   layout.ProjectDir,
		ConfigPath:   layout.ConfigPath,
		DBPath:       layout.DBPath,
		WorktreesDir: worktreesDir,
		WorkersDir:   layout.RuntimeWorkersDir,
		Config:       cfg,
		DB:           db,
		Tmux:         fTmux,
		Git:          fGit,
		TaskRuntime:  tasksvc.NewRuntimeFactory(),
	}

	workerSvc := worker.New(cp)
	pmSvc := New(cp, workerSvc)
	return pmSvc, cp, fTmux, fGit
}

func createTicket(t *testing.T, db *gorm.DB, title string) store.Ticket {
	t.Helper()

	tk := store.Ticket{
		Title:          strings.TrimSpace(title),
		Description:    "test ticket description",
		WorkflowStatus: store.TicketBacklog,
	}
	if err := db.Create(&tk).Error; err != nil {
		t.Fatalf("create ticket failed: %v", err)
	}
	return tk
}

func testPMAgentDispatchScript() string {
	return `#!/usr/bin/env bash
set -euo pipefail

if [[ -n "${DALEK_TEST_PROMPT_PATH:-}" ]]; then
  prompt=""
  if [[ "$#" -gt 0 ]]; then
    prompt="${@: -1}"
  fi
  printf "%s\n" "$prompt" > "${DALEK_TEST_PROMPT_PATH}"
fi

echo "dispatch_done request_id=${DALEK_DISPATCH_REQUEST_ID:-}"
`
}

func testWorkerAgentScript() string {
	return `#!/usr/bin/env bash
set -euo pipefail
cat >/dev/null
echo '{"type":"thread.started","thread_id":"thread-worker-default"}'
echo '{"type":"item.completed","item":{"id":"msg-worker-default","type":"agent_message","text":"worker default ok"}}'
`
}
