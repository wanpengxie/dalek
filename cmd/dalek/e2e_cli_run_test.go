package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"dalek/internal/app"
	"dalek/internal/contracts"
	runsvc "dalek/internal/services/run"
	tasksvc "dalek/internal/services/task"
)

func TestCLI_RunWorkflow_JSON(t *testing.T) {
	bin := buildCLIBinary(t)
	repo := initGitRepo(t)
	home := filepath.Join(t.TempDir(), "home")
	prepareDemoProjectWithOneTicket(t, bin, repo, home)

	requestOut, _ := runCLIOK(
		t,
		bin,
		repo,
		"-home", home,
		"-project", "demo",
		"run", "request",
		"--verify-target", "test",
		"--request-id", "req-run-1",
		"-o", "json",
	)
	var requestResp struct {
		Schema string `json:"schema"`
		Run    struct {
			RunID     uint   `json:"RunID"`
			RequestID string `json:"RequestID"`
			RunStatus string `json:"RunStatus"`
		} `json:"run"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(requestOut)), &requestResp); err != nil {
		t.Fatalf("decode run request response failed: %v raw=%s", err, requestOut)
	}
	if requestResp.Schema != "dalek.run.request.v1" || requestResp.Run.RunID == 0 || requestResp.Run.RequestID != "req-run-1" {
		t.Fatalf("unexpected request response: %+v", requestResp)
	}

	showOut, _ := runCLIOK(
		t,
		bin,
		repo,
		"-home", home,
		"-project", "demo",
		"run", "show",
		"--id", fmt.Sprintf("%d", requestResp.Run.RunID),
		"-o", "json",
	)
	var showResp struct {
		Schema string `json:"schema"`
		Run    struct {
			RunID     uint   `json:"RunID"`
			RunStatus string `json:"RunStatus"`
		} `json:"run"`
		TaskStatus struct {
			RuntimeSummary    string `json:"RuntimeSummary"`
			SemanticMilestone string `json:"SemanticMilestone"`
			LastEventType     string `json:"LastEventType"`
		} `json:"task_status"`
		Warnings []string `json:"warnings"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(showOut)), &showResp); err != nil {
		t.Fatalf("decode run show response failed: %v raw=%s", err, showOut)
	}
	if showResp.Schema != "dalek.run.show.v1" || showResp.Run.RunID != requestResp.Run.RunID {
		t.Fatalf("unexpected show response: %+v", showResp)
	}
	if showResp.TaskStatus.RuntimeSummary == "" || showResp.TaskStatus.SemanticMilestone == "" || showResp.TaskStatus.LastEventType == "" {
		t.Fatalf("expected task_status in show response: %+v", showResp)
	}
	if len(showResp.Warnings) != 0 {
		t.Fatalf("expected no warnings for normal show response: %+v", showResp.Warnings)
	}

	logsOut, _ := runCLIOK(
		t,
		bin,
		repo,
		"-home", home,
		"-project", "demo",
		"run", "logs",
		"--id", fmt.Sprintf("%d", requestResp.Run.RunID),
		"-o", "json",
	)
	var logsResp struct {
		Schema string `json:"schema"`
		Logs   struct {
			Found bool   `json:"Found"`
			Tail  string `json:"Tail"`
		} `json:"logs"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(logsOut)), &logsResp); err != nil {
		t.Fatalf("decode run logs response failed: %v raw=%s", err, logsOut)
	}
	if logsResp.Schema != "dalek.run.logs.v1" || !logsResp.Logs.Found {
		t.Fatalf("unexpected logs response: %+v", logsResp)
	}
	if !strings.Contains(logsResp.Logs.Tail, "run_requested") {
		t.Fatalf("expected logs tail to include run_requested, got=%q", logsResp.Logs.Tail)
	}

	artifactsOut, _ := runCLIOK(
		t,
		bin,
		repo,
		"-home", home,
		"-project", "demo",
		"run", "artifact", "ls",
		"--id", fmt.Sprintf("%d", requestResp.Run.RunID),
		"-o", "json",
	)
	var artifactsResp struct {
		Schema    string `json:"schema"`
		Artifacts struct {
			Found     bool `json:"Found"`
			Artifacts []struct {
				Name string `json:"Name"`
			} `json:"Artifacts"`
			Issues []struct {
				Name   string `json:"Name"`
				Status string `json:"Status"`
				Reason string `json:"Reason"`
			} `json:"Issues"`
		} `json:"artifacts"`
		Warnings []string `json:"warnings"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(artifactsOut)), &artifactsResp); err != nil {
		t.Fatalf("decode run artifacts response failed: %v raw=%s", err, artifactsOut)
	}
	if artifactsResp.Schema != "dalek.run.artifacts.v1" || !artifactsResp.Artifacts.Found {
		t.Fatalf("unexpected artifacts response: %+v", artifactsResp)
	}
	if len(artifactsResp.Artifacts.Artifacts) != 0 {
		t.Fatalf("expected empty artifacts list: %+v", artifactsResp.Artifacts.Artifacts)
	}
	if len(artifactsResp.Artifacts.Issues) != 0 {
		t.Fatalf("expected empty artifact issues: %+v", artifactsResp.Artifacts.Issues)
	}
	if len(artifactsResp.Warnings) != 0 {
		t.Fatalf("expected empty artifact warnings: %+v", artifactsResp.Warnings)
	}

	cancelOut, _ := runCLIOK(
		t,
		bin,
		repo,
		"-home", home,
		"-project", "demo",
		"run", "cancel",
		"--id", fmt.Sprintf("%d", requestResp.Run.RunID),
		"-o", "json",
	)
	var cancelResp struct {
		Schema string `json:"schema"`
		Result struct {
			RunID    uint `json:"RunID"`
			Found    bool `json:"Found"`
			Canceled bool `json:"Canceled"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(cancelOut)), &cancelResp); err != nil {
		t.Fatalf("decode run cancel response failed: %v raw=%s", err, cancelOut)
	}
	if cancelResp.Schema != "dalek.run.cancel.v1" || !cancelResp.Result.Found || cancelResp.Result.RunID != requestResp.Run.RunID {
		t.Fatalf("unexpected cancel response: %+v", cancelResp)
	}
}

func TestCLI_RunShow_ReconcilesRecoveryStatus_JSON(t *testing.T) {
	bin := buildCLIBinary(t)
	repo := initGitRepo(t)
	home := filepath.Join(t.TempDir(), "home")
	prepareDemoProjectWithOneTicket(t, bin, repo, home)

	h, err := app.OpenHome(home)
	if err != nil {
		t.Fatalf("OpenHome failed: %v", err)
	}
	project, err := h.OpenProjectByName("demo")
	if err != nil {
		t.Fatalf("OpenProjectByName failed: %v", err)
	}
	db, err := project.OpenDBForTest()
	if err != nil {
		t.Fatalf("OpenDBForTest failed: %v", err)
	}
	now := time.Date(2026, 3, 16, 12, 0, 0, 0, time.UTC)
	viewRec := contracts.RunView{
		RunID:        41,
		TaskRunID:    41,
		ProjectKey:   "demo",
		RequestID:    "req-reconcile-41",
		RunStatus:    contracts.RunReconciling,
		VerifyTarget: "test",
		SnapshotID:   "snap-41",
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := db.Create(&viewRec).Error; err != nil {
		t.Fatalf("seed run view failed: %v", err)
	}

	server := startNodeQueryServer(t, map[string]any{
		"found":           true,
		"run_id":          41,
		"task_run_id":     41,
		"status":          "succeeded",
		"lifecycle_stage": "completed",
		"summary":         "remote recovered",
		"snapshot_id":     "snap-41",
		"verify_target":   "test",
		"last_event_type": "run_verify_succeeded",
		"last_event_note": "remote recovered",
	})
	t.Cleanup(server.Close)
	configureNodeAgentInternalForE2E(t, home, server.URL, "node-token-e2e")

	showOut, _ := runCLIOK(
		t,
		bin,
		repo,
		"-home", home,
		"-project", "demo",
		"run", "show",
		"--id", "41",
		"-o", "json",
	)
	var showResp struct {
		Schema string `json:"schema"`
		Run    struct {
			RunID     uint   `json:"RunID"`
			RunStatus string `json:"RunStatus"`
			RequestID string `json:"RequestID"`
		} `json:"run"`
		TaskStatus struct {
			LastEventType  string `json:"LastEventType"`
			LastEventNote  string `json:"LastEventNote"`
			RuntimeSummary string `json:"RuntimeSummary"`
		} `json:"task_status"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(showOut)), &showResp); err != nil {
		t.Fatalf("decode run show response failed: %v raw=%s", err, showOut)
	}
	if showResp.Schema != "dalek.run.show.v1" || showResp.Run.RunID != 41 || showResp.Run.RunStatus != "succeeded" {
		t.Fatalf("unexpected reconciled show response: %+v", showResp)
	}
}

