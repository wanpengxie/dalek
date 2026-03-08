package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"dalek/internal/app"
	"dalek/internal/contracts"
	channelsvc "dalek/internal/services/channel"
)

func runCmd(t *testing.T, dir string, name string, args ...string) (string, string, error) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.String(), stderr.String(), err
}

func runCmdOK(t *testing.T, dir string, name string, args ...string) (string, string) {
	t.Helper()
	stdout, stderr, err := runCmd(t, dir, name, args...)
	if err != nil {
		t.Fatalf("command failed: %s %s\nstderr:\n%s\nerr=%v", name, strings.Join(args, " "), stderr, err)
	}
	return stdout, stderr
}

func initGitRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	runCmdOK(t, repo, "git", "init")
	runCmdOK(t, repo, "git", "config", "user.email", "dalek-test@example.com")
	runCmdOK(t, repo, "git", "config", "user.name", "dalek-test")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write README failed: %v", err)
	}
	runCmdOK(t, repo, "git", "add", "README.md")
	runCmdOK(t, repo, "git", "commit", "-m", "init")
	return repo
}

func buildCLIBinary(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "dalek")
	_, stderr, err := runCmd(t, ".", "go", "build", "-o", bin, ".")
	if err != nil {
		t.Fatalf("go build failed:\n%s\nerr=%v", stderr, err)
	}
	return bin
}

func runCLI(t *testing.T, bin, dir string, args ...string) (string, string, error) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.String(), stderr.String(), err
}

func runCLIOK(t *testing.T, bin, dir string, args ...string) (string, string) {
	t.Helper()
	stdout, stderr, err := runCLI(t, bin, dir, args...)
	if err != nil {
		t.Fatalf("cli failed: %s %s\nstderr:\n%s\nerr=%v", bin, strings.Join(args, " "), stderr, err)
	}
	return stdout, stderr
}

func TestCLI_E2E_BasicWorkflow(t *testing.T) {
	bin := buildCLIBinary(t)
	repo := initGitRepo(t)
	home := filepath.Join(t.TempDir(), "home")

	// 1) init
	_, _ = runCLIOK(t, bin, repo, "-home", home, "init", "-name", "demo")

	// 2) project ls
	out, _ := runCLIOK(t, bin, repo, "-home", home, "project", "ls")
	if !strings.Contains(out, "demo") {
		t.Fatalf("project ls should include demo, got:\n%s", out)
	}

	// 3) create ticket
	out, _ = runCLIOK(t, bin, repo, "-home", home, "-project", "demo", "ticket", "create", "-title", "first ticket", "-desc", "first ticket description", "--label", "feature")
	if strings.TrimSpace(out) != "1" {
		t.Fatalf("create should return ticket id 1, got: %q", out)
	}

	// 4) list ticket
	out, _ = runCLIOK(t, bin, repo, "-home", home, "-project", "demo", "ticket", "ls")
	if !strings.Contains(out, "first ticket") || !strings.Contains(out, "ID") || !strings.Contains(out, "LABEL") || !strings.Contains(out, "feature") || !strings.Contains(out, "PRIORITY") || !strings.Contains(out, "STATUS") {
		t.Fatalf("ticket ls output missing expected columns:\n%s", out)
	}

	// 5) show ticket（文本输出包含 label）
	out, _ = runCLIOK(t, bin, repo, "-home", home, "-project", "demo", "ticket", "show", "--ticket", "1")
	if !strings.Contains(out, "label:\tfeature") {
		t.Fatalf("ticket show text should include label, got:\n%s", out)
	}

	// 6) manager status（确保 manager 子命令链路可用）
	out, _ = runCLIOK(t, bin, repo, "-home", home, "-project", "demo", "manager", "status")
	if !strings.Contains(out, "autopilot=") || !strings.Contains(out, "planner_dirty=") || !strings.Contains(out, "planner_active_task_run_id=") {
		t.Fatalf("manager status output unexpected:\n%s", out)
	}
	out, _ = runCLIOK(t, bin, repo, "-home", home, "-project", "demo", "manager", "status", "-o", "json")
	var managerStatus struct {
		Schema                 string `json:"schema"`
		PlannerDirty           bool   `json:"planner_dirty"`
		PlannerActiveTaskRunID *uint  `json:"planner_active_task_run_id"`
	}
	if err := json.Unmarshal([]byte(out), &managerStatus); err != nil {
		t.Fatalf("unmarshal manager status json failed: %v\nraw=%s", err, out)
	}
	if managerStatus.Schema != "dalek.manager.status.v1" {
		t.Fatalf("unexpected manager status schema: %s", managerStatus.Schema)
	}
	if managerStatus.PlannerDirty {
		t.Fatalf("planner_dirty should default false")
	}
	if managerStatus.PlannerActiveTaskRunID != nil {
		t.Fatalf("planner_active_task_run_id should default nil, got=%v", *managerStatus.PlannerActiveTaskRunID)
	}

	// 7) pm dashboard（text + json）
	out, _ = runCLIOK(t, bin, repo, "-home", home, "-project", "demo", "pm", "dashboard")
	if !strings.Contains(out, "=== Project Dashboard ===") || !strings.Contains(out, "Tickets:") || !strings.Contains(out, "Workers:") || !strings.Contains(out, "Planner:") || !strings.Contains(out, "Merges:") || !strings.Contains(out, "Inbox:") {
		t.Fatalf("pm dashboard text output unexpected:\n%s", out)
	}
	out, _ = runCLIOK(t, bin, repo, "-home", home, "-project", "demo", "pm", "dashboard", "-o", "json")
	var dashboard struct {
		TicketCounts struct {
			Backlog int `json:"backlog"`
		} `json:"ticket_counts"`
		WorkerStats struct {
			MaxRunning int `json:"max_running"`
		} `json:"worker_stats"`
	}
	if err := json.Unmarshal([]byte(out), &dashboard); err != nil {
		t.Fatalf("unmarshal pm dashboard json failed: %v\nraw=%s", err, out)
	}
	if dashboard.TicketCounts.Backlog != 0 {
		t.Fatalf("ticket backlog should default 0, got=%d", dashboard.TicketCounts.Backlog)
	}
	if dashboard.WorkerStats.MaxRunning != 3 {
		t.Fatalf("worker max_running should default 3, got=%d", dashboard.WorkerStats.MaxRunning)
	}

	// 8) remove + empty list
	out, _ = runCLIOK(t, bin, repo, "-home", home, "project", "rm", "-name", "demo")
	if !strings.Contains(out, "demo removed") {
		t.Fatalf("project rm output unexpected:\n%s", out)
	}
	out, _ = runCLIOK(t, bin, repo, "-home", home, "project", "ls")
	if strings.TrimSpace(out) != "(empty)" {
		t.Fatalf("project ls should be empty after remove, got:\n%s", out)
	}
}

