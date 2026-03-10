package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"dalek/internal/app"
)

type daemonDispatchSubmitPayload struct {
	Project    string `json:"project"`
	TicketID   uint   `json:"ticket_id"`
	RequestID  string `json:"request_id"`
	Prompt     string `json:"prompt"`
	AutoStart  *bool  `json:"auto_start"`
	BaseBranch string `json:"base_branch"`
}

type daemonTicketStartPayload struct {
	Project    string `json:"project"`
	TicketID   uint   `json:"ticket_id"`
	BaseBranch string `json:"base_branch"`
}

type daemonWorkerRunSubmitPayload struct {
	Project   string `json:"project"`
	TicketID  uint   `json:"ticket_id"`
	RequestID string `json:"request_id"`
	Prompt    string `json:"prompt"`
}

func prepareDemoProjectWithOneTicket(t *testing.T, bin, repo, home string) {
	t.Helper()
	_, _ = runCLIOK(t, bin, repo, "-home", home, "init", "-name", "demo")
	out, _ := runCLIOK(
		t,
		bin,
		repo,
		"-home", home,
		"-project", "demo",
		"ticket", "create",
		"-title", "dispatch acceptance",
		"-desc", "dispatch acceptance",
	)
	if strings.TrimSpace(out) != "1" {
		t.Fatalf("expected ticket id 1, got=%q", out)
	}
}

func configureDaemonInternalListenForE2E(t *testing.T, homeDir, daemonURL string) {
	t.Helper()
	h, err := app.OpenHome(homeDir)
	if err != nil {
		t.Fatalf("OpenHome failed: %v", err)
	}
	u, err := url.Parse(strings.TrimSpace(daemonURL))
	if err != nil {
		t.Fatalf("parse daemon url failed: %v", err)
	}
	cfg := h.Config.WithDefaults()
	cfg.Daemon.Internal.Listen = strings.TrimSpace(u.Host)
	if err := app.WriteHomeConfigAtomic(h.ConfigPath, cfg); err != nil {
		t.Fatalf("WriteHomeConfigAtomic failed: %v", err)
	}
}