func TestCLI_RunShow_TextHintsArtifactFailure(t *testing.T) {
	bin := buildCLIBinary(t)
	repo := initGitRepo(t)
	home := filepath.Join(t.TempDir(), "home")
	prepareDemoProjectWithOneTicket(t, bin, repo, home)

	h, err := app.OpenHome(home)
	if err != nil {
		t.Fatalf("OpenHome failed: %v", err)
	}
	project, err := h.OpenProjectByName("demo")
	if err != nil {
		t.Fatalf("OpenProjectByName failed: %v", err)
	}
	ctx := context.Background()
	res, err := project.SubmitRun(ctx, app.SubmitRunOptions{
		RequestID:    "req-artifact-hint-1",
		VerifyTarget: "test",
	})
	if err != nil {
		t.Fatalf("SubmitRun failed: %v", err)
	}
	db, err := project.OpenDBForTest()
	if err != nil {
		t.Fatalf("OpenDBForTest failed: %v", err)
	}
	runService := runsvc.New(db, tasksvc.New(db), nil, nil)
	if err := runService.RecordArtifactFailure(ctx, res.RunID, "report.json", "upload failed"); err != nil {
		t.Fatalf("RecordArtifactFailure failed: %v", err)
	}

	showOut, _ := runCLIOK(
		t,
		bin,
		repo,
		"-home", home,
		"-project", "demo",
		"run", "show",
		"--id", fmt.Sprintf("%d", res.RunID),
	)
	if !strings.Contains(showOut, "last_event: run_artifact_upload_failed") {
		t.Fatalf("expected last_event in text output, got=%s", showOut)
	}
	if !strings.Contains(showOut, "artifact 上传存在部分失败") {
		t.Fatalf("expected artifact hint in text output, got=%s", showOut)
	}
}