func TestCLI_TicketEdit_E2E(t *testing.T) {
	bin := buildCLIBinary(t)
	repo := initGitRepo(t)
	home := filepath.Join(t.TempDir(), "home")

	_, _ = runCLIOK(t, bin, repo, "-home", home, "init", "-name", "demo")
	_, _ = runCLIOK(t, bin, repo, "-home", home, "-project", "demo", "ticket", "create", "-title", "old title", "-desc", "old desc", "--label", "old-label")

	_, stderr, err := runCLI(t, bin, repo, "-home", home, "-project", "demo", "ticket", "edit", "--ticket", "1")
	if err == nil {
		t.Fatalf("ticket edit without --title/--desc should fail")
	}
	if !strings.Contains(stderr, "至少需要 --title、--desc、--label 或 --priority") {
		t.Fatalf("ticket edit missing-field hint not found:\n%s", stderr)
	}

	out, _ := runCLIOK(t, bin, repo, "-home", home, "-project", "demo", "ticket", "edit", "--ticket", "1", "--title", "new title")
	if strings.TrimSpace(out) != "t1 updated" {
		t.Fatalf("ticket edit text output unexpected: %q", out)
	}

	showOut, _ := runCLIOK(t, bin, repo, "-home", home, "-project", "demo", "ticket", "show", "--ticket", "1", "-o", "json")
	var showPayload struct {
		Schema      string `json:"schema"`
		Title       string `json:"title"`
		Description string `json:"description"`
		Label       string `json:"label"`
	}
	if err := json.Unmarshal([]byte(showOut), &showPayload); err != nil {
		t.Fatalf("unmarshal ticket show json failed: %v\nraw=%s", err, showOut)
	}
	if showPayload.Schema != "dalek.ticket.show.v1" {
		t.Fatalf("unexpected show schema: %s", showPayload.Schema)
	}
	if showPayload.Title != "new title" {
		t.Fatalf("title should be updated, got=%q", showPayload.Title)
	}
	if showPayload.Description != "old desc" {
		t.Fatalf("description should remain unchanged, got=%q", showPayload.Description)
	}
	if showPayload.Label != "old-label" {
		t.Fatalf("label should remain unchanged, got=%q", showPayload.Label)
	}

	editOut, _ := runCLIOK(t, bin, repo, "-home", home, "-project", "demo", "ticket", "edit", "--ticket", "1", "--desc", "new desc", "--label", "new-label", "-o", "json")
	var editPayload struct {
		Schema      string `json:"schema"`
		TicketID    uint   `json:"ticket_id"`
		Title       string `json:"title"`
		Description string `json:"description"`
		Label       string `json:"label"`
		Status      string `json:"status"`
	}
	if err := json.Unmarshal([]byte(editOut), &editPayload); err != nil {
		t.Fatalf("unmarshal ticket edit json failed: %v\nraw=%s", err, editOut)
	}
	if editPayload.Schema != "dalek.ticket.edit.v1" {
		t.Fatalf("unexpected edit schema: %s", editPayload.Schema)
	}
	if editPayload.TicketID != 1 {
		t.Fatalf("unexpected ticket id: %d", editPayload.TicketID)
	}
	if editPayload.Title != "new title" || editPayload.Description != "new desc" {
		t.Fatalf("unexpected edit payload: %+v", editPayload)
	}
	if editPayload.Label != "new-label" {
		t.Fatalf("label should be updated, got=%q", editPayload.Label)
	}
	if strings.TrimSpace(editPayload.Status) == "" {
		t.Fatalf("edit status should not be empty")
	}

	_, _ = runCLIOK(t, bin, repo, "-home", home, "-project", "demo", "ticket", "archive", "--ticket", "1")
	_, stderr, err = runCLI(t, bin, repo, "-home", home, "-project", "demo", "ticket", "edit", "--ticket", "1", "--title", "blocked title")
	if err == nil {
		t.Fatalf("editing archived ticket should fail")
	}
	if !strings.Contains(stderr, "已归档") {
		t.Fatalf("archived ticket edit hint missing:\n%s", stderr)
	}
}

