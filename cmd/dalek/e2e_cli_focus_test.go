package main

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestCLI_ManagerFocusCommands_DaemonUnavailable(t *testing.T) {
	bin := buildCLIBinary(t)
	repo := initGitRepo(t)
	home := filepath.Join(t.TempDir(), "home")

	prepareDemoProjectWithOneTicket(t, bin, repo, home)
	configureDaemonInternalListenForE2E(t, home, "http://127.0.0.1:1")

	cases := []struct {
		name string
		args []string
	}{
		{
			name: "run",
			args: []string{"-home", home, "-project", "demo", "manager", "run", "--mode", "batch", "--tickets", "1"},
		},
		{
			name: "stop",
			args: []string{"-home", home, "-project", "demo", "manager", "stop", "--focus-id", "1"},
		},
		{
			name: "tail",
			args: []string{"-home", home, "-project", "demo", "manager", "tail", "--focus-id", "1"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stdout, stderr, err := runCLI(t, bin, repo, tc.args...)
			if err == nil {
				t.Fatalf("expected manager %s fail when daemon unavailable\nstdout:\n%s\nstderr:\n%s", tc.name, stdout, stderr)
			}
			if strings.TrimSpace(stdout) != "" {
				t.Fatalf("stdout should be empty for manager %s runtime error, got:\n%s", tc.name, stdout)
			}
			if !strings.Contains(stderr, "daemon 不在线") {
				t.Fatalf("stderr should mention daemon unavailable for manager %s:\n%s", tc.name, stderr)
			}
			if !strings.Contains(stderr, "请先执行 dalek daemon start 后重试") {
				t.Fatalf("stderr should contain daemon start guide for manager %s:\n%s", tc.name, stderr)
			}
		})
	}
}