func TestCLI_RunArtifactList_ReportsArtifactIssues(t *testing.T) {
	bin := buildCLIBinary(t)
	repo := initGitRepo(t)
	home := filepath.Join(t.TempDir(), "home")
	prepareDemoProjectWithOneTicket(t, bin, repo, home)

	h, err := app.OpenHome(home)
	if err != nil {
		t.Fatalf("OpenHome failed: %v", err)
	}
	project, err := h.OpenProjectByName("demo")
	if err != nil {
		t.Fatalf("OpenProjectByName failed: %v", err)
	}
	ctx := context.Background()
	res, err := project.SubmitRun(ctx, app.SubmitRunOptions{
		RequestID:    "req-artifact-issue-1",
		VerifyTarget: "test",
	})
	if err != nil {
		t.Fatalf("SubmitRun failed: %v", err)
	}
	db, err := project.OpenDBForTest()
	if err != nil {
		t.Fatalf("OpenDBForTest failed: %v", err)
	}
	runService := runsvc.New(db, tasksvc.New(db), nil, nil)
	if err := runService.RecordArtifactFailure(ctx, res.RunID, "report.json", "upload failed"); err != nil {
		t.Fatalf("RecordArtifactFailure failed: %v", err)
	}

	jsonOut, _ := runCLIOK(
		t,
		bin,
		repo,
		"-home", home,
		"-project", "demo",
		"run", "artifact", "ls",
		"--id", fmt.Sprintf("%d", res.RunID),
		"-o", "json",
	)
	var jsonResp struct {
		Artifacts struct {
			Found  bool `json:"Found"`
			Issues []struct {
				Name   string `json:"Name"`
				Status string `json:"Status"`
				Reason string `json:"Reason"`
			} `json:"Issues"`
		} `json:"artifacts"`
		Warnings []string `json:"warnings"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(jsonOut)), &jsonResp); err != nil {
		t.Fatalf("decode artifact issue json failed: %v raw=%s", err, jsonOut)
	}
	if !jsonResp.Artifacts.Found || len(jsonResp.Artifacts.Issues) != 1 {
		t.Fatalf("expected artifact issue in json output: %+v", jsonResp)
	}
	if jsonResp.Artifacts.Issues[0].Name != "report.json" || jsonResp.Artifacts.Issues[0].Status != "upload_failed" {
		t.Fatalf("unexpected artifact issue json output: %+v", jsonResp.Artifacts.Issues)
	}
	if len(jsonResp.Warnings) == 0 {
		t.Fatalf("expected warnings in artifact issue json output: %+v", jsonResp)
	}

	textOut, _ := runCLIOK(
		t,
		bin,
		repo,
		"-home", home,
		"-project", "demo",
		"run", "artifact", "ls",
		"--id", fmt.Sprintf("%d", res.RunID),
	)
	if !strings.Contains(textOut, "(no indexed artifacts)") {
		t.Fatalf("expected no indexed artifacts banner, got=%s", textOut)
	}
	if !strings.Contains(textOut, "WARN\treport.json\tupload_failed\tupload failed") {
		t.Fatalf("expected artifact issue line, got=%s", textOut)
	}
}

func startNodeQueryServer(t *testing.T, response map[string]any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.TrimSpace(r.Header.Get("Authorization")) != "Bearer node-token-e2e" {
			t.Fatalf("unexpected authorization header: %q", r.Header.Get("Authorization"))
		}
		if r.URL.Path != "/api/node/run/query" {
			http.NotFound(w, r)
			return
		}
		var req map[string]any
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode query request failed: %v", err)
		}
		meta, _ := req["meta"].(map[string]any)
		requestID := strings.TrimSpace(fmt.Sprint(meta["request_id"]))
		runID := strings.TrimSpace(fmt.Sprint(meta["run_id"]))
		if requestID != "req-reconcile-41" && runID != "41" {
			t.Fatalf("unexpected query selector: %+v", req)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(response)
	}))
}