func TestCLI_TicketCreateAndEdit_PriorityFlags(t *testing.T) {
	bin := buildCLIBinary(t)
	repo := initGitRepo(t)
	home := filepath.Join(t.TempDir(), "home")

	_, _ = runCLIOK(t, bin, repo, "-home", home, "init", "-name", "demo")
	_, _ = runCLIOK(t, bin, repo, "-home", home, "-project", "demo", "ticket", "create", "-title", "t-none", "-desc", "d-none")
	_, _ = runCLIOK(t, bin, repo, "-home", home, "-project", "demo", "ticket", "create", "-title", "t-low", "-desc", "d-low", "--priority", "low")
	_, _ = runCLIOK(t, bin, repo, "-home", home, "-project", "demo", "ticket", "create", "-title", "t-medium", "-desc", "d-medium", "--priority", "medium")
	_, _ = runCLIOK(t, bin, repo, "-home", home, "-project", "demo", "ticket", "create", "-title", "t-high", "-desc", "d-high", "--priority", "high")

	checkPriority := func(ticketID uint, wantPriority int, wantLabel string) {
		t.Helper()
		raw, _ := runCLIOK(t, bin, repo, "-home", home, "-project", "demo", "ticket", "show", "--ticket", fmt.Sprintf("%d", ticketID), "-o", "json")
		var payload struct {
			Priority      int    `json:"priority"`
			PriorityLabel string `json:"priority_label"`
		}
		if err := json.Unmarshal([]byte(raw), &payload); err != nil {
			t.Fatalf("unmarshal ticket show json failed: %v\nraw=%s", err, raw)
		}
		if payload.Priority != wantPriority || payload.PriorityLabel != wantLabel {
			t.Fatalf("unexpected priority for t%d: got=%d/%s want=%d/%s", ticketID, payload.Priority, payload.PriorityLabel, wantPriority, wantLabel)
		}
	}

	checkPriority(1, contracts.TicketPriorityNone, "none")
	checkPriority(2, contracts.TicketPriorityLow, "low")
	checkPriority(3, contracts.TicketPriorityMedium, "medium")
	checkPriority(4, contracts.TicketPriorityHigh, "high")

	_, _ = runCLIOK(t, bin, repo, "-home", home, "-project", "demo", "ticket", "edit", "--ticket", "1", "--priority", "high")
	checkPriority(1, contracts.TicketPriorityHigh, "high")

	_, _ = runCLIOK(t, bin, repo, "-home", home, "-project", "demo", "ticket", "edit", "--ticket", "2", "--title", "t-low-v2")
	checkPriority(2, contracts.TicketPriorityLow, "low")

	_, stderr, err := runCLI(t, bin, repo, "-home", home, "-project", "demo", "ticket", "create", "-title", "bad-priority", "-desc", "bad-priority", "--priority", "p0")
	if err == nil {
		t.Fatalf("ticket create should fail on invalid priority")
	}
	if !strings.Contains(stderr, "只支持 high、medium、low、none") {
		t.Fatalf("invalid create priority hint missing:\n%s", stderr)
	}

	_, stderr, err = runCLI(t, bin, repo, "-home", home, "-project", "demo", "ticket", "edit", "--ticket", "3", "--priority", "p0")
	if err == nil {
		t.Fatalf("ticket edit should fail on invalid priority")
	}
	if !strings.Contains(stderr, "只支持 high、medium、low、none") {
		t.Fatalf("invalid edit priority hint missing:\n%s", stderr)
	}
}

