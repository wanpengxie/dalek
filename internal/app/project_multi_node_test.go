package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"dalek/internal/contracts"
)

func TestProject_SubmitTaskRequest_RemoteDevProxy(t *testing.T) {
	p := newIntegrationProject(t)
	ctx := context.Background()

	tk, err := p.CreateTicketWithDescription(ctx, "remote dev proxy", "submit remote dev task")
	if err != nil {
		t.Fatalf("CreateTicket failed: %v", err)
	}

	runRes, err := p.SubmitRun(ctx, SubmitRunOptions{
		TicketID:     tk.ID,
		RequestID:    "run-failed-ctx",
		VerifyTarget: "test",
	})
	if err != nil {
		t.Fatalf("SubmitRun failed: %v", err)
	}
	now := time.Now()
	if err := p.task.MarkRunFailed(ctx, runRes.RunID, "verify_failed", "compile error: missing symbol", now); err != nil {
		t.Fatalf("MarkRunFailed failed: %v", err)
	}
	_ = p.task.AppendEvent(ctx, contracts.TaskEventInput{
		TaskRunID: runRes.RunID,
		EventType: "run_failed",
		Note:      "compile error: missing symbol",
		CreatedAt: now,
	})

	var capturedPrompt atomic.Value
	var runQueryCount atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/worker-run/submit":
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatalf("decode submit payload failed: %v", err)
			}
			capturedPrompt.Store(strings.TrimSpace(payload["prompt"].(string)))
			w.WriteHeader(http.StatusAccepted)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"accepted":    true,
				"project":     "demo",
				"request_id":  "remote-dev-req-1",
				"task_run_id": 99,
				"ticket_id":   tk.ID,
				"worker_id":   7,
			})
		case r.Method == http.MethodGet && r.URL.Path == "/api/runs/99":
			runQueryCount.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"run": map[string]any{
					"run_id":               99,
					"project":              "demo",
					"owner_type":           "worker",
					"task_type":            "worker_run",
					"ticket_id":            tk.ID,
					"worker_id":            7,
					"orchestration_state":  "succeeded",
					"runtime_health_state": "idle",
					"runtime_needs_user":   false,
					"runtime_summary":      "remote worker completed",
					"semantic_next_action": "done",
					"semantic_summary":     "remote worker completed",
					"updated_at":           time.Now(),
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	p.core.Config.MultiNode.AutoRoute = true
	p.core.Config.MultiNode.DevBaseURL = srv.URL
	p.core.Config.MultiNode.DevProjectName = "demo"

	res, err := p.SubmitTaskRequest(ctx, SubmitTaskRequestOptions{
		TicketID:  tk.ID,
		RequestID: "dev-proxy-1",
		Prompt:    "please continue fixing this ticket",
	})
	if err != nil {
		t.Fatalf("SubmitTaskRequest failed: %v", err)
	}
	if !res.Accepted || res.Role != "dev" || res.TaskRunID == 0 || res.RemoteRunID != 99 {
		t.Fatalf("unexpected submission: %+v", res)
	}
	prompt, _ := capturedPrompt.Load().(string)
	if !strings.Contains(prompt, "compile error: missing symbol") {
		t.Fatalf("expected linked run failure context in prompt, got=%q", prompt)
	}

	status, err := p.GetTaskStatus(ctx, res.TaskRunID)
	if err != nil {
		t.Fatalf("GetTaskStatus failed: %v", err)
	}
	if status == nil {
		t.Fatalf("expected task status")
	}
	if status.OrchestrationState != string(contracts.TaskSucceeded) {
		t.Fatalf("expected reconciled succeeded state, got=%s", status.OrchestrationState)
	}
	if got := strings.TrimSpace(status.SemanticSummary); got != "remote worker completed" {
		t.Fatalf("unexpected semantic summary: %q", got)
	}
	if runQueryCount.Load() == 0 {
		t.Fatalf("expected remote run query during reconcile")
	}

	events, err := p.ListTaskEvents(ctx, res.TaskRunID, 20)
	if err != nil {
		t.Fatalf("ListTaskEvents failed: %v", err)
	}
	foundLinked := false
	foundReconciled := false
	for _, ev := range events {
		switch ev.EventType {
		case "run_failure_context_linked":
			foundLinked = true
		case "remote_task_reconciled":
			foundReconciled = true
		}
	}
	if !foundLinked || !foundReconciled {
		t.Fatalf("expected linked and reconciled events, linked=%v reconciled=%v", foundLinked, foundReconciled)
	}
}

