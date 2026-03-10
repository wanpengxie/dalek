package repo

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnsureReferenceTransactionHook_Create(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git 不可用，跳过测试: %v", err)
	}
	repoRoot := initGitRepoForHookTest(t)

	res, err := EnsureReferenceTransactionHook(repoRoot, "/tmp/dalek home", "demo")
	if err != nil {
		t.Fatalf("EnsureReferenceTransactionHook failed: %v", err)
	}
	if !res.Installed || res.Skipped {
		t.Fatalf("expected hook installed, got %+v", res)
	}
	if strings.TrimSpace(res.Path) == "" {
		t.Fatalf("hook path should not be empty")
	}
	content, err := os.ReadFile(res.Path)
	if err != nil {
		t.Fatalf("read hook failed: %v", err)
	}
	text := string(content)
	if !strings.Contains(text, referenceTransactionHookMarkerHead) || !strings.Contains(text, referenceTransactionHookMarkerTail) {
		t.Fatalf("hook should include dalek marker block:\n%s", text)
	}
	if !strings.Contains(text, "dalek merge sync-ref --home '/tmp/dalek home' --project 'demo' --ref \"$ref_name\" --old \"$old_sha\" --new \"$new_sha\"") {
		t.Fatalf("hook should include sync-ref command with home/project, got:\n%s", text)
	}
	if st, err := os.Stat(res.Path); err != nil {
		t.Fatalf("stat hook failed: %v", err)
	} else if st.Mode()&0o111 == 0 {
		t.Fatalf("hook should be executable, mode=%#o", st.Mode().Perm())
	}
}

func TestEnsureReferenceTransactionHook_UpdateManagedBlock(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git 不可用，跳过测试: %v", err)
	}
	repoRoot := initGitRepoForHookTest(t)
	hookPath := filepath.Join(repoRoot, ".git", "hooks", referenceTransactionHookName)
	existing := strings.Join([]string{
		"#!/usr/bin/env bash",
		"echo user-before",
		referenceTransactionHookMarkerHead,
		"echo old-hook-body",
		referenceTransactionHookMarkerTail,
		"echo user-after",
		"",
	}, "\n")
	if err := os.MkdirAll(filepath.Dir(hookPath), 0o755); err != nil {
		t.Fatalf("mkdir hooks dir failed: %v", err)
	}
	if err := os.WriteFile(hookPath, []byte(existing), 0o755); err != nil {
		t.Fatalf("write existing hook failed: %v", err)
	}

	res, err := EnsureReferenceTransactionHook(repoRoot, "/tmp/new-home", "demo2")
	if err != nil {
		t.Fatalf("EnsureReferenceTransactionHook update failed: %v", err)
	}
	if !res.Installed || res.Skipped {
		t.Fatalf("expected managed hook updated, got %+v", res)
	}
	content, err := os.ReadFile(hookPath)
	if err != nil {
		t.Fatalf("read hook failed: %v", err)
	}
	text := string(content)
	if !strings.Contains(text, "echo user-before") || !strings.Contains(text, "echo user-after") {
		t.Fatalf("update should preserve non-dalek content:\n%s", text)
	}
	if !strings.Contains(text, "dalek merge sync-ref --home '/tmp/new-home' --project 'demo2' --ref \"$ref_name\" --old \"$old_sha\" --new \"$new_sha\"") {
		t.Fatalf("managed block should be refreshed:\n%s", text)
	}
}

func TestEnsureReferenceTransactionHook_SkipNonDalekHook(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git 不可用，跳过测试: %v", err)
	}
	repoRoot := initGitRepoForHookTest(t)
	hookPath := filepath.Join(repoRoot, ".git", "hooks", referenceTransactionHookName)
	original := "#!/usr/bin/env bash\necho custom-hook\n"
	if err := os.MkdirAll(filepath.Dir(hookPath), 0o755); err != nil {
		t.Fatalf("mkdir hooks dir failed: %v", err)
	}
	if err := os.WriteFile(hookPath, []byte(original), 0o755); err != nil {
		t.Fatalf("write custom hook failed: %v", err)
	}

	res, err := EnsureReferenceTransactionHook(repoRoot, "/tmp/new-home", "demo")
	if err != nil {
		t.Fatalf("EnsureReferenceTransactionHook should not fail on custom hook: %v", err)
	}
	if !res.Skipped || res.Installed {
		t.Fatalf("expected skipped custom hook, got %+v", res)
	}
	if strings.TrimSpace(res.Warning) == "" {
		t.Fatalf("expected warning when skip custom hook")
	}
	content, err := os.ReadFile(hookPath)
	if err != nil {
		t.Fatalf("read hook failed: %v", err)
	}
	if string(content) != original {
		t.Fatalf("custom hook should stay untouched")
	}
}

func initGitRepoForHookTest(t *testing.T) string {
	t.Helper()
	repoRoot := t.TempDir()
	runGitForHookTest(t, repoRoot, "init")
	runGitForHookTest(t, repoRoot, "config", "user.email", "hook-test@example.com")
	runGitForHookTest(t, repoRoot, "config", "user.name", "hook-test")
	if err := os.WriteFile(filepath.Join(repoRoot, "README.md"), []byte("hook test\n"), 0o644); err != nil {
		t.Fatalf("write README failed: %v", err)
	}
	runGitForHookTest(t, repoRoot, "add", "README.md")
	runGitForHookTest(t, repoRoot, "commit", "-m", "init")
	return repoRoot
}

func runGitForHookTest(t *testing.T, repoRoot string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", repoRoot}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, string(out))
	}
	return string(out)
}
