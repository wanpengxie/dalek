package worker

import (
	"context"
	"dalek/internal/contracts"
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

	"gorm.io/gorm"
)

type fakeTmuxClient struct {
	sessions         map[string]bool
	sessionsBySocket map[string]map[string]bool
	listErrBySocket  map[string]error
	newSessionCalls  int
	killSessionCalls int
	newSessionErr    error
	killSessionErr   error
}

func (f *fakeTmuxClient) ensure() {
	if f.sessions == nil {
		f.sessions = map[string]bool{}
	}
	if f.sessionsBySocket == nil {
		f.sessionsBySocket = map[string]map[string]bool{}
	}
	if f.listErrBySocket == nil {
		f.listErrBySocket = map[string]error{}
	}
}

func (f *fakeTmuxClient) NewSession(ctx context.Context, socket, name, startDir string) error {
	f.ensure()
	name = strings.TrimSpace(name)
	if f.newSessionErr != nil {
		return f.newSessionErr
	}
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
	if f.killSessionErr != nil {
		return f.killSessionErr
	}
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

func (f *fakeTmuxClient) SendKeys(ctx context.Context, socket, target, keys string) error { return nil }

func (f *fakeTmuxClient) SendKeysLiteral(ctx context.Context, socket, target, text string) error {
	return nil
}

func (f *fakeTmuxClient) SendLine(ctx context.Context, socket, target, line string) error { return nil }

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
	if err := f.listErrBySocket[socket]; err != nil {
		return nil, err
	}
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
	addErr         error
	afterAdd       func(path string) error
	removeErr      error
	removeCalls    int
	removedPaths   []string
}

func (f *fakeGitClient) CurrentBranch(repoRoot string) (string, error) {
	cur := strings.TrimSpace(f.currentBranch)
	if cur == "" {
		cur = "main"
	}
	return cur, nil
}

func (f *fakeGitClient) AddWorktree(repoRoot, path, branch, baseBranch string) error {
	if f.addErr != nil {
		return f.addErr
	}
	f.addCalls++
	f.lastBaseBranch = strings.TrimSpace(baseBranch)
	if err := os.MkdirAll(path, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(path, ".git"), []byte("gitdir: /tmp/fake\n"), 0o644); err != nil {
		return err
	}
	if f.afterAdd != nil {
		return f.afterAdd(path)
	}
	return nil
}

func (f *fakeGitClient) RemoveWorktree(repoRoot, path string, force bool) error {
	f.removeCalls++
	f.removedPaths = append(f.removedPaths, strings.TrimSpace(path))
	if f.removeErr != nil {
		return f.removeErr
	}
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

	// 控制面 seed（项目 kernel 等）；worker service 的 StartTicket 依赖它。
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
	dispatchScript := `#!/usr/bin/env bash
set -euo pipefail
echo "dispatch_done request_id=${DALEK_DISPATCH_REQUEST_ID:-}"
`
	if err := os.WriteFile(testDispatch, []byte(dispatchScript), 0o755); err != nil {
		t.Fatalf("write test pm agent dispatch failed: %v", err)
	}
	_ = os.Chmod(testDispatch, 0o755)

	cfg := repo.Config{
		TmuxSocket:        "dalek",
		BranchPrefix:      "",
		RefreshIntervalMS: 1000,
		WorkerAgent: repo.AgentExecConfig{
			Provider: "codex",
			Mode:     "cli",
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
	return New(cp), cp, fTmux, fGit
}

func createTicket(t *testing.T, db *gorm.DB, title string) store.Ticket {
	t.Helper()

	tk := store.Ticket{Title: strings.TrimSpace(title), Description: "", WorkflowStatus: contracts.TicketBacklog}
	if err := db.Create(&tk).Error; err != nil {
		t.Fatalf("create ticket failed: %v", err)
	}
	return tk
}
