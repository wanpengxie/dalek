package repo

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnsureRepoAgentEntryPoints_LinkingAndRef(t *testing.T) {
	repoRoot := t.TempDir()
	ref := "@.dalek/AGENTS.md"

	// both missing: create AGENTS.md + link CLAUDE.md -> AGENTS.md
	if err := EnsureRepoAgentEntryPoints(repoRoot); err != nil {
		t.Fatalf("EnsureRepoAgentEntryPoints failed: %v", err)
	}

	agentsPath := filepath.Join(repoRoot, "AGENTS.md")
	claudePath := filepath.Join(repoRoot, "CLAUDE.md")

	if st, err := os.Lstat(agentsPath); err != nil {
		t.Fatalf("stat AGENTS.md failed: %v", err)
	} else if st.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("expected AGENTS.md not symlink")
	}
	if st, err := os.Lstat(claudePath); err != nil {
		t.Fatalf("stat CLAUDE.md failed: %v", err)
	} else if st.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("expected CLAUDE.md symlink -> AGENTS.md")
	}
	if target, err := os.Readlink(claudePath); err != nil {
		t.Fatalf("readlink CLAUDE.md failed: %v", err)
	} else if strings.TrimSpace(target) != "AGENTS.md" {
		t.Fatalf("unexpected CLAUDE.md link target: %q", target)
	}
	if b, err := os.ReadFile(agentsPath); err != nil {
		t.Fatalf("read AGENTS.md failed: %v", err)
	} else if !strings.Contains(string(b), ref) {
		t.Fatalf("AGENTS.md should include ref line %q", ref)
	} else if !strings.Contains(string(b), injectBlockBegin) || !strings.Contains(string(b), injectBlockEnd) {
		t.Fatalf("AGENTS.md should include dalek inject block")
	} else if strings.Count(string(b), injectBlockBegin) != 1 || strings.Count(string(b), injectBlockEnd) != 1 {
		t.Fatalf("AGENTS.md inject block should appear exactly once")
	}
}

func TestEnsureRepoAgentEntryPoints_PreserveUserContentAndInjectIdempotent(t *testing.T) {
	repoRoot := t.TempDir()
	agentsPath := filepath.Join(repoRoot, "AGENTS.md")
	claudePath := filepath.Join(repoRoot, "CLAUDE.md")

	customAgents := "# 用户 AGENTS\n\n这里是用户自定义内容。\n"
	customClaude := "# 用户 CLAUDE\n\n请保持这段不变。\n"
	if err := os.WriteFile(agentsPath, []byte(customAgents), 0o644); err != nil {
		t.Fatalf("write AGENTS.md failed: %v", err)
	}
	if err := os.WriteFile(claudePath, []byte(customClaude), 0o644); err != nil {
		t.Fatalf("write CLAUDE.md failed: %v", err)
	}

	if err := EnsureRepoAgentEntryPoints(repoRoot); err != nil {
		t.Fatalf("EnsureRepoAgentEntryPoints first run failed: %v", err)
	}
	if err := EnsureRepoAgentEntryPoints(repoRoot); err != nil {
		t.Fatalf("EnsureRepoAgentEntryPoints second run failed: %v", err)
	}

	assertInjected := func(path string, original string) {
		t.Helper()
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s failed: %v", path, err)
		}
		s := string(b)
		if !strings.Contains(s, strings.TrimSpace(original)) {
			t.Fatalf("%s should preserve original user content", path)
		}
		if !strings.Contains(s, agentsRefLine) {
			t.Fatalf("%s should include %q", path, agentsRefLine)
		}
		if strings.Count(s, injectBlockBegin) != 1 || strings.Count(s, injectBlockEnd) != 1 {
			t.Fatalf("%s inject block should appear exactly once", path)
		}
		if !strings.Contains(s, `<dalek_bootstrap PRIORITY="HIGHEST" override="true">`) {
			t.Fatalf("%s should include injected bootstrap block", path)
		}
		blockPos := strings.Index(s, injectBlockBegin)
		origPos := strings.Index(s, strings.TrimSpace(original))
		if blockPos < 0 || origPos < 0 {
			t.Fatalf("%s should contain both inject block and original content", path)
		}
		if blockPos > origPos {
			t.Fatalf("%s inject block should be at file head before user content", path)
		}
	}

	assertInjected(agentsPath, customAgents)
	assertInjected(claudePath, customClaude)
}

func TestEnsureRepoAgentEntryPointsVersioned_AddAndCommit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git 不可用，跳过测试: %v", err)
	}
	repoRoot := t.TempDir()
	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", repoRoot}, args...)...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, string(out))
		}
		return string(out)
	}

	run("init")
	run("config", "user.email", "dalek-test@example.com")
	run("config", "user.name", "dalek-test")
	if err := os.WriteFile(filepath.Join(repoRoot, "README.md"), []byte("init\n"), 0o644); err != nil {
		t.Fatalf("write README failed: %v", err)
	}
	run("add", "README.md")
	run("commit", "-m", "init")
	if err := os.WriteFile(filepath.Join(repoRoot, ".gitignore"), []byte("AGENTS.md\nCLAUDE.md\n"), 0o644); err != nil {
		t.Fatalf("write .gitignore failed: %v", err)
	}
	run("add", ".gitignore")
	run("commit", "-m", "ignore entrypoints")

	if err := EnsureRepoAgentEntryPoints(repoRoot); err != nil {
		t.Fatalf("EnsureRepoAgentEntryPoints failed: %v", err)
	}
	if err := EnsureRepoAgentEntryPointsVersioned(repoRoot); err != nil {
		t.Fatalf("EnsureRepoAgentEntryPointsVersioned failed: %v", err)
	}

	run("ls-files", "--error-unmatch", "AGENTS.md")
	run("ls-files", "--error-unmatch", "CLAUDE.md")

	status := run("status", "--porcelain", "--", "AGENTS.md", "CLAUDE.md")
	if strings.TrimSpace(status) != "" {
		t.Fatalf("expected clean status for entry files, got:\n%s", status)
	}
}
