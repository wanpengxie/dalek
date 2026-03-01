package main

import (
	"encoding/json"
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