func TestCLI_TicketStart_StartsQueued(t *testing.T) {
	bin := buildCLIBinary(t)
	repo := initGitRepo(t)
	home := filepath.Join(t.TempDir(), "home")
	prepareDemoProjectWithOneTicket(t, bin, repo, home)

	var called atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/tickets/start" {
			http.NotFound(w, r)
			return
		}
		var payload daemonTicketStartPayload
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode payload failed: %v", err)
		}
		if strings.TrimSpace(payload.Project) != "demo" || payload.TicketID != 1 {
			t.Fatalf("unexpected ticket-start payload: %+v", payload)
		}
		if strings.TrimSpace(payload.BaseBranch) != "main" {
			t.Fatalf("expected base_branch=main, got=%q", payload.BaseBranch)
		}
		called.Store(true)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"started":         true,
			"project":         "demo",
			"ticket_id":       1,
			"worker_id":       9,
			"workflow_status": "queued",
			"worktree":        "/tmp/worktree/t1",
			"branch":          "ticket/1",
			"log_path":        "/tmp/workers/9.log",
		})
	}))
	t.Cleanup(server.Close)

	configureDaemonInternalListenForE2E(t, home, server.URL)

	stdout, _ := runCLIOK(
		t,
		bin,
		repo,
		"-home", home,
		"-project", "demo",
		"ticket", "start",
		"--ticket", "1",
		"--base", "main",
		"-o", "json",
	)

	var resp struct {
		Schema         string `json:"schema"`
		Started        bool   `json:"started"`
		TicketID       uint   `json:"ticket_id"`
		WorkerID       uint   `json:"worker_id"`
		WorkflowStatus string `json:"workflow_status"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &resp); err != nil {
		t.Fatalf("decode response failed: %v\nraw=%s", err, stdout)
	}
	if resp.Schema != "dalek.ticket.start.v1" {
		t.Fatalf("unexpected schema: %q", resp.Schema)
	}
	if !resp.Started {
		t.Fatalf("expected started=true, got=%+v", resp)
	}
	if resp.TicketID != 1 || resp.WorkerID != 9 || resp.WorkflowStatus != "queued" {
		t.Fatalf("unexpected response payload: %+v", resp)
	}
	if !called.Load() {
		t.Fatalf("expected daemon start endpoint called")
	}
}

func TestCLI_TicketDispatch_Removed(t *testing.T) {
	bin := buildCLIBinary(t)
	repo := initGitRepo(t)
	home := filepath.Join(t.TempDir(), "home")
	prepareDemoProjectWithOneTicket(t, bin, repo, home)

	stdout, stderr, err := runCLI(
		t,
		bin,
		repo,
		"-home", home,
		"-project", "demo",
		"ticket", "dispatch",
		"--ticket", "1",
	)
	if err == nil {
		t.Fatalf("expected ticket dispatch removed\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
	}
	if !strings.Contains(stderr, "ticket dispatch 已移除") {
		t.Fatalf("stderr should mention removed command:\n%s", stderr)
	}
	if !strings.Contains(stderr, "请改用 ticket start") {
		t.Fatalf("stderr should mention ticket start migration:\n%s", stderr)
	}
}

func TestCLI_TicketStart_DaemonUnavailable_ShowsSyncFallback(t *testing.T) {
	bin := buildCLIBinary(t)
	repo := initGitRepo(t)
	home := filepath.Join(t.TempDir(), "home")
	prepareDemoProjectWithOneTicket(t, bin, repo, home)

	configureDaemonInternalListenForE2E(t, home, "http://127.0.0.1:1")

	stdout, stderr, err := runCLI(
		t,
		bin,
		repo,
		"-home", home,
		"-project", "demo",
		"ticket", "start",
		"--ticket", "1",
	)
	if err == nil {
		t.Fatalf("expected ticket start fail when daemon unavailable\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
	}
	if !strings.Contains(stderr, "daemon 不在线，无法启动 ticket") {
		t.Fatalf("stderr should contain daemon unavailable hint:\n%s", stderr)
	}
	if !strings.Contains(stderr, "Fix: `dalek daemon start`") {
		t.Fatalf("stderr should contain daemon start guide:\n%s", stderr)
	}
	fixPos := strings.Index(stderr, "Fix:")
	causePos := strings.Index(stderr, "Cause:")
	if fixPos == -1 || causePos == -1 || fixPos > causePos {
		t.Fatalf("stderr should output Fix before Cause:\n%s", stderr)
	}
	if !strings.Contains(stderr, "P50=28m, P90=96m（无历史时默认 20~120m）") {
		t.Fatalf("stderr should contain P50/P90 estimate:\n%s", stderr)
	}
	if strings.Contains(stderr, "--sync") {
		t.Fatalf("stderr should not contain sync fallback hint:\n%s", stderr)
	}
}

func TestCLI_TicketStart_TimeoutMustBePositiveWhenProvided(t *testing.T) {
	bin := buildCLIBinary(t)
	repo := initGitRepo(t)
	home := filepath.Join(t.TempDir(), "home")
	prepareDemoProjectWithOneTicket(t, bin, repo, home)

	for _, timeoutArg := range []string{"0", "-1s"} {
		t.Run(timeoutArg, func(t *testing.T) {
			stdout, stderr, err := runCLI(
				t,
				bin,
				repo,
				"-home", home,
				"-project", "demo",
				"ticket", "start",
				"--ticket", "1",
				"--timeout", timeoutArg,
			)
			if err == nil {
				t.Fatalf("expected ticket start fail when timeout=%s\nstdout:\n%s\nstderr:\n%s", timeoutArg, stdout, stderr)
			}
			if !strings.Contains(stderr, "非法参数 --timeout") {
				t.Fatalf("stderr should contain timeout usage error:\n%s", stderr)
			}
			if !strings.Contains(stderr, "--timeout 必须大于 0") {
				t.Fatalf("stderr should contain positive timeout hint:\n%s", stderr)
			}
		})
	}
}

func TestCLI_TicketStart_DepthGuardBlocksNestedDispatch(t *testing.T) {
	bin := buildCLIBinary(t)
	repo := initGitRepo(t)
	home := filepath.Join(t.TempDir(), "home")
	prepareDemoProjectWithOneTicket(t, bin, repo, home)

	stdout, stderr, err := runCLIWithEnv(
		t,
		bin,
		repo,
		map[string]string{
			dispatchDepthEnvKey: "1",
		},
		"-home", home,
		"-project", "demo",
		"ticket", "start",
		"--ticket", "1",
		"-o", "json",
	)
	if err == nil {
		t.Fatalf("expected ticket start blocked by dispatch depth\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
	}
	if !strings.Contains(stderr, "禁止在二次派发上下文执行 dalek ticket start") {
		t.Fatalf("stderr should contain dispatch-depth guard error:\n%s", stderr)
	}
	if strings.Contains(stderr, "daemon 不在线") {
		t.Fatalf("guard should stop before daemon fallback branch:\n%s", stderr)
	}

	var payload cliErrorJSON
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &payload); err != nil {
		t.Fatalf("decode json error payload failed: %v\nraw=%s", err, stdout)
	}
	if payload.Schema != "dalek.error.v1" || payload.ExitCode != 1 {
		t.Fatalf("unexpected error payload: %+v", payload)
	}
	if !strings.Contains(payload.Cause, "DALEK_DISPATCH_DEPTH=1") {
		t.Fatalf("json cause should contain dispatch depth detail: %+v", payload)
	}
}

func TestCLI_AgentRun_DepthGuardBlocksNestedDispatch(t *testing.T) {
	bin := buildCLIBinary(t)
	repo := initGitRepo(t)
	home := filepath.Join(t.TempDir(), "home")
	prepareDemoProjectWithOneTicket(t, bin, repo, home)

	stdout, stderr, err := runCLIWithEnv(
		t,
		bin,
		repo,
		map[string]string{
			dispatchDepthEnvKey: "1",
		},
		"-home", home,
		"-project", "demo",
		"agent", "run",
		"--prompt", "do something",
		"-o", "json",
	)
	if err == nil {
		t.Fatalf("expected agent run blocked by dispatch depth\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
	}
	if !strings.Contains(stderr, "禁止在二次派发上下文执行 dalek agent run") {
		t.Fatalf("stderr should contain dispatch-depth guard error:\n%s", stderr)
	}
	if strings.Contains(stderr, "daemon 不在线") {
		t.Fatalf("guard should stop before daemon fallback branch:\n%s", stderr)
	}

	var payload cliErrorJSON
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &payload); err != nil {
		t.Fatalf("decode json error payload failed: %v\nraw=%s", err, stdout)
	}
	if payload.Schema != "dalek.error.v1" || payload.ExitCode != 1 {
		t.Fatalf("unexpected error payload: %+v", payload)
	}
	if !strings.Contains(payload.Cause, "DALEK_DISPATCH_DEPTH=1") {
		t.Fatalf("json cause should contain dispatch depth detail: %+v", payload)
	}
}

func TestCLI_WorkerRun_AsyncAccepted(t *testing.T) {
	bin := buildCLIBinary(t)
	repo := initGitRepo(t)
	home := filepath.Join(t.TempDir(), "home")
	prepareDemoProjectWithOneTicket(t, bin, repo, home)

	var called atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/worker-run/submit" {
			http.NotFound(w, r)
			return
		}
		var payload daemonWorkerRunSubmitPayload
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode payload failed: %v", err)
		}
		if strings.TrimSpace(payload.Project) != "demo" || payload.TicketID != 1 {
			t.Fatalf("unexpected worker-run payload: %+v", payload)
		}
		if strings.TrimSpace(payload.RequestID) != "worker-run-e2e-req" {
			t.Fatalf("unexpected request_id: %q", payload.RequestID)
		}
		if strings.TrimSpace(payload.Prompt) != "继续执行任务" {
			t.Fatalf("unexpected prompt: %q", payload.Prompt)
		}
		called.Store(true)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"accepted":    true,
			"project":     "demo",
			"request_id":  "worker-run-e2e-req",
			"task_run_id": 8102,
			"ticket_id":   1,
			"worker_id":   12,
		})
	}))
	t.Cleanup(server.Close)

	configureDaemonInternalListenForE2E(t, home, server.URL)

	stdout, _ := runCLIOK(
		t,
		bin,
		repo,
		"-home", home,
		"-project", "demo",
		"worker", "run",
		"--ticket", "1",
		"--request-id", "worker-run-e2e-req",
		"--prompt", "继续执行任务",
		"-o", "json",
	)

	var resp struct {
		Schema    string `json:"schema"`
		Mode      string `json:"mode"`
		Accepted  bool   `json:"accepted"`
		RequestID string `json:"request_id"`
		TaskRunID uint   `json:"task_run_id"`
		TicketID  uint   `json:"ticket_id"`
		WorkerID  uint   `json:"worker_id"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &resp); err != nil {
		t.Fatalf("decode response failed: %v\nraw=%s", err, stdout)
	}
	if resp.Schema != "dalek.worker.run.accepted.v1" {
		t.Fatalf("unexpected schema: %q", resp.Schema)
	}
	if resp.Mode != "async" || !resp.Accepted {
		t.Fatalf("unexpected mode/accepted: mode=%q accepted=%v", resp.Mode, resp.Accepted)
	}
	if resp.RequestID != "worker-run-e2e-req" || resp.TaskRunID != 8102 || resp.TicketID != 1 || resp.WorkerID != 12 {
		t.Fatalf("unexpected response payload: %+v", resp)
	}
	if !called.Load() {
		t.Fatalf("expected daemon submit endpoint called")
	}
}