func TestProject_SubmitTaskRequest_RemoteRunProxy(t *testing.T) {
	p := newIntegrationProject(t)
	ctx := context.Background()

	tk, err := p.CreateTicketWithDescription(ctx, "remote run proxy", "submit remote run task")
	if err != nil {
		t.Fatalf("CreateTicket failed: %v", err)
	}

	var canceled atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/runs":
			w.WriteHeader(http.StatusAccepted)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"accepted":      true,
				"project":       "demo",
				"run_id":        501,
				"task_run_id":   501,
				"request_id":    "remote-run-req-1",
				"run_status":    "running",
				"verify_target": "test",
			})
		case r.Method == http.MethodGet && r.URL.Path == "/api/runs/501":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"run": map[string]any{
					"run_id":               501,
					"project":              "demo",
					"owner_type":           "pm",
					"task_type":            "run_verify",
					"ticket_id":            tk.ID,
					"orchestration_state":  "running",
					"runtime_health_state": "busy",
					"runtime_needs_user":   false,
					"runtime_summary":      "verify running on remote node",
					"semantic_next_action": "continue",
					"semantic_summary":     "remote verify running",
					"updated_at":           time.Now(),
				},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/api/runs/501/logs":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"found":  true,
				"run_id": 501,
				"tail":   "2026-03-17T12:00:00Z remote verify failed",
			})
		case r.Method == http.MethodGet && r.URL.Path == "/api/runs/501/artifacts":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"found":  true,
				"run_id": 501,
				"artifacts": []map[string]any{
					{"name": "junit.xml", "kind": "report", "size": 128, "ref": "artifact://junit.xml"},
				},
				"issues": []map[string]any{
					{"name": "coverage.out", "status": "upload_failed", "reason": "network"},
				},
			})
		case r.Method == http.MethodPost && r.URL.Path == "/api/runs/501/cancel":
			canceled.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"run_id": 501, "found": true, "canceled": true, "project": "demo", "request_id": "remote-run-req-1", "reason": "cancel signal sent",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	p.core.Config.MultiNode.AutoRoute = true
	p.core.Config.MultiNode.RunBaseURL = srv.URL
	p.core.Config.MultiNode.RunProjectName = "demo"

	res, err := p.SubmitTaskRequest(ctx, SubmitTaskRequestOptions{
		TicketID:     tk.ID,
		RequestID:    "run-proxy-1",
		VerifyTarget: "test",
		ForceRole:    TaskRequestRoleRun,
	})
	if err != nil {
		t.Fatalf("SubmitTaskRequest failed: %v", err)
	}
	if !res.Accepted || res.Role != "run" || res.TaskRunID == 0 || res.RemoteRunID != 501 {
		t.Fatalf("unexpected submission: %+v", res)
	}

	view, err := p.GetRun(ctx, res.TaskRunID)
	if err != nil {
		t.Fatalf("GetRun failed: %v", err)
	}
	if view == nil || view.RunStatus != contracts.RunRunning {
		t.Fatalf("expected failed remote run view, got=%+v", view)
	}

	status, err := p.GetTaskStatus(ctx, res.TaskRunID)
	if err != nil {
		t.Fatalf("GetTaskStatus failed: %v", err)
	}
	if status == nil || strings.TrimSpace(status.RuntimeSummary) != "verify running on remote node" {
		t.Fatalf("unexpected task status: %+v", status)
	}

	logs, err := p.GetRunLogs(ctx, res.TaskRunID, 20)
	if err != nil {
		t.Fatalf("GetRunLogs failed: %v", err)
	}
	if !strings.Contains(logs.Tail, "remote verify failed") {
		t.Fatalf("unexpected logs tail: %q", logs.Tail)
	}

	artifacts, err := p.ListRunArtifacts(ctx, res.TaskRunID)
	if err != nil {
		t.Fatalf("ListRunArtifacts failed: %v", err)
	}
	if len(artifacts.Artifacts) != 1 || len(artifacts.Issues) != 1 {
		t.Fatalf("unexpected artifacts payload: %+v", artifacts)
	}

	cancelRes, err := p.CancelRun(ctx, res.TaskRunID)
	if err != nil {
		t.Fatalf("CancelRun failed: %v", err)
	}
	if !cancelRes.Canceled || canceled.Load() == 0 {
		t.Fatalf("expected remote cancel to be called: cancelRes=%+v canceledCalls=%d", cancelRes, canceled.Load())
	}
}

