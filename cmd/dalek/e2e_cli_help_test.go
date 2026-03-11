package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCLI_HelpShouldExitZero(t *testing.T) {
	bin := buildCLIBinary(t)
	repo := initGitRepo(t)

	cases := [][]string{
		{"ticket", "start", "--help"},
		{"ticket", "edit", "--help"},
		{"manager", "status", "--help"},
		{"pm", "dashboard", "--help"},
		{"inbox", "ls", "--help"},
		{"merge", "ls", "--help"},
		{"merge", "status", "--help"},
		{"merge", "abandon", "--help"},
		{"merge", "sync-ref", "--help"},
		{"merge", "retarget", "--help"},
		{"merge", "rescan", "--help"},
		{"gateway", "send", "--help"},
		{"feishu", "auth", "--help"},
		{"feishu", "doc", "--help"},
		{"feishu", "doc", "create", "--help"},
		{"feishu", "wiki", "--help"},
		{"feishu", "wiki", "ls", "--help"},
		{"feishu", "perm", "--help"},
		{"feishu", "perm", "share", "--help"},
	}

	for _, args := range cases {
		stdout, stderr, err := runCLI(t, bin, repo, args...)
		if err != nil {
			t.Fatalf("help should exit 0: %v, stdout=%s stderr=%s", args, stdout, stderr)
		}
		combined := stdout + "\n" + stderr
		if !strings.Contains(combined, "Usage:") {
			t.Fatalf("help output should include Usage: args=%v output=%s", args, combined)
		}
		if strings.Contains(combined, "Error:") {
			t.Fatalf("help output should not include Error: args=%v output=%s", args, combined)
		}
	}
}

func TestCLI_TicketHelpIncludesEdit(t *testing.T) {
	bin := buildCLIBinary(t)
	repo := initGitRepo(t)

	stdout, stderr, err := runCLI(t, bin, repo, "ticket", "--help")
	if err != nil {
		t.Fatalf("ticket --help should exit 0: %v, stdout=%s stderr=%s", err, stdout, stderr)
	}
	combined := stdout + "\n" + stderr
	if !strings.Contains(combined, "edit") {
		t.Fatalf("ticket --help should include edit subcommand, got:\n%s", combined)
	}
}

func TestCLI_OldTopLevelCommandFails(t *testing.T) {
	bin := buildCLIBinary(t)
	repo := initGitRepo(t)

	_, stderr, err := runCLI(t, bin, repo, "create")
	if err == nil {
		t.Fatalf("old top-level command should fail")
	}
	if !strings.Contains(stderr, "未知命令") || !strings.Contains(stderr, "运行 dalek --help 查看可用命令") {
		t.Fatalf("unknown command hint missing for create:\n%s", stderr)
	}

	_, stderr, err = runCLI(t, bin, repo, "agent", "start")
	if err == nil {
		t.Fatalf("invalid agent subcommand should fail")
	}
	if !strings.Contains(stderr, "未知 agent 子命令") || !strings.Contains(stderr, "dalek agent --help") {
		t.Fatalf("agent unknown subcommand hint missing:\n%s", stderr)
	}
}

func TestCLI_JSONErrorForGatewayChat(t *testing.T) {
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
		"gateway", "chat",
		"-ws-url", "ws://127.0.0.1:1/ws",
		"-text", "hello",
		"-o", "json",
	)
	if err == nil {
		t.Fatalf("gateway chat with unavailable daemon should fail")
	}
	if !strings.Contains(stderr, "Error: gateway chat 失败") {
		t.Fatalf("stderr should include structured error:\n%s", stderr)
	}
	if !strings.Contains(stdout, "\"schema\": \"dalek.error.v1\"") {
		t.Fatalf("stdout should include json error schema, got:\n%s", stdout)
	}
}

func TestCLI_GatewayServeRemoved(t *testing.T) {
	bin := buildCLIBinary(t)
	repo := initGitRepo(t)

	_, stderr, err := runCLI(t, bin, repo, "gateway", "serve")
	if err == nil {
		t.Fatalf("gateway serve should fail with migration hint")
	}
	if !strings.Contains(stderr, "gateway serve 已迁移到 daemon") || !strings.Contains(stderr, "dalek daemon start") {
		t.Fatalf("gateway serve migration hint missing:\n%s", stderr)
	}
}