func TestCLI_WorkerRun_DaemonUnavailable_ShowsSyncFallback(t *testing.T) {
	bin := buildCLIBinary(t)
	repo := initGitRepo(t)
	home := filepath.Join(t.TempDir(), "home")
	prepareDemoProjectWithOneTicket(t, bin, repo, home)

	configureDaemonInternalListenForE2E(t, home, "http://127.0.0.1:1")

	stdout, stderr, err := runCLI(
		t,
		bin,
		repo,
		"-home", home,
		"-project", "demo",
		"worker", "run",
		"--ticket", "1",
	)
	if err == nil {
		t.Fatalf("expected worker run fail when daemon unavailable\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
	}
	if !strings.Contains(stderr, "daemon 不在线，无法异步执行 worker run") {
		t.Fatalf("stderr should contain daemon unavailable hint:\n%s", stderr)
	}
	if !strings.Contains(stderr, "Fix: `dalek daemon start`") {
		t.Fatalf("stderr should contain daemon start guide:\n%s", stderr)
	}
	fixPos := strings.Index(stderr, "Fix:")
	causePos := strings.Index(stderr, "Cause:")
	if fixPos == -1 || causePos == -1 || fixPos > causePos {
		t.Fatalf("stderr should output Fix before Cause:\n%s", stderr)
	}
	if !strings.Contains(stderr, "P50=28m, P90=96m（无历史时默认 20~120m）") {
		t.Fatalf("stderr should contain P50/P90 estimate:\n%s", stderr)
	}
	if strings.Contains(stderr, "--sync") {
		t.Fatalf("stderr should not contain sync fallback hint:\n%s", stderr)
	}
}

func TestCLI_WorkerRun_TimeoutMustBePositiveWhenProvided(t *testing.T) {
	bin := buildCLIBinary(t)
	repo := initGitRepo(t)
	home := filepath.Join(t.TempDir(), "home")
	prepareDemoProjectWithOneTicket(t, bin, repo, home)

	for _, timeoutArg := range []string{"0", "-1s"} {
		t.Run(timeoutArg, func(t *testing.T) {
			stdout, stderr, err := runCLI(
				t,
				bin,
				repo,
				"-home", home,
				"-project", "demo",
				"worker", "run",
				"--ticket", "1",
				"--timeout", timeoutArg,
			)
			if err == nil {
				t.Fatalf("expected worker run fail when timeout=%s\nstdout:\n%s\nstderr:\n%s", timeoutArg, stdout, stderr)
			}
			if !strings.Contains(stderr, "非法参数 --timeout") {
				t.Fatalf("stderr should contain timeout usage error:\n%s", stderr)
			}
			if !strings.Contains(stderr, "--timeout 必须为正值") {
				t.Fatalf("stderr should contain positive timeout hint:\n%s", stderr)
			}
		})
	}
}

func TestCLI_WorkerRun_DepthGuardBlocksNestedDispatch(t *testing.T) {
	bin := buildCLIBinary(t)
	repo := initGitRepo(t)
	home := filepath.Join(t.TempDir(), "home")
	prepareDemoProjectWithOneTicket(t, bin, repo, home)

	stdout, stderr, err := runCLIWithEnv(
		t,
		bin,
		repo,
		map[string]string{
			dispatchDepthEnvKey: "1",
		},
		"-home", home,
		"-project", "demo",
		"worker", "run",
		"--ticket", "1",
		"-o", "json",
	)
	if err == nil {
		t.Fatalf("expected worker run blocked by dispatch depth\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
	}
	if !strings.Contains(stderr, "禁止在二次派发上下文执行 dalek worker run") {
		t.Fatalf("stderr should contain dispatch-depth guard error:\n%s", stderr)
	}
	if strings.Contains(stderr, "daemon 不在线") {
		t.Fatalf("guard should stop before daemon fallback branch:\n%s", stderr)
	}

	var payload cliErrorJSON
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &payload); err != nil {
		t.Fatalf("decode json error payload failed: %v\nraw=%s", err, stdout)
	}
	if payload.Schema != "dalek.error.v1" || payload.ExitCode != 1 {
		t.Fatalf("unexpected error payload: %+v", payload)
	}
	if !strings.Contains(payload.Cause, "DALEK_DISPATCH_DEPTH=1") {
		t.Fatalf("json cause should contain dispatch depth detail: %+v", payload)
	}
}
