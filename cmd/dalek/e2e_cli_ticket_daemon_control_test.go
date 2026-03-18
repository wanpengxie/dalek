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

func seedWorkerForTicketControlE2E(t *testing.T, homeDir string, ticketID, workerID uint, status contracts.WorkerStatus) {
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
	worker := contracts.Worker{
		ID:           workerID,
		TicketID:     ticketID,
		Status:       status,
		WorktreePath: "/tmp/ticket-control-worker",
		Branch:       "ts/demo/t1",
		LogPath:      "/tmp/ticket-control-worker.log",
		StartedAt:    &now,
	}
	if err := db.Create(&worker).Error; err != nil {
		t.Fatalf("seed worker failed: %v", err)
	}
}

func seedActiveWorkerRunForTicketControlE2E(t *testing.T, homeDir string, runID, ticketID, workerID uint, requestID string) {
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
		TicketID:           ticketID,
		WorkerID:           workerID,
		SubjectType:        "ticket",
		SubjectID:          "1",
		RequestID:          strings.TrimSpace(requestID),
		OrchestrationState: contracts.TaskRunning,
		RunnerID:           "daemon_runner_ticket_control",
		Attempt:            1,
		StartedAt:          &now,
	}
	if err := db.Create(&run).Error; err != nil {
		t.Fatalf("seed task run failed: %v", err)
	}
}

func loadWorkerForTicketControlE2E(t *testing.T, homeDir string, workerID uint) contracts.Worker {
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
	var worker contracts.Worker
	if err := db.First(&worker, workerID).Error; err != nil {
		t.Fatalf("load worker failed: %v", err)
	}
	return worker
}

func loadTaskRunForTicketControlE2E(t *testing.T, homeDir string, runID uint) contracts.TaskRun {
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
	if err := db.First(&run, runID).Error; err != nil {
		t.Fatalf("load task run failed: %v", err)
	}
	return run
}

func TestCLI_TicketStop_UsesDaemonTicketLoopCancel(t *testing.T) {
	bin := buildCLIBinary(t)
	repoRoot := initGitRepo(t)
	home := filepath.Join(t.TempDir(), "home")
	prepareDemoProjectWithOneTicket(t, bin, repoRoot, home)
	seedWorkerForTicketControlE2E(t, home, 1, 1, contracts.WorkerRunning)

	var cancelCalled atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/api/ticket-loops/1/cancel" &&
			r.URL.Query().Get("project") == "demo" &&
			r.URL.Query().Get("cause") == string(contracts.TaskCancelCauseUserStop) {
			cancelCalled.Store(true)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ticket_id":  1,
				"found":      true,
				"canceled":   true,
				"project":    "demo",
				"request_id": "req-ticket-stop",
				"reason":     "ticket loop cancel signal sent",
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
		"ticket", "stop",
		"--ticket", "1",
		"-o", "json",
	)
	var resp struct {
		Schema  string `json:"schema"`
		Count   int    `json:"count"`
		Stopped []struct {
			WorkerID uint `json:"worker_id"`
		} `json:"stopped"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &resp); err != nil {
		t.Fatalf("decode response failed: %v raw=%s", err, stdout)
	}
	if resp.Schema != "dalek.ticket.stop.v1" || resp.Count != 1 || len(resp.Stopped) != 1 || resp.Stopped[0].WorkerID != 1 {
		t.Fatalf("unexpected stop response: %+v", resp)
	}
	if !cancelCalled.Load() {
		t.Fatalf("expected daemon cancel endpoint called")
	}
	worker := loadWorkerForTicketControlE2E(t, home, 1)
	if worker.Status != contracts.WorkerRunning {
		t.Fatalf("expected worker unchanged without legacy fallback, got=%s", worker.Status)
	}
}

func TestCLI_TicketInterrupt_UsesDaemonTicketLoopCancel(t *testing.T) {
	bin := buildCLIBinary(t)
	repoRoot := initGitRepo(t)
	home := filepath.Join(t.TempDir(), "home")
	prepareDemoProjectWithOneTicket(t, bin, repoRoot, home)
	seedWorkerForTicketControlE2E(t, home, 1, 1, contracts.WorkerRunning)
	seedActiveWorkerRunForTicketControlE2E(t, home, 7001, 1, 1, "ticket-interrupt-e2e")

	var cancelCalled atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/api/ticket-loops/1/cancel" &&
			r.URL.Query().Get("project") == "demo" &&
			r.URL.Query().Get("cause") == string(contracts.TaskCancelCauseUserInterrupt) {
			cancelCalled.Store(true)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ticket_id":  1,
				"found":      true,
				"canceled":   true,
				"project":    "demo",
				"request_id": "req-ticket-interrupt",
				"reason":     "ticket loop cancel signal sent",
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
		"ticket", "interrupt",
		"--ticket", "1",
		"-o", "json",
	)
	var resp struct {
		Schema    string `json:"schema"`
		TicketID  uint   `json:"ticket_id"`
		WorkerID  uint   `json:"worker_id"`
		Mode      string `json:"mode"`
		TaskRunID uint   `json:"task_run_id"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &resp); err != nil {
		t.Fatalf("decode response failed: %v raw=%s", err, stdout)
	}
	if resp.Schema != "dalek.ticket.interrupt.v1" || resp.TicketID != 1 || resp.WorkerID != 1 {
		t.Fatalf("unexpected interrupt response: %+v", resp)
	}
	if resp.Mode != "ticket_loop_cancel" {
		t.Fatalf("expected ticket_loop_cancel mode, got=%s", resp.Mode)
	}
	if resp.TaskRunID != 7001 {
		t.Fatalf("expected task_run_id=7001, got=%d", resp.TaskRunID)
	}
	if !cancelCalled.Load() {
		t.Fatalf("expected daemon cancel endpoint called")
	}
	run := loadTaskRunForTicketControlE2E(t, home, 7001)
	if run.OrchestrationState != contracts.TaskRunning {
		t.Fatalf("expected task run unchanged without legacy fallback, got=%s", run.OrchestrationState)
	}
}