func TestCLI_ManagerRunRemoved(t *testing.T) {
	bin := buildCLIBinary(t)
	repo := initGitRepo(t)

	_, stderr, err := runCLI(t, bin, repo, "manager", "run")
	if err == nil {
		t.Fatalf("manager run should fail with migration hint")
	}
	if !strings.Contains(stderr, "manager run 已迁移到 daemon") || !strings.Contains(stderr, "dalek daemon start") {
		t.Fatalf("manager run migration hint missing:\n%s", stderr)
	}
}

func TestCLI_TicketIntegrationMigrated(t *testing.T) {
	bin := buildCLIBinary(t)
	repo := initGitRepo(t)

	_, stderr, err := runCLI(t, bin, repo, "ticket", "integration")
	if err == nil {
		t.Fatalf("ticket integration should fail with migration hint")
	}
	if !strings.Contains(stderr, "ticket integration 已迁移到 merge 命令组") {
		t.Fatalf("ticket integration migration hint missing:\n%s", stderr)
	}
	if !strings.Contains(stderr, "dalek merge status --ticket 1") || !strings.Contains(stderr, "dalek merge abandon --ticket 1 --reason") {
		t.Fatalf("ticket integration should mention merge replacements:\n%s", stderr)
	}
}

func TestCLI_ManagerRunSyncWorkerRunRequiresOnce(t *testing.T) {
	bin := buildCLIBinary(t)
	repo := initGitRepo(t)

	_, stderr, err := runCLI(t, bin, repo, "manager", "run", "--sync-worker-run")
	if err == nil {
		t.Fatalf("manager run --sync-worker-run without --once should fail")
	}
	if !strings.Contains(stderr, "缺少必填参数 --once") {
		t.Fatalf("missing --once hint not found:\n%s", stderr)
	}
}

func TestCLI_ManagerRunSyncWorkerRunRequiresPositiveTimeout(t *testing.T) {
	bin := buildCLIBinary(t)
	repo := initGitRepo(t)

	cases := []struct {
		name string
		args []string
	}{
		{
			name: "missing",
			args: []string{"manager", "run", "--once", "--sync-worker-run"},
		},
		{
			name: "zero",
			args: []string{"manager", "run", "--once", "--sync-worker-run", "--worker-run-timeout", "0"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, stderr, err := runCLI(t, bin, repo, tc.args...)
			if err == nil {
				t.Fatalf("manager run --sync-worker-run should fail when worker-run-timeout invalid")
			}
			if !strings.Contains(stderr, "--sync-worker-run 模式必须指定 --worker-run-timeout > 0") {
				t.Fatalf("worker-run-timeout hard requirement missing:\n%s", stderr)
			}
		})
	}
}

func TestCLI_ManagerRunSyncWorkerRunOnceDryRunJSON(t *testing.T) {
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
		"manager", "run",
		"--once",
		"--sync-worker-run",
		"--dry-run",
		"--worker-run-timeout", "120m",
		"-o", "json",
	)
	if err != nil {
		t.Fatalf("manager run sync-worker-run dry-run should succeed: err=%v stderr=%s", err, stderr)
	}

	var payload struct {
		Schema           string `json:"schema"`
		Mode             string `json:"mode"`
		WorkerRunTimeout string `json:"worker_run_timeout"`
	}
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("decode manager run json failed: %v\nraw=%s", err, stdout)
	}
	if payload.Schema != "dalek.manager.run.v1" {
		t.Fatalf("unexpected schema: %q", payload.Schema)
	}
	if payload.Mode != "sync_worker_run" {
		t.Fatalf("unexpected mode: %q", payload.Mode)
	}
	if payload.WorkerRunTimeout != "2h0m0s" {
		t.Fatalf("unexpected worker_run_timeout: %q", payload.WorkerRunTimeout)
	}
}
