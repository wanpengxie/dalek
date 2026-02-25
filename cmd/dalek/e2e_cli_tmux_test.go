package main

import (
	"os"
	"strings"
	"testing"

	"dalek/internal/testutil"
)

func TestCLI_E2E_StartAndStopWithTmuxShim(t *testing.T) {
	// 让 CLI 子进程走 tmux shim，不依赖真实 tmux server。
	shimDir, statePath := testutil.InstallTmuxShim(t)
	t.Setenv("DALEK_TEST_TMUX_STATE", statePath)
	t.Setenv("PATH", shimDir+":"+os.Getenv("PATH"))

	bin := buildCLIBinary(t)
	repo := initGitRepo(t)
	home := t.TempDir()

	_, _ = runCLIOK(t, bin, repo, "-home", home, "init", "-name", "demo")
	out, _ := runCLIOK(t, bin, repo, "-home", home, "-project", "demo", "ticket", "create", "-title", "tmux flow", "-desc", "tmux flow description")
	if strings.TrimSpace(out) != "1" {
		t.Fatalf("create should return ticket id 1, got %q", out)
	}

	out, _ = runCLIOK(t, bin, repo, "-home", home, "-project", "demo", "ticket", "start", "-ticket", "1")
	if !strings.Contains(out, "started") || !strings.Contains(out, "worker=w") {
		t.Fatalf("start output unexpected:\n%s", out)
	}

	out, _ = runCLIOK(t, bin, repo, "-home", home, "-project", "demo", "ticket", "ls")
	if !strings.Contains(out, "tmux flow") {
		t.Fatalf("ls output missing ticket after start:\n%s", out)
	}

	out, _ = runCLIOK(t, bin, repo, "-home", home, "-project", "demo", "ticket", "stop", "-ticket", "1")
	if !strings.Contains(out, "stopped: worker=") {
		t.Fatalf("stop output unexpected:\n%s", out)
	}
}
