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
		{"inbox", "ls", "--help"},
		{"merge", "ls", "--help"},
		{"merge", "discard", "--help"},
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

func TestCLI_HelpIncludesNode(t *testing.T) {
	bin := buildCLIBinary(t)
	repo := initGitRepo(t)

	stdout, stderr, err := runCLI(t, bin, repo, "--help")
	if err != nil {
		t.Fatalf("dalek --help should exit 0: %v, stdout=%s stderr=%s", err, stdout, stderr)
	}
	combined := stdout + "\n" + stderr
	if !strings.Contains(combined, "node") {
		t.Fatalf("top-level help should include node command, got:\n%s", combined)
	}
}

func TestCLI_NodeHelpIncludesRunLoop(t *testing.T) {
	bin := buildCLIBinary(t)
	repo := initGitRepo(t)

	stdout, stderr, err := runCLI(t, bin, repo, "node", "--help")
	if err != nil {
		t.Fatalf("node --help should exit 0: %v, stdout=%s stderr=%s", err, stdout, stderr)
	}
	combined := stdout + "\n" + stderr
	if !strings.Contains(combined, "run-loop") {
		t.Fatalf("node --help should include run-loop, got:\n%s", combined)
	}
}

func TestCLI_RunShowHelpIncludesTaskStatusAndWarnings(t *testing.T) {
	bin := buildCLIBinary(t)
	repo := initGitRepo(t)

	stdout, stderr, err := runCLI(t, bin, repo, "run", "show", "--help")
	if err != nil {
		t.Fatalf("run show --help should exit 0: %v, stdout=%s stderr=%s", err, stdout, stderr)
	}
	combined := stdout + "\n" + stderr
	if !strings.Contains(combined, "聚合状态") {
		t.Fatalf("run show --help should mention aggregated status, got:\n%s", combined)
	}
}

func TestCLI_RunArtifactHelpIncludesIssues(t *testing.T) {
	bin := buildCLIBinary(t)
	repo := initGitRepo(t)

	stdout, stderr, err := runCLI(t, bin, repo, "run", "artifact", "ls", "--help")
	if err != nil {
		t.Fatalf("run artifact ls --help should exit 0: %v, stdout=%s stderr=%s", err, stdout, stderr)
	}
	combined := stdout + "\n" + stderr
	if !strings.Contains(combined, "artifact issues") {
		t.Fatalf("run artifact ls --help should mention artifact issues, got:\n%s", combined)
	}
}

func TestCLI_NodeRunLoopHelpIncludesRecoveryWarnings(t *testing.T) {
	bin := buildCLIBinary(t)
	repo := initGitRepo(t)

	stdout, stderr, err := runCLI(t, bin, repo, "node", "run-loop", "--help")
	if err != nil {
		t.Fatalf("node run-loop --help should exit 0: %v, stdout=%s stderr=%s", err, stdout, stderr)
	}
	combined := stdout + "\n" + stderr
	if !strings.Contains(combined, "recovery") || !strings.Contains(combined, "warnings") {
		t.Fatalf("node run-loop --help should mention recovery and warnings, got:\n%s", combined)
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

func TestCLI_ManagerRunSyncDispatchRequiresOnce(t *testing.T) {
	bin := buildCLIBinary(t)
	repo := initGitRepo(t)

	_, stderr, err := runCLI(t, bin, repo, "manager", "run", "--sync-dispatch")
	if err == nil {
		t.Fatalf("manager run --sync-dispatch without --once should fail")
	}
	if !strings.Contains(stderr, "缺少必填参数 --once") {
		t.Fatalf("missing --once hint not found:\n%s", stderr)
	}
}

func TestCLI_ManagerRunSyncDispatchRequiresPositiveDispatchTimeout(t *testing.T) {
	bin := buildCLIBinary(t)
	repo := initGitRepo(t)

	cases := []struct {
		name string
		args []string
	}{
		{
			name: "missing",
			args: []string{"manager", "run", "--once", "--sync-dispatch"},
		},
		{
			name: "zero",
			args: []string{"manager", "run", "--once", "--sync-dispatch", "--dispatch-timeout", "0"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, stderr, err := runCLI(t, bin, repo, tc.args...)
			if err == nil {
				t.Fatalf("manager run --sync-dispatch should fail when dispatch-timeout invalid")
			}
			if !strings.Contains(stderr, "--sync-dispatch 模式必须指定 --dispatch-timeout > 0") {
				t.Fatalf("dispatch-timeout hard requirement missing:\n%s", stderr)
			}
		})
	}
}

func TestCLI_ManagerRunSyncDispatchOnceDryRunJSON(t *testing.T) {
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
		"--sync-dispatch",
		"--dry-run",
		"--dispatch-timeout", "120m",
		"-o", "json",
	)
	if err != nil {
		t.Fatalf("manager run sync-dispatch dry-run should succeed: err=%v stderr=%s", err, stderr)
	}

	var payload struct {
		Schema          string `json:"schema"`
		Mode            string `json:"mode"`
		DispatchTimeout string `json:"dispatch_timeout"`
	}
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("decode manager run json failed: %v\nraw=%s", err, stdout)
	}
	if payload.Schema != "dalek.manager.run.v1" {
		t.Fatalf("unexpected schema: %q", payload.Schema)
	}
	if payload.Mode != "sync_dispatch" {
		t.Fatalf("unexpected mode: %q", payload.Mode)
	}
	if payload.DispatchTimeout != "2h0m0s" {
		t.Fatalf("unexpected dispatch_timeout: %q", payload.DispatchTimeout)
	}
}
