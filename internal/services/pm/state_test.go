package pm

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"dalek/internal/infra"
	"dalek/internal/repo"
	"dalek/internal/services/core"
	tasksvc "dalek/internal/services/task"
	ticketsvc "dalek/internal/services/ticket"
	workersvc "dalek/internal/services/worker"
	"dalek/internal/store"
)

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

	p, err := core.NewProject(core.NewProjectInput{
		Name:          "demo",
		Key:           "demo",
		RepoRoot:      repoRoot,
		Layout:        layout,
		WorktreesDir:  filepath.Join(t.TempDir(), "worktrees"),
		WorkersDir:    layout.RuntimeWorkersDir,
		Config:        repo.Config{},
		DB:            db,
		Logger:        core.DiscardLogger(),
		WorkerRuntime: infra.NewDaemonProcessManager(),
		Git:           noopGit{},
		TaskRuntime:   tasksvc.NewRuntimeFactory(),
	})
	if err != nil {
		t.Fatalf("NewProject failed: %v", err)
	}

	workerSvc := workersvc.New(p, ticketsvc.New(p.DB))
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

func TestPM_StatePlannerFields_DefaultZeroAndRoundTrip(t *testing.T) {
	pmSvc, p := newPMServiceForTest(t)
	ctx := context.Background()

	st, err := pmSvc.GetState(ctx)
	if err != nil {
		t.Fatalf("GetState failed: %v", err)
	}
	if st.PlannerDirty {
		t.Fatalf("planner_dirty should default false")
	}
	if st.PlannerWakeVersion != 0 {
		t.Fatalf("planner_wake_version should default 0, got=%d", st.PlannerWakeVersion)
	}
	if st.PlannerActiveTaskRunID != nil {
		t.Fatalf("planner_active_task_run_id should default nil")
	}
	if st.PlannerCooldownUntil != nil {
		t.Fatalf("planner_cooldown_until should default nil")
	}
	if st.PlannerLastError != "" {
		t.Fatalf("planner_last_error should default empty, got=%q", st.PlannerLastError)
	}
	if st.PlannerLastRunAt != nil {
		t.Fatalf("planner_last_run_at should default nil")
	}

	now := time.Now().UTC().Truncate(time.Second)
	cooldown := now.Add(10 * time.Minute)
	activeRunID := uint(42)
	if err := p.DB.WithContext(ctx).Model(&store.PMState{}).Where("id = ?", st.ID).Updates(map[string]any{
		"planner_dirty":              true,
		"planner_wake_version":       7,
		"planner_active_task_run_id": activeRunID,
		"planner_cooldown_until":     &cooldown,
		"planner_last_error":         "planner failed once",
		"planner_last_run_at":        &now,
	}).Error; err != nil {
		t.Fatalf("update planner fields failed: %v", err)
	}

	got, err := pmSvc.GetState(ctx)
	if err != nil {
		t.Fatalf("GetState(2) failed: %v", err)
	}
	if !got.PlannerDirty {
		t.Fatalf("planner_dirty should persist true")
	}
	if got.PlannerWakeVersion != 7 {
		t.Fatalf("planner_wake_version should persist, got=%d", got.PlannerWakeVersion)
	}
	if got.PlannerActiveTaskRunID == nil || *got.PlannerActiveTaskRunID != activeRunID {
		t.Fatalf("planner_active_task_run_id mismatch: got=%v want=%d", got.PlannerActiveTaskRunID, activeRunID)
	}
	if got.PlannerCooldownUntil == nil || got.PlannerCooldownUntil.UTC().Unix() != cooldown.Unix() {
		t.Fatalf("planner_cooldown_until mismatch: got=%v want=%v", got.PlannerCooldownUntil, cooldown)
	}
	if got.PlannerLastError != "planner failed once" {
		t.Fatalf("planner_last_error mismatch: got=%q", got.PlannerLastError)
	}
	if got.PlannerLastRunAt == nil || got.PlannerLastRunAt.UTC().Unix() != now.Unix() {
		t.Fatalf("planner_last_run_at mismatch: got=%v want=%v", got.PlannerLastRunAt, now)
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
