package repo

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnsureWorktreeContract_OnlyEnsuresContractDir(t *testing.T) {
	root := t.TempDir()

	cp, err := EnsureWorktreeContract(root)
	if err != nil {
		t.Fatalf("EnsureWorktreeContract failed: %v", err)
	}

	if _, err := os.Stat(cp.Dir); err != nil {
		t.Fatalf("expected contract dir exists: %s err=%v", cp.Dir, err)
	}

	mustNotExist := []string{
		cp.AgentKernelMD,
		cp.StateJSON,
		filepath.Join(cp.Dir, "contract.json"),
		filepath.Join(cp.Dir, "requests"),
		filepath.Join(cp.Dir, "responses"),
	}
	for _, p := range mustNotExist {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Fatalf("path should not be created by contract ensure: %s err=%v", p, err)
		}
	}
}

func TestEnsureWorktreeContract_ConfiguresDalekGitProtectionForWorktree(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git 不可用，跳过真实 worktree 测试: %v", err)
	}

	repoRoot := t.TempDir()
	run := func(dir string, args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v failed: %v\n%s", args, err, string(out))
		}
		return strings.TrimSpace(string(out))
	}

	run(repoRoot, "init")
	run(repoRoot, "config", "user.email", "dalek-test@example.com")
	run(repoRoot, "config", "user.name", "dalek-test")
	if err := os.WriteFile(filepath.Join(repoRoot, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write README failed: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(repoRoot, ".dalek", "control"), 0o755); err != nil {
		t.Fatalf("mkdir tracked .dalek/control failed: %v", err)
	}
	trackedDalekFile := filepath.Join(repoRoot, ".dalek", "control", "skill.txt")
	if err := os.WriteFile(trackedDalekFile, []byte("tracked\n"), 0o644); err != nil {
		t.Fatalf("write tracked .dalek file failed: %v", err)
	}
	run(repoRoot, "add", "README.md", ".dalek/control/skill.txt")
	run(repoRoot, "commit", "-m", "init")

	worktreeRoot := filepath.Join(t.TempDir(), "wt")
	run(repoRoot, "worktree", "add", "-b", "ts/test-worktree-protection", worktreeRoot, "HEAD")

	if _, err := EnsureWorktreeContract(worktreeRoot); err != nil {
		t.Fatalf("EnsureWorktreeContract failed: %v", err)
	}

	excludePath := run(worktreeRoot, "rev-parse", "--git-path", "info/exclude")
	rawExclude, err := os.ReadFile(excludePath)
	if err != nil {
		t.Fatalf("read info/exclude failed: %v", err)
	}
	if !strings.Contains(string(rawExclude), ".dalek/") {
		t.Fatalf("expected .git/info/exclude contains .dalek/, got:\n%s", string(rawExclude))
	}

	if err := os.WriteFile(filepath.Join(worktreeRoot, ".dalek", "control", "skill.txt"), []byte("changed in worktree\n"), 0o644); err != nil {
		t.Fatalf("modify tracked .dalek file in worktree failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(worktreeRoot, ".dalek", "state.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write untracked .dalek/state.json failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(worktreeRoot, "feature.txt"), []byte("feature\n"), 0o644); err != nil {
		t.Fatalf("write feature.txt failed: %v", err)
	}

	status := run(worktreeRoot, "status", "--porcelain")
	if strings.Contains(status, ".dalek") {
		t.Fatalf("expected worktree status ignore .dalek changes, got:\n%s", status)
	}
	if !strings.Contains(status, "feature.txt") {
		t.Fatalf("expected status still reports product file changes, got:\n%s", status)
	}

	lsFiles := run(worktreeRoot, "ls-files", "-v", "--", ".dalek/control/skill.txt")
	if !strings.HasPrefix(lsFiles, "S ") {
		t.Fatalf("expected tracked .dalek file marked skip-worktree, got=%q", lsFiles)
	}
}
