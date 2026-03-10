package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCLI_VersionCommand(t *testing.T) {
	bin := buildCLIBinary(t)
	repo := initGitRepo(t)

	stdout, stderr, err := runCLI(t, bin, repo, "version")
	if err != nil {
		t.Fatalf("version command failed: err=%v stderr=%s", err, stderr)
	}
	if strings.TrimSpace(stdout) == "" {
		t.Fatalf("version output should not be empty")
	}
}

func TestCLI_UpgradeDryRunJSON(t *testing.T) {
	bin := buildCLIBinary(t)
	repo := initGitRepo(t)
	home := t.TempDir()

	_, _ = runCLIOK(t, bin, repo, "-home", home, "init", "-name", "demo")
	stdout, stderr, err := runCLI(
		t,
		bin,
		repo,
		"-home", home,
		"-project", "demo",
		"upgrade",
		"--dry-run",
		"-o", "json",
	)
	if err != nil {
		t.Fatalf("upgrade dry-run failed: err=%v stderr=%s", err, stderr)
	}
	var payload struct {
		Schema  string `json:"schema"`
		Project string `json:"project"`
		DryRun  bool   `json:"dry_run"`
	}
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("decode upgrade json failed: %v raw=%s", err, stdout)
	}
	if payload.Schema != "dalek.upgrade.v1" {
		t.Fatalf("unexpected schema: %q", payload.Schema)
	}
	if payload.Project != "demo" {
		t.Fatalf("unexpected project: %q", payload.Project)
	}
	if !payload.DryRun {
		t.Fatalf("dry_run should be true")
	}
}

func TestCLI_InitAndUpgradeInstallReferenceTransactionHook(t *testing.T) {
	bin := buildCLIBinary(t)
	repo := initGitRepo(t)
	home := t.TempDir()

	_, _ = runCLIOK(t, bin, repo, "-home", home, "init", "-name", "demo")
	hookPath := filepath.Join(repo, ".git", "hooks", "reference-transaction")
	content, err := os.ReadFile(hookPath)
	if err != nil {
		t.Fatalf("init should install reference-transaction hook: %v", err)
	}
	if !strings.Contains(string(content), "dalek merge sync-ref") {
		t.Fatalf("hook content should call dalek merge sync-ref:\n%s", string(content))
	}

	if err := os.Remove(hookPath); err != nil {
		t.Fatalf("remove hook before upgrade failed: %v", err)
	}
	_, _ = runCLIOK(t, bin, repo, "-home", home, "-project", "demo", "upgrade")
	content, err = os.ReadFile(hookPath)
	if err != nil {
		t.Fatalf("upgrade should (re)install reference-transaction hook: %v", err)
	}
	if !strings.Contains(string(content), "dalek merge sync-ref") {
		t.Fatalf("upgraded hook content should call dalek merge sync-ref:\n%s", string(content))
	}
}