func TestProject_SubmitTaskRequest_AutoRoutePromptToRun(t *testing.T) {
	p := newIntegrationProject(t)
	ctx := context.Background()

	tk, err := p.CreateTicketWithDescription(ctx, "auto route prompt", "prompt should infer run role")
	if err != nil {
		t.Fatalf("CreateTicket failed: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/runs":
			w.WriteHeader(http.StatusAccepted)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"accepted":      true,
				"project":       "demo",
				"run_id":        611,
				"task_run_id":   611,
				"request_id":    "auto-route-run-1",
				"run_status":    "running",
				"verify_target": "test",
			})
		case r.Method == http.MethodGet && r.URL.Path == "/api/runs/611":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"run": map[string]any{
					"run_id":               611,
					"project":              "demo",
					"owner_type":           "pm",
					"task_type":            "run_verify",
					"ticket_id":            tk.ID,
					"orchestration_state":  "running",
					"runtime_health_state": "busy",
					"runtime_needs_user":   false,
					"runtime_summary":      "verify running on remote node",
					"semantic_next_action": "continue",
					"semantic_summary":     "remote verify running",
					"updated_at":           time.Now(),
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	p.core.Config.MultiNode.AutoRoute = true
	p.core.Config.MultiNode.RunBaseURL = srv.URL
	p.core.Config.MultiNode.RunProjectName = "demo"

	res, err := p.SubmitTaskRequest(ctx, SubmitTaskRequestOptions{
		TicketID: tk.ID,
		Prompt:   "请帮我验证一下最近修改，跑测试并回归检查",
	})
	if err != nil {
		t.Fatalf("SubmitTaskRequest failed: %v", err)
	}
	if res.Role != "run" {
		t.Fatalf("expected auto-routed run role, got=%+v", res)
	}
	if res.RoleSource != "auto_route_prompt" {
		t.Fatalf("unexpected role source: %+v", res)
	}
	if !strings.Contains(res.RouteReason, "verify/test") {
		t.Fatalf("unexpected route reason: %+v", res)
	}
	route, ok, err := p.GetTaskRouteInfo(ctx, res.TaskRunID)
	if err != nil {
		t.Fatalf("GetTaskRouteInfo failed: %v", err)
	}
	if !ok || route.RoleSource != "auto_route_prompt" || !strings.Contains(route.RouteReason, "verify/test") {
		t.Fatalf("unexpected route info: ok=%v route=%+v", ok, route)
	}

	events, err := p.ListTaskEvents(ctx, res.TaskRunID, 20)
	if err != nil {
		t.Fatalf("ListTaskEvents failed: %v", err)
	}
	foundRouteEvent := false
	for _, ev := range events {
		if ev.EventType == "task_request_routed" && strings.Contains(ev.PayloadJSON, "\"role_source\":\"auto_route_prompt\"") {
			foundRouteEvent = true
			break
		}
	}
	if !foundRouteEvent {
		t.Fatalf("expected task_request_routed event with auto_route_prompt")
	}
}