func TestCLI_TicketSetPriority_AndListOrder(t *testing.T) {
	bin := buildCLIBinary(t)
	repo := initGitRepo(t)
	home := filepath.Join(t.TempDir(), "home")

	_, _ = runCLIOK(t, bin, repo, "-home", home, "init", "-name", "demo")
	_, _ = runCLIOK(t, bin, repo, "-home", home, "-project", "demo", "ticket", "create", "-title", "t1", "-desc", "d1")
	_, _ = runCLIOK(t, bin, repo, "-home", home, "-project", "demo", "ticket", "create", "-title", "t2", "-desc", "d2")
	_, _ = runCLIOK(t, bin, repo, "-home", home, "-project", "demo", "ticket", "create", "-title", "t3", "-desc", "d3")

	_, _ = runCLIOK(t, bin, repo, "-home", home, "-project", "demo", "ticket", "set-priority", "--ticket", "3", "--priority", "high")
	_, _ = runCLIOK(t, bin, repo, "-home", home, "-project", "demo", "ticket", "set-priority", "--ticket", "1", "--priority", "medium")
	_, _ = runCLIOK(t, bin, repo, "-home", home, "-project", "demo", "ticket", "set-priority", "--ticket", "2", "--priority", "low")

	out, _ := runCLIOK(t, bin, repo, "-home", home, "-project", "demo", "ticket", "ls", "-o", "json")
	var payload struct {
		Tickets []struct {
			ID            uint   `json:"id"`
			Priority      int    `json:"priority"`
			PriorityLabel string `json:"priority_label"`
		} `json:"tickets"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("unmarshal ticket ls json failed: %v\nraw=%s", err, out)
	}
	if len(payload.Tickets) != 3 {
		t.Fatalf("expected 3 tickets, got %d", len(payload.Tickets))
	}

	gotIDs := []uint{payload.Tickets[0].ID, payload.Tickets[1].ID, payload.Tickets[2].ID}
	wantIDs := []uint{3, 1, 2}
	for i := range wantIDs {
		if gotIDs[i] != wantIDs[i] {
			t.Fatalf("unexpected order: got=%v want=%v", gotIDs, wantIDs)
		}
	}
	if payload.Tickets[0].Priority != contracts.TicketPriorityHigh || payload.Tickets[0].PriorityLabel != "high" {
		t.Fatalf("unexpected high priority payload: %+v", payload.Tickets[0])
	}
	if payload.Tickets[1].Priority != contracts.TicketPriorityMedium || payload.Tickets[1].PriorityLabel != "medium" {
		t.Fatalf("unexpected medium priority payload: %+v", payload.Tickets[1])
	}
	if payload.Tickets[2].Priority != contracts.TicketPriorityLow || payload.Tickets[2].PriorityLabel != "low" {
		t.Fatalf("unexpected low priority payload: %+v", payload.Tickets[2])
	}

	_, stderr, err := runCLI(t, bin, repo, "-home", home, "-project", "demo", "ticket", "set-priority", "--ticket", "1", "--priority", "p0")
	if err == nil {
		t.Fatalf("set-priority should fail on invalid value")
	}
	if !strings.Contains(stderr, "只支持 high、medium、low、none") {
		t.Fatalf("invalid priority hint missing:\n%s", stderr)
	}
}

func TestCLI_TicketStopAll_PreservesRuntimeLogPath(t *testing.T) {
	bin := buildCLIBinary(t)
	repo := initGitRepo(t)
	home := filepath.Join(t.TempDir(), "home")

	_, _ = runCLIOK(t, bin, repo, "-home", home, "init", "-name", "demo")
	_, _ = runCLIOK(t, bin, repo, "-home", home, "-project", "demo", "ticket", "create", "-title", "stop-all-1", "-desc", "stop-all-1")
	_, _ = runCLIOK(t, bin, repo, "-home", home, "-project", "demo", "ticket", "create", "-title", "stop-all-2", "-desc", "stop-all-2")

	type startResp struct {
		WorkerID uint `json:"worker_id"`
	}
	startRaw1, _ := runCLIOK(t, bin, repo, "-home", home, "-project", "demo", "ticket", "start", "--ticket", "1", "-o", "json")
	startRaw2, _ := runCLIOK(t, bin, repo, "-home", home, "-project", "demo", "ticket", "start", "--ticket", "2", "-o", "json")
	var start1, start2 startResp
	if err := json.Unmarshal([]byte(strings.TrimSpace(startRaw1)), &start1); err != nil {
		t.Fatalf("decode ticket start #1 failed: %v\nraw=%s", err, startRaw1)
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(startRaw2)), &start2); err != nil {
		t.Fatalf("decode ticket start #2 failed: %v\nraw=%s", err, startRaw2)
	}
	if start1.WorkerID == 0 || start2.WorkerID == 0 {
		t.Fatalf("ticket start should return non-zero worker id, got=%d/%d", start1.WorkerID, start2.WorkerID)
	}

	stopRaw, _ := runCLIOK(t, bin, repo, "-home", home, "-project", "demo", "ticket", "stop", "--all", "-o", "json")
	var stopResp struct {
		Schema  string `json:"schema"`
		Count   int    `json:"count"`
		Stopped []struct {
			WorkerID uint `json:"worker_id"`
		} `json:"stopped"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(stopRaw)), &stopResp); err != nil {
		t.Fatalf("decode ticket stop --all failed: %v\nraw=%s", err, stopRaw)
	}
	if stopResp.Schema != "dalek.ticket.stop.v1" {
		t.Fatalf("unexpected schema: %q", stopResp.Schema)
	}
	if stopResp.Count != 2 || len(stopResp.Stopped) != 2 {
		t.Fatalf("stop --all should stop 2 workers, count=%d stopped=%d raw=%s", stopResp.Count, len(stopResp.Stopped), stopRaw)
	}

	h, err := app.OpenHome(home)
	if err != nil {
		t.Fatalf("OpenHome failed: %v", err)
	}
	project, err := h.OpenProjectByName("demo")
	if err != nil {
		t.Fatalf("OpenProjectByName failed: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, wid := range []uint{start1.WorkerID, start2.WorkerID} {
		w, err := project.WorkerByID(ctx, wid)
		if err != nil {
			t.Fatalf("WorkerByID(%d) failed: %v", wid, err)
		}
		if w == nil {
			t.Fatalf("WorkerByID(%d) returned nil", wid)
		}
		if w.Status != contracts.WorkerStopped {
			t.Fatalf("worker #%d should be stopped, got=%s", wid, w.Status)
		}
		if strings.TrimSpace(w.LogPath) == "" {
			t.Fatalf("worker #%d log_path should not be empty after stop --all", wid)
		}
	}
}

func TestCLI_GatewayChat_ListTickets(t *testing.T) {
	bin := buildCLIBinary(t)
	repo := initGitRepo(t)
	home := filepath.Join(t.TempDir(), "home")

	installFakeClaudeForE2E(t)
	_, _ = runCLIOK(t, bin, repo, "-home", home, "init", "-name", "demo")
	_, _ = runCLIOK(t, bin, repo, "-home", home, "-project", "demo", "ticket", "create", "-title", "gateway ticket", "-desc", "gateway ticket description")
	wsURL := startGatewayDaemonForE2E(t, home)

	out, _ := runCLIOK(t, bin, repo, "-home", home, "-project", "demo", "gateway", "chat", "-ws-url", wsURL, "-text", "请给我 ticket 列表")
	if !strings.Contains(out, "正在查询 ticket 列表。") {
		t.Fatalf("gateway chat output unexpected:\n%s", out)
	}
}

