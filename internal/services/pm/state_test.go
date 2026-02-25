package pm

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"dalek/internal/infra"
	"dalek/internal/repo"
	"dalek/internal/services/core"
	tasksvc "dalek/internal/services/task"
	workersvc "dalek/internal/services/worker"
	"dalek/internal/store"
)

type noopTmux struct{}

func (noopTmux) NewSession(ctx context.Context, socket, name, startDir string) error { return nil }
func (noopTmux) NewSessionWithCommand(ctx context.Context, socket, name, startDir string, cmd []string) error {
	return nil
}
func (noopTmux) KillSession(ctx context.Context, socket, name string) error { return nil }
func (noopTmux) KillServer(ctx context.Context, socket string) error        { return nil }
func (noopTmux) SendKeys(ctx context.Context, socket, target, keys string) error {
	return nil
}
func (noopTmux) SendKeysLiteral(ctx context.Context, socket, target, text string) error { return nil }
func (noopTmux) SendLine(ctx context.Context, socket, target, line string) error        { return nil }
func (noopTmux) CapturePane(ctx context.Context, socket, target string, lines int) (string, error) {
	return "", nil
}
func (noopTmux) PipePaneToFile(ctx context.Context, socket, target, filePath string) error {
	return nil
}
func (noopTmux) StopPipePane(ctx context.Context, socket, target string) error { return nil }
func (noopTmux) ListSessions(ctx context.Context, socket string) (map[string]bool, error) {
	return map[string]bool{}, nil
}
func (noopTmux) ListPanes(ctx context.Context, socket, session string) ([]infra.PaneInfo, error) {
	return []infra.PaneInfo{{PaneID: "%1", CurrentCommand: "bash"}}, nil
}
func (noopTmux) ActivePane(ctx context.Context, socket, session string) (infra.PaneInfo, error) {
	return infra.PaneInfo{PaneID: "%1", CurrentCommand: "bash"}, nil
}
func (noopTmux) AttachCmd(socket, session string) *exec.Cmd { return exec.Command("true") }

type noopGit struct{}

func (noopGit) CurrentBranch(repoRoot string) (string, error) { return "main", nil }
func (noopGit) AddWorktree(repoRoot, path, branch, baseBranch string) error {
	return os.MkdirAll(path, 0o755)
}
func (noopGit) RemoveWorktree(repoRoot, path string, force bool) error { return os.RemoveAll(path) }
func (noopGit) WorktreeDirty(path string) (bool, error)                { return false, nil }
func (noopGit) HasCommit(repoRoot string) (bool, error)                { return true, nil }
func (noopGit) WorktreeBranchCheckedOut(repoRoot, branch string) (bool, string, error) {
	return false, "", nil
}
func (noopGit) PruneWorktrees(repoRoot string) error { return nil }
func (noopGit) IsWorktreeDir(path string) bool       { return true }

func newPMServiceForTest(t *testing.T) (*Service, *core.Project) {
	t.Helper()
	repoRoot := t.TempDir()
	layout := repo.NewLayout(repoRoot)
	if err := os.MkdirAll(layout.RuntimeDir, 0o755); err != nil {
		t.Fatalf("mkdir runtime failed: %v", err)
	}
	if err := os.MkdirAll(layout.RuntimeWorkersDir, 0o755); err != nil {
		t.Fatalf("mkdir workers failed: %v", err)
	}

	db, err := store.OpenAndMigrate(layout.DBPath)
	if err != nil {
		t.Fatalf("OpenAndMigrate failed: %v", err)
	}

	p := &core.Project{
		Name:         "demo",
		Key:          "demo",
		RepoRoot:     repoRoot,
		Layout:       layout,
		ProjectDir:   layout.ProjectDir,
		ConfigPath:   filepath.Join(layout.ProjectDir, "config.json"),
		DBPath:       layout.DBPath,
		WorktreesDir: filepath.Join(t.TempDir(), "worktrees"),
		WorkersDir:   layout.RuntimeWorkersDir,
		Config:       repo.Config{TmuxSocket: "dalek"},
		DB:           db,
		Tmux:         noopTmux{},
		Git:          noopGit{},
		TaskRuntime:  tasksvc.NewRuntimeFactory(),
	}

	workerSvc := workersvc.New(p)
	pmSvc := New(p, workerSvc)
	return pmSvc, p
}

func TestPM_StateAndSettings(t *testing.T) {
	pmSvc, _ := newPMServiceForTest(t)
	ctx := context.Background()

	st, err := pmSvc.GetState(ctx)
	if err != nil {
		t.Fatalf("GetState failed: %v", err)
	}
	if !st.AutopilotEnabled || st.MaxRunningWorkers != 3 {
		t.Fatalf("unexpected default state: %+v", st)
	}

	if _, err := pmSvc.SetAutopilotEnabled(ctx, false); err != nil {
		t.Fatalf("SetAutopilotEnabled(false) failed: %v", err)
	}
	if _, err := pmSvc.SetMaxRunningWorkers(ctx, 5); err != nil {
		t.Fatalf("SetMaxRunningWorkers(5) failed: %v", err)
	}

	st, err = pmSvc.GetState(ctx)
	if err != nil {
		t.Fatalf("GetState failed: %v", err)
	}
	if st.AutopilotEnabled {
		t.Fatalf("expected autopilot disabled")
	}
	if st.MaxRunningWorkers != 5 {
		t.Fatalf("expected max_running=5, got %d", st.MaxRunningWorkers)
	}
}

func TestManagerDispatchTimeout_UsesConfig(t *testing.T) {
	pmSvc, p := newPMServiceForTest(t)
	p.Config.PMDispatchTimeoutMS = 123456

	got := pmSvc.managerDispatchTimeout()
	want := 123456 * time.Millisecond
	if got != want {
		t.Fatalf("unexpected manager dispatch timeout: got=%v want=%v", got, want)
	}
}

func TestManagerStartTimeout_CoversBootstrap(t *testing.T) {
	pmSvc, _ := newPMServiceForTest(t)

	got := pmSvc.managerStartTimeout()
	if got < 5*time.Minute {
		t.Fatalf("manager start timeout must cover bootstrap timeout: got=%v", got)
	}
}
