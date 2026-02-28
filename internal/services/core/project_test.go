package core

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
	"dalek/internal/store"

	"gorm.io/gorm"
)

type noopGitClient struct{}

func (noopGitClient) CurrentBranch(repoRoot string) (string, error) { return "main", nil }
func (noopGitClient) AddWorktree(repoRoot, path, branch, baseBranch string) error {
	return os.MkdirAll(path, 0o755)
}
func (noopGitClient) RemoveWorktree(repoRoot, path string, force bool) error {
	return os.RemoveAll(path)
}
func (noopGitClient) WorktreeDirty(path string) (bool, error) { return false, nil }
func (noopGitClient) HasCommit(repoRoot string) (bool, error) { return true, nil }
func (noopGitClient) WorktreeBranchCheckedOut(repoRoot, branch string) (bool, string, error) {
	return false, "", nil
}
func (noopGitClient) PruneWorktrees(repoRoot string) error { return nil }
func (noopGitClient) IsWorktreeDir(path string) bool       { return true }

type noopTaskRuntimeFactory struct{}

func (noopTaskRuntimeFactory) ForDB(db *gorm.DB) TaskRuntime {
	return nil
}

type noopWorkerRuntime struct{}

func (noopWorkerRuntime) StartProcess(ctx context.Context, spec infra.WorkerProcessSpec) (infra.WorkerProcessHandle, error) {
	return infra.WorkerProcessHandle{}, nil
}
func (noopWorkerRuntime) StopProcess(ctx context.Context, handle infra.WorkerProcessHandle, timeout time.Duration) error {
	return nil
}
func (noopWorkerRuntime) InterruptProcess(ctx context.Context, handle infra.WorkerProcessHandle) error {
	return nil
}
func (noopWorkerRuntime) IsAlive(ctx context.Context, handle infra.WorkerProcessHandle) (bool, error) {
	return false, nil
}
func (noopWorkerRuntime) CaptureOutput(ctx context.Context, handle infra.WorkerProcessHandle, lines int) (string, error) {
	return "", nil
}
func (noopWorkerRuntime) AttachCmd(handle infra.WorkerProcessHandle) *exec.Cmd {
	return exec.Command("true")
}

func TestNewProject_SuccessAndPathHelpers(t *testing.T) {
	input := newValidProjectInput(t)
	p, err := NewProject(input)
	if err != nil {
		t.Fatalf("NewProject failed: %v", err)
	}
	if got := p.ProjectDir(); strings.TrimSpace(got) != strings.TrimSpace(input.Layout.ProjectDir) {
		t.Fatalf("ProjectDir mismatch: got=%q want=%q", got, input.Layout.ProjectDir)
	}
	if got := p.ConfigPath(); strings.TrimSpace(got) != strings.TrimSpace(input.Layout.ConfigPath) {
		t.Fatalf("ConfigPath mismatch: got=%q want=%q", got, input.Layout.ConfigPath)
	}
	if got := p.DBPath(); strings.TrimSpace(got) != strings.TrimSpace(input.Layout.DBPath) {
		t.Fatalf("DBPath mismatch: got=%q want=%q", got, input.Layout.DBPath)
	}
}

func TestNewProject_ValidateRequiredFields(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*NewProjectInput)
		want   string
	}{
		{
			name: "missing_name",
			mutate: func(in *NewProjectInput) {
				in.Name = " "
			},
			want: "name",
		},
		{
			name: "missing_key",
			mutate: func(in *NewProjectInput) {
				in.Key = ""
			},
			want: "key",
		},
		{
			name: "missing_layout",
			mutate: func(in *NewProjectInput) {
				in.Layout = repo.Layout{}
				in.RepoRoot = ""
			},
			want: "repo_root",
		},
		{
			name: "missing_logger",
			mutate: func(in *NewProjectInput) {
				in.Logger = nil
			},
			want: "Logger",
		},
		{
			name: "missing_worker_runtime",
			mutate: func(in *NewProjectInput) {
				in.WorkerRuntime = nil
			},
			want: "WorkerRuntime",
		},
		{
			name: "missing_git",
			mutate: func(in *NewProjectInput) {
				in.Git = nil
			},
			want: "Git",
		},
		{
			name: "missing_runtime",
			mutate: func(in *NewProjectInput) {
				in.TaskRuntime = nil
			},
			want: "TaskRuntime",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			in := newValidProjectInput(t)
			tc.mutate(&in)
			_, err := NewProject(in)
			if err == nil {
				t.Fatalf("NewProject should fail")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("unexpected error: got=%v want_contains=%q", err, tc.want)
			}
		})
	}
}

func TestProjectPathHelpers_DeriveFromRepoRoot(t *testing.T) {
	repoRoot := t.TempDir()
	p := &Project{RepoRoot: repoRoot}
	layout := repo.NewLayout(repoRoot)
	if got := p.ProjectDir(); strings.TrimSpace(got) != strings.TrimSpace(layout.ProjectDir) {
		t.Fatalf("ProjectDir mismatch: got=%q want=%q", got, layout.ProjectDir)
	}
	if got := p.ConfigPath(); strings.TrimSpace(got) != strings.TrimSpace(layout.ConfigPath) {
		t.Fatalf("ConfigPath mismatch: got=%q want=%q", got, layout.ConfigPath)
	}
	if got := p.DBPath(); strings.TrimSpace(got) != strings.TrimSpace(layout.DBPath) {
		t.Fatalf("DBPath mismatch: got=%q want=%q", got, layout.DBPath)
	}
}

func newValidProjectInput(t *testing.T) NewProjectInput {
	t.Helper()
	repoRoot := t.TempDir()
	layout := repo.NewLayout(repoRoot)
	if err := os.MkdirAll(filepath.Dir(layout.DBPath), 0o755); err != nil {
		t.Fatalf("mkdir runtime failed: %v", err)
	}
	db, err := store.OpenAndMigrate(layout.DBPath)
	if err != nil {
		t.Fatalf("OpenAndMigrate failed: %v", err)
	}
	return NewProjectInput{
		Name:          "demo",
		Key:           "demo",
		RepoRoot:      repoRoot,
		Layout:        layout,
		WorktreesDir:  filepath.Join(t.TempDir(), "worktrees"),
		WorkersDir:    layout.RuntimeWorkersDir,
		Config:        repo.Config{}.WithDefaults(),
		DB:            db,
		Logger:        DiscardLogger(),
		WorkerRuntime: noopWorkerRuntime{},
		Git:           noopGitClient{},
		TaskRuntime:   noopTaskRuntimeFactory{},
	}
}
