package main

import (
	"dalek/internal/app"
	"dalek/internal/contracts"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func seedRunningTaskRunForCancelE2E(t *testing.T, homeDir string, runID uint, requestID string) {
	t.Helper()
	h, err := app.OpenHome(homeDir)
	if err != nil {
		t.Fatalf("OpenHome failed: %v", err)
	}
	p, err := h.OpenProjectByName("demo")
	if err != nil {
		t.Fatalf("OpenProjectByName failed: %v", err)
	}
	db, err := p.OpenDBForTest()
	if err != nil {
		t.Fatalf("OpenDBForTest failed: %v", err)
	}
	now := time.Now()
	run := contracts.TaskRun{
		ID:                 runID,
		OwnerType:          contracts.TaskOwnerWorker,
		TaskType:           contracts.TaskTypeDeliverTicket,
		ProjectKey:         "demo",
		TicketID:           1,
		WorkerID:           1,
		SubjectType:        "ticket",
		SubjectID:          "1",
		RequestID:          strings.TrimSpace(requestID),
		OrchestrationState: contracts.TaskRunning,
		RunnerID:           "daemon_runner_e2e",
		Attempt:            1,
		StartedAt:          &now,
	}
	if err := db.Create(&run).Error; err != nil {
		t.Fatalf("seed task run failed: %v", err)
	}
}

func loadTaskRunForCancelE2E(t *testing.T, homeDir string, runID uint) contracts.TaskRun {
	t.Helper()
	h, err := app.OpenHome(homeDir)
	if err != nil {
		t.Fatalf("OpenHome failed: %v", err)
	}
	p, err := h.OpenProjectByName("demo")
	if err != nil {
		t.Fatalf("OpenProjectByName failed: %v", err)
	}
	db, err := p.OpenDBForTest()
	if err != nil {
		t.Fatalf("OpenDBForTest failed: %v", err)
	}
	var run contracts.TaskRun
	if err := db.Where("id = ?", runID).First(&run).Error; err != nil {
		t.Fatalf("load task run failed: %v", err)
	}
	return run
}

func TestCLI_TaskCancel_UsesDaemonAPIThenMarksDB(t *testing.T) {
	bin := buildCLIBinary(t)
	repoRoot := initGitRepo(t)
	home := filepath.Join(t.TempDir(), "home")
	prepareDemoProjectWithOneTicket(t, bin, repoRoot, home)

	runID := uint(9001)
	seedRunningTaskRunForCancelE2E(t, home, runID, "task-cancel-e2e-online")

	var cancelCalled atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/api/ticket-loops/1/cancel" && r.URL.Query().Get("project") == "demo" {
			cancelCalled.Store(true)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ticket_id":  1,
				"found":      true,
				"canceled":   true,
				"project":    "demo",
				"request_id": "req-cancel-online",
				"reason":     "cancel signal sent",
			})
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(server.Close)

	configureDaemonInternalListenForE2E(t, home, server.URL)

	stdout, _ := runCLIOK(
		t,
		bin,
		repoRoot,
		"-home", home,
		"-project", "demo",
		"task", "cancel",
		"--id", "9001",
		"-o", "json",
	)
	var resp struct {
		Schema    string `json:"schema"`
		RunID     uint   `json:"run_id"`
		Found     bool   `json:"found"`
		Canceled  bool   `json:"canceled"`
		FromState string `json:"from_state"`
		ToState   string `json:"to_state"`
		Warning   string `json:"warning"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &resp); err != nil {
		t.Fatalf("decode response failed: %v raw=%s", err, stdout)
	}
	if resp.Schema != "dalek.task.cancel.v1" {
		t.Fatalf("unexpected schema: %q", resp.Schema)
	}
	if resp.RunID != runID || !resp.Found || !resp.Canceled {
		t.Fatalf("unexpected cancel response: %+v", resp)
	}
	if strings.TrimSpace(resp.Warning) != "" {
		t.Fatalf("unexpected warning for daemon-online cancel: %q", resp.Warning)
	}
	if !cancelCalled.Load() {
		t.Fatalf("expected daemon cancel endpoint called")
	}
	run := loadTaskRunForCancelE2E(t, home, runID)
	if run.OrchestrationState != contracts.TaskCanceled {
		t.Fatalf("expected run canceled in DB, got=%s", run.OrchestrationState)
	}
}

func TestCLI_TaskCancel_DaemonUnavailableFallbackWarnsAndMarksDB(t *testing.T) {
	bin := buildCLIBinary(t)
	repoRoot := initGitRepo(t)
	home := filepath.Join(t.TempDir(), "home")
	prepareDemoProjectWithOneTicket(t, bin, repoRoot, home)

	runID := uint(9002)
	seedRunningTaskRunForCancelE2E(t, home, runID, "task-cancel-e2e-offline")
	configureDaemonInternalListenForE2E(t, home, "http://127.0.0.1:1")

	stdout, _ := runCLIOK(
		t,
		bin,
		repoRoot,
		"-home", home,
		"-project", "demo",
		"task", "cancel",
		"--id", "9002",
		"-o", "json",
	)
	var resp struct {
		Schema   string `json:"schema"`
		RunID    uint   `json:"run_id"`
		Found    bool   `json:"found"`
		Canceled bool   `json:"canceled"`
		Warning  string `json:"warning"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &resp); err != nil {
		t.Fatalf("decode response failed: %v raw=%s", err, stdout)
	}
	if resp.Schema != "dalek.task.cancel.v1" {
		t.Fatalf("unexpected schema: %q", resp.Schema)
	}
	if resp.RunID != runID || !resp.Found || !resp.Canceled {
		t.Fatalf("unexpected cancel response: %+v", resp)
	}
	if !strings.Contains(resp.Warning, "已降级为仅标记数据库") {
		t.Fatalf("expected fallback warning in response, got=%q", resp.Warning)
	}
	run := loadTaskRunForCancelE2E(t, home, runID)
	if run.OrchestrationState != contracts.TaskCanceled {
		t.Fatalf("expected run canceled in DB, got=%s", run.OrchestrationState)
	}
}