func TestCLI_GatewayChat_FailedJobReturnsNonZero(t *testing.T) {
	bin := buildCLIBinary(t)
	repo := initGitRepo(t)
	home := filepath.Join(t.TempDir(), "home")

	installFakeClaudeForE2EEmpty(t)
	_, _ = runCLIOK(t, bin, repo, "-home", home, "init", "-name", "demo")
	wsURL := startGatewayDaemonForE2E(t, home)

	stdout, stderr, err := runCLI(t, bin, repo, "-home", home, "-project", "demo", "gateway", "chat", "-ws-url", wsURL, "-text", "你好")
	if err == nil {
		t.Fatalf("expected gateway chat to fail when agent has no response, got success\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
	}
	if !strings.Contains(stderr, "job_status=failed") {
		t.Fatalf("stderr should include failed job status, got:\n%s", stderr)
	}
	if !strings.Contains(stderr, "job_error_type=") {
		t.Fatalf("stderr should include job error type, got:\n%s", stderr)
	}
}

func TestCLI_GatewayChat_CodexJSONL(t *testing.T) {
	bin := buildCLIBinary(t)
	repo := initGitRepo(t)
	home := filepath.Join(t.TempDir(), "home")

	installFakeCodexForE2E(t)
	t.Setenv("DALEK_GATEWAY_AGENT_PROVIDER", "codex")
	t.Setenv("DALEK_GATEWAY_AGENT_MODEL", "gpt-5-codex-test")

	_, _ = runCLIOK(t, bin, repo, "-home", home, "init", "-name", "demo")
	wsURL := startGatewayDaemonForE2E(t, home)

	out, _ := runCLIOK(t, bin, repo, "-home", home, "-project", "demo", "gateway", "chat", "-ws-url", wsURL, "-text", "hello codex")
	if !strings.Contains(out, "codex final reply") {
		t.Fatalf("gateway chat codex output unexpected:\n%s", out)
	}
}

func TestCLI_GatewayChat_DaemonUnavailable(t *testing.T) {
	bin := buildCLIBinary(t)
	repo := initGitRepo(t)
	home := filepath.Join(t.TempDir(), "home")

	installFakeClaudeForE2E(t)
	_, _ = runCLIOK(t, bin, repo, "-home", home, "init", "-name", "demo")

	stdout, stderr, err := runCLI(t, bin, repo, "-home", home, "-project", "demo", "gateway", "chat", "-ws-url", "ws://127.0.0.1:1/ws", "-text", "hello")
	if err == nil {
		t.Fatalf("expected gateway chat to fail when daemon is unavailable, got success\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
	}
	if !strings.Contains(stderr, "gateway daemon") || !strings.Contains(stderr, "请先执行") {
		t.Fatalf("stderr should tell user to start daemon, got:\n%s", stderr)
	}
}

func TestCLI_GatewaySend_DaemonUnavailable(t *testing.T) {
	bin := buildCLIBinary(t)
	repo := initGitRepo(t)
	home := filepath.Join(t.TempDir(), "home")

	_, _ = runCLIOK(t, bin, repo, "-home", home, "init", "-name", "demo")
	configureDaemonInternalListenForE2E(t, home, "http://127.0.0.1:1")

	stdout, stderr, err := runCLI(t, bin, repo, "-home", home, "-project", "demo", "gateway", "send", "--text", "部署完成")
	if err == nil {
		t.Fatalf("expected gateway send fail when daemon unavailable\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
	}
	if strings.TrimSpace(stdout) != "" {
		t.Fatalf("stdout should be empty for text-mode runtime error, got:\n%s", stdout)
	}
	if !strings.Contains(stderr, "gateway send 失败（daemon 不在线）") {
		t.Fatalf("stderr should contain daemon unavailable error:\n%s", stderr)
	}
	if !strings.Contains(stderr, "请先执行 dalek daemon start 后重试") {
		t.Fatalf("stderr should contain daemon start guide:\n%s", stderr)
	}
}

func TestCLI_GatewaySend_ProxyToDaemonInternalAPI(t *testing.T) {
	bin := buildCLIBinary(t)
	repo := initGitRepo(t)
	home := filepath.Join(t.TempDir(), "home")

	_, _ = runCLIOK(t, bin, repo, "-home", home, "init", "-name", "demo")

	var called bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/send" {
			http.NotFound(w, r)
			return
		}
		var payload struct {
			Project string `json:"project"`
			Text    string `json:"text"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode payload failed: %v", err)
		}
		if strings.TrimSpace(payload.Project) != "demo" {
			t.Fatalf("unexpected project: %q", payload.Project)
		}
		if strings.TrimSpace(payload.Text) != "部署完成" {
			t.Fatalf("unexpected text: %q", payload.Text)
		}
		called = true
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"schema":    gatewaySendResponseSchemaV1,
			"project":   "demo",
			"text":      "部署完成",
			"delivered": 1,
			"failed":    0,
			"results": []map[string]any{
				{
					"binding_id":      11,
					"conversation_id": 22,
					"message_id":      33,
					"outbox_id":       44,
					"chat_id":         "chat-send-1",
					"status":          "delivered",
				},
			},
		})
	}))
	t.Cleanup(server.Close)

	configureDaemonInternalListenForE2E(t, home, server.URL)

	out, _ := runCLIOK(t, bin, repo, "-home", home, "-project", "demo", "gateway", "send", "--text", "部署完成", "-o", "json")

	var resp gatewaySendResponse
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &resp); err != nil {
		t.Fatalf("decode gateway send response failed: %v\nraw=%s", err, out)
	}
	if resp.Schema != gatewaySendResponseSchemaV1 {
		t.Fatalf("unexpected schema: %q", resp.Schema)
	}
	if resp.Delivered != 1 || resp.Failed != 0 {
		t.Fatalf("unexpected delivery stats: delivered=%d failed=%d", resp.Delivered, resp.Failed)
	}
	if len(resp.Results) != 1 {
		t.Fatalf("unexpected results len: %d", len(resp.Results))
	}
	if strings.TrimSpace(resp.Results[0].ChatID) != "chat-send-1" {
		t.Fatalf("unexpected chat id in result: %q", resp.Results[0].ChatID)
	}
	if !called {
		t.Fatalf("expected daemon /api/send to be called")
	}
}

func TestCLI_GatewayCLITestChannel_SimulateFeishuFlow(t *testing.T) {
	bin := buildCLIBinary(t)
	clientBin := buildCLITestChannelClientBinary(t)
	repo := initGitRepo(t)
	home := filepath.Join(t.TempDir(), "home")

	installFakeClaudeForE2E(t)
	_, _ = runCLIOK(t, bin, repo, "-home", home, "init", "-name", "demo")

	_, channelURL := startGatewayDaemonWithCLITestChannelForE2E(t, home)
	chatID := "cli-test-chat-1"
	senderID := "cli-test-user-1"

	out := runCLITestChannelClientOK(t, clientBin, channelURL, chatID, senderID, "/bind demo")
	t.Logf("cli-test-channel /bind -> %s", out)
	if !strings.Contains(out, "已绑定到 project demo") {
		t.Fatalf("bind output unexpected:\n%s", out)
	}

	out = runCLITestChannelClientOK(t, clientBin, channelURL, chatID, senderID, "/new")
	t.Logf("cli-test-channel /new -> %s", out)
	if !strings.Contains(out, "已重置会话") {
		t.Fatalf("new output unexpected:\n%s", out)
	}

	out = runCLITestChannelClientOK(t, clientBin, channelURL, chatID, senderID, "请给我 ticket 列表")
	t.Logf("cli-test-channel message -> %s", out)
	if !strings.Contains(out, "正在查询 ticket 列表。") {
		t.Fatalf("forward output unexpected:\n%s", out)
	}

	out = runCLITestChannelClientOK(t, clientBin, channelURL, chatID, senderID, "/unbind")
	t.Logf("cli-test-channel /unbind -> %s", out)
	if !strings.Contains(out, "已解绑") {
		t.Fatalf("unbind output unexpected:\n%s", out)
	}

	out = runCLITestChannelClientOK(t, clientBin, channelURL, chatID, senderID, "还在吗")
	t.Logf("cli-test-channel message-after-unbind -> %s", out)
	if !strings.Contains(out, "本群尚未绑定项目") {
		t.Fatalf("unbound hint output unexpected:\n%s", out)
	}
}

func startGatewayDaemonForE2E(t *testing.T, homeDir string) string {
	t.Helper()
	wsURL, _ := startGatewayDaemonWithCLITestChannelForE2E(t, homeDir)
	return wsURL
}

func startGatewayDaemonWithCLITestChannelForE2E(t *testing.T, homeDir string) (string, string) {
	t.Helper()

	h, err := app.OpenHome(homeDir)
	if err != nil {
		t.Fatalf("OpenHome failed: %v", err)
	}

	project, err := h.OpenProjectByName("demo")
	if err != nil {
		t.Fatalf("OpenProjectByName failed: %v", err)
	}
	gatewayDB, err := h.OpenGatewayDB()
	if err != nil {
		t.Fatalf("OpenGatewayDB failed: %v", err)
	}
	resolver := newHomeProjectResolver(h)
	gateway, err := channelsvc.NewGateway(gatewayDB, resolver, channelsvc.GatewayOptions{QueueDepth: 32})
	if err != nil {
		t.Fatalf("NewGateway failed: %v", err)
	}

	wsPath, wsHandler := newGatewayWSServerHandler(project, gatewayWSServerOptions{
		Path:               "/ws",
		DefaultSender:      "ws.user",
		ConversationPrefix: "gw",
		TurnTimeout:        project.GatewayTurnTimeout(),
		InboxPollInterval:  2 * time.Second,
		InboxLimit:         20,
	})
	mux := http.NewServeMux()
	mux.HandleFunc(wsPath, wsHandler)
	// 挂载飞书路由，确保 daemon 路由形态与生产一致。
	feishuPath := "/feishu/webhook"
	mux.HandleFunc(feishuPath, newGatewayFeishuWebhookHandler(gateway, resolver, &noopFeishuSender{}, gatewayFeishuHandlerOptions{
		Adapter: "im.feishu",
	}))
	// 仅测试内使用的本地无认证通道，不对外开放到正式 gateway serve。
	mux.HandleFunc("/cli/test/channel", newGatewayCLITestChannelHandlerForE2E(gateway, resolver))

	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + wsPath
	channelURL := strings.TrimRight(server.URL, "/") + "/cli/test/channel"
	return wsURL, channelURL
}

func buildCLITestChannelClientBinary(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "gateway_cli_test_client")
	_, stderr, err := runCmd(t, ".", "go", "build", "-o", bin, "../../tools/gateway_cli_test_client")
	if err != nil {
		t.Fatalf("build cli test channel client failed:\n%s\nerr=%v", stderr, err)
	}
	return bin
}

func runCLITestChannelClientOK(t *testing.T, clientBin, channelURL, chatID, senderID, text string) string {
	t.Helper()
	stdout, stderr, err := runCmd(t, ".", clientBin,
		"-url", channelURL,
		"-chat-id", chatID,
		"-sender", senderID,
		"-text", text,
	)
	if err != nil {
		t.Fatalf("cli test channel client failed:\nstderr:\n%s\nerr=%v", stderr, err)
	}
	return strings.TrimSpace(stdout)
}

type cliTestChannelRequest struct {
	ChatID   string `json:"chat_id"`
	SenderID string `json:"sender_id"`
	Text     string `json:"text"`
}

type fakeFeishuGatewaySendMessage struct {
	ReceiveID string `json:"receive_id"`
	MsgType   string `json:"msg_type"`
	Content   string `json:"content"`
}

type fakeFeishuGatewaySendCall struct {
	authorization string
	receiveIDType string
	payload       fakeFeishuGatewaySendMessage
}

type fakeFeishuGatewaySendServer struct {
	baseURL string

	mu           sync.Mutex
	tokenCalls   int
	messageCalls []fakeFeishuGatewaySendCall
}

type fakeFeishuGatewaySendSnapshot struct {
	tokenCalls   int
	messageCalls []fakeFeishuGatewaySendCall
}

func startFakeFeishuServerForGatewaySendE2E(t *testing.T) *fakeFeishuGatewaySendServer {
	t.Helper()

	const fakeToken = "token-gateway-send-e2e"
	server := &fakeFeishuGatewaySendServer{}
	mux := http.NewServeMux()
	mux.HandleFunc("/open-apis/auth/v3/tenant_access_token/internal", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		server.mu.Lock()
		server.tokenCalls++
		server.mu.Unlock()
		writeGatewayJSON(w, http.StatusOK, map[string]any{
			"code":                0,
			"msg":                 "ok",
			"tenant_access_token": fakeToken,
			"expire":              7200,
		})
	})
	mux.HandleFunc("/open-apis/im/v1/messages", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var payload fakeFeishuGatewaySendMessage
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			http.Error(w, "invalid json", http.StatusBadRequest)
			return
		}
		call := fakeFeishuGatewaySendCall{
			authorization: strings.TrimSpace(r.Header.Get("Authorization")),
			receiveIDType: strings.TrimSpace(r.URL.Query().Get("receive_id_type")),
			payload:       payload,
		}
		server.mu.Lock()
		server.messageCalls = append(server.messageCalls, call)
		server.mu.Unlock()
		writeGatewayJSON(w, http.StatusOK, map[string]any{
			"code": 0,
			"msg":  "ok",
			"data": map[string]any{
				"message_id": "om_gateway_send_e2e",
			},
		})
	})

	httptestServer := httptest.NewServer(mux)
	t.Cleanup(httptestServer.Close)
	server.baseURL = strings.TrimRight(httptestServer.URL, "/")
	return server
}

func (s *fakeFeishuGatewaySendServer) snapshot() fakeFeishuGatewaySendSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return fakeFeishuGatewaySendSnapshot{
		tokenCalls:   s.tokenCalls,
		messageCalls: append([]fakeFeishuGatewaySendCall(nil), s.messageCalls...),
	}
}

type cliTestChannelCaptureSender struct {
	reply string
}

func (s *cliTestChannelCaptureSender) SendText(ctx context.Context, chatID, text string) error {
	_ = ctx
	_ = chatID
	s.reply = strings.TrimSpace(text)
	return nil
}

func (s *cliTestChannelCaptureSender) SendCard(ctx context.Context, chatID, title, markdown string) error {
	_ = title
	return s.SendText(ctx, chatID, markdown)
}

func (s *cliTestChannelCaptureSender) SendCardInteractive(ctx context.Context, chatID, cardJSON string) (string, error) {
	_ = ctx
	_ = chatID
	_ = cardJSON
	return "", nil
}

func (s *cliTestChannelCaptureSender) PatchCard(ctx context.Context, messageID, cardJSON string) error {
	_ = ctx
	_ = messageID
	_ = cardJSON
	return nil
}

func (s *cliTestChannelCaptureSender) GetUserName(ctx context.Context, userID string) (string, error) {
	_ = ctx
	_ = userID
	return "", nil
}

func (s *cliTestChannelCaptureSender) GetBotOpenID(ctx context.Context) (string, error) {
	_ = ctx
	return "", nil
}

func newGatewayCLITestChannelHandlerForE2E(gateway *channelsvc.Gateway, resolver channelsvc.ProjectResolver) http.HandlerFunc {
	const adapter = "im.cli.test"
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req cliTestChannelRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeGatewayJSON(w, http.StatusBadRequest, map[string]any{"code": 1, "msg": "invalid json"})
			return
		}
		chatID := strings.TrimSpace(req.ChatID)
		text := strings.TrimSpace(req.Text)
		senderID := strings.TrimSpace(req.SenderID)
		if chatID == "" || text == "" {
			writeGatewayJSON(w, http.StatusBadRequest, map[string]any{"code": 1, "msg": "chat_id/text required"})
			return
		}
		if senderID == "" {
			senderID = "cli.test.user"
		}

		cmdSender := &cliTestChannelCaptureSender{}
		if handled := tryHandleFeishuBindCommand(context.Background(), gateway, resolver, cmdSender, adapter, chatID, text); handled {
			writeGatewayJSON(w, http.StatusOK, map[string]any{"code": 0, "reply": cmdSender.reply})
			return
		}
		if handled := tryHandleFeishuUnbindCommand(context.Background(), gateway, cmdSender, adapter, chatID, text); handled {
			writeGatewayJSON(w, http.StatusOK, map[string]any{"code": 0, "reply": cmdSender.reply})
			return
		}
		if handled := tryHandleFeishuNewCommand(context.Background(), gateway, resolver, cmdSender, adapter, chatID, text); handled {
			writeGatewayJSON(w, http.StatusOK, map[string]any{"code": 0, "reply": cmdSender.reply})
			return
		}
		if handled := tryHandleFeishuInterruptCommand(context.Background(), gateway, resolver, cmdSender, adapter, chatID, text); handled {
			writeGatewayJSON(w, http.StatusOK, map[string]any{"code": 0, "reply": cmdSender.reply})
			return
		}

		projectName, err := gateway.LookupBoundProject(context.Background(), contracts.ChannelTypeIM, adapter, chatID)
		if err != nil {
			writeGatewayJSON(w, http.StatusOK, map[string]any{"code": 1, "msg": "lookup failed: " + strings.TrimSpace(err.Error())})
			return
		}
		if strings.TrimSpace(projectName) == "" {
			writeGatewayJSON(w, http.StatusOK, map[string]any{"code": 0, "reply": buildFeishuUnboundHint(resolver)})
			return
		}

		replyCh := make(chan string, 1)
		errCh := make(chan error, 1)
		err = gateway.Submit(context.Background(), channelsvc.GatewayInboundRequest{
			ProjectName:    projectName,
			PeerProjectKey: chatID,
			Envelope: contracts.InboundEnvelope{
				Schema:             contracts.ChannelInboundSchemaV1,
				ChannelType:        contracts.ChannelTypeIM,
				Adapter:            adapter,
				PeerConversationID: chatID,
				PeerMessageID:      fmt.Sprintf("cli-test-%d", time.Now().UnixNano()),
				SenderID:           senderID,
				Text:               text,
				ReceivedAt:         time.Now().UTC().Format(time.RFC3339),
			},
			Callback: func(res channelsvc.ProcessResult, runErr error) {
				if runErr != nil {
					errCh <- runErr
					return
				}
				reply := strings.TrimSpace(res.ReplyText)
				if reply == "" {
					reply = strings.TrimSpace(res.JobError)
				}
				if reply == "" {
					reply = "(empty reply)"
				}
				replyCh <- reply
			},
		})
		if err != nil {
			msg := strings.TrimSpace(err.Error())
			if err == channelsvc.ErrInboundQueueFull {
				msg = "排队中，请稍后再试。"
			}
			writeGatewayJSON(w, http.StatusOK, map[string]any{"code": 1, "msg": msg})
			return
		}

		select {
		case reply := <-replyCh:
			writeGatewayJSON(w, http.StatusOK, map[string]any{"code": 0, "project": projectName, "reply": reply})
		case runErr := <-errCh:
			writeGatewayJSON(w, http.StatusOK, map[string]any{"code": 1, "msg": strings.TrimSpace(runErr.Error())})
		case <-time.After(8 * time.Second):
			writeGatewayJSON(w, http.StatusOK, map[string]any{"code": 1, "msg": "wait callback timeout"})
		}
	}
}

func installFakeClaudeForE2E(t *testing.T) {
	t.Helper()
	binDir := t.TempDir()
	path := filepath.Join(binDir, "claude")
	script := `#!/usr/bin/env bash
set -euo pipefail
session_id=""
args=("$@")
for ((i=0;i<${#args[@]};i++)); do
  case "${args[$i]}" in
    --session-id|--resume)
      if (( i + 1 < ${#args[@]} )); then
        session_id="${args[$((i+1))]}"
      fi
      ;;
  esac
done
prompt=""
if (( ${#args[@]} > 0 )); then
  prompt="${args[$((${#args[@]}-1))]}"
fi
python3 - "$prompt" "$session_id" <<'PY'
import datetime
import json
import os
import re
import sqlite3
import sys

text = (sys.argv[1] or "").strip()
session_id = (sys.argv[2] or "").strip()
if not session_id:
    session_id = "sess-e2e-1"
lower = text.lower()
if ("创建" in text) or ("新建" in text) or ("新增" in text) or ("create" in lower):
    title = text
    m = re.search(r'(?:创建|新建|新增|create)\s*(?:ticket|工单|任务)?\s*[:：]\s*([^"\n]+)', text, flags=re.I)
    if m:
        title = m.group(1).strip()
    if not title:
        title = "未命名 ticket"
    db_path = (os.environ.get("DALEK_FAKE_DB_PATH") or "").strip()
    if db_path:
        now = datetime.datetime.utcnow().strftime("%Y-%m-%d %H:%M:%S")
        conn = None
        try:
            conn = sqlite3.connect(db_path)
            conn.execute(
                "INSERT INTO tickets(created_at, updated_at, title, description, workflow_status, priority) VALUES(?,?,?,?,?,?)",
                (now, now, title, "", "backlog", 0),
            )
            conn.commit()
        except Exception as e:
            print(f"[fake-agent] create ticket failed: {e}", file=sys.stderr)
        finally:
            if conn is not None:
                conn.close()
    reply = f"正在创建 ticket：{title}。"
elif ("merge" in lower) or ("合并" in text):
    reply = "正在查询 merge 列表。"
else:
    m = re.search(r'(?:ticket|t|工单|任务)\s*#?\s*(\d+)', text, flags=re.I)
    if m:
        tid = int(m.group(1))
        reply = f"正在查询 t{tid} 的详情。"
    elif ("ticket" in lower) or ("工单" in text) or ("任务" in text) or ("列表" in text):
        reply = "正在查询 ticket 列表。"
    else:
        reply = "你好，我是 project manager agent。"
print(json.dumps({
    "type": "result",
    "subtype": "success",
    "duration_ms": 1,
    "duration_api_ms": 1,
    "is_error": False,
    "num_turns": 1,
    "session_id": session_id,
    "result": reply
}, ensure_ascii=False))
PY
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake claude script failed: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("DALEK_GATEWAY_AGENT_MODE", "cli")
}

func installFakeClaudeForE2EEmpty(t *testing.T) {
	t.Helper()
	binDir := t.TempDir()
	path := filepath.Join(binDir, "claude")
	script := `#!/usr/bin/env bash
set -euo pipefail
exit 0
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake claude empty script failed: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("DALEK_GATEWAY_AGENT_MODE", "cli")
}

func installFakeCodexForE2E(t *testing.T) {
	t.Helper()
	binDir := t.TempDir()
	path := filepath.Join(binDir, "codex")
	script := `#!/usr/bin/env bash
set -euo pipefail
prompt="${@: -1}"
python3 - "$prompt" <<'PY'
import json
import sys

text = (sys.argv[1] or "").strip()
events = [
    {"type": "item.completed", "item": {"id": "item_0", "type": "reasoning", "text": "codex thinking"}},
    {"type": "item.completed", "item": {"id": "item_1", "type": "agent_message", "text": f"codex final reply: {text}"}},
]
for ev in events:
    print(json.dumps(ev, ensure_ascii=False))
PY
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake codex script failed: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
}
