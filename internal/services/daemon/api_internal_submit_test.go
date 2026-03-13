package daemon

import (
	"bytes"
	"context"
	"dalek/internal/contracts"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

const submitSyncRejectCause = "daemon submit 不支持 sync=true"

type internalSubmitAPIError struct {
	Error string `json:"error"`
	Cause string `json:"cause"`
}

func startTestInternalAPIForSubmit(t *testing.T) *InternalAPI {
	t.Helper()

	host, err := NewExecutionHost(&testExecutionHostResolver{project: &testExecutionHostProject{}}, ExecutionHostOptions{})
	if err != nil {
		t.Fatalf("NewExecutionHost failed: %v", err)
	}
	svc, err := NewInternalAPI(host, InternalAPIConfig{
		ListenAddr: "127.0.0.1:0",
	}, InternalAPIOptions{})
	if err != nil {
		t.Fatalf("NewInternalAPI failed: %v", err)
	}
	if err := svc.Start(context.Background()); err != nil {
		t.Fatalf("InternalAPI Start failed: %v", err)
	}
	t.Cleanup(func() {
		_ = svc.Stop(context.Background())
	})
	return svc
}

func postSubmitSyncTrue(t *testing.T, svc *InternalAPI, route string) (int, internalSubmitAPIError, string) {
	t.Helper()

	payload := map[string]any{
		"project":    "demo",
		"request_id": "req-sync-reject",
		"prompt":     "run",
		"sync":       true,
	}
	if route == "/api/subagent/submit" {
		payload["provider"] = "codex"
		payload["model"] = "gpt-5.3-codex"
	} else {
		payload["ticket_id"] = 1
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal request failed: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, "http://"+svc.listener.Addr().String()+route, bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("new request failed: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("http do failed: %v", err)
	}
	defer resp.Body.Close()

	bodyRaw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body failed: %v", err)
	}
	var body internalSubmitAPIError
	if err := json.Unmarshal(bodyRaw, &body); err != nil {
		t.Fatalf("decode response failed: %v; raw=%s", err, string(bodyRaw))
	}
	return resp.StatusCode, body, string(bodyRaw)
}

func assertSubmitSyncRejected(t *testing.T, status int, body internalSubmitAPIError) {
	t.Helper()

	if status != http.StatusBadRequest {
		t.Fatalf("unexpected status: %d", status)
	}
	if body.Error != "bad_request" {
		t.Fatalf("unexpected error code: %s", body.Error)
	}
	if body.Cause != submitSyncRejectCause {
		t.Fatalf("unexpected cause: %s", body.Cause)
	}
}

func TestHandleTicketLoopSubmit_RejectsSyncTrue(t *testing.T) {
	svc := startTestInternalAPIForSubmit(t)

	status, body, _ := postSubmitSyncTrue(t, svc, "/api/ticket-loops/submit")
	assertSubmitSyncRejected(t, status, body)
}

func TestHandleSubagentSubmit_RejectsSyncTrue(t *testing.T) {
	svc := startTestInternalAPIForSubmit(t)

	status, body, _ := postSubmitSyncTrue(t, svc, "/api/subagent/submit")
	assertSubmitSyncRejected(t, status, body)
}

func TestHandleDispatchSubmitRouteRemoved(t *testing.T) {
	svc := startTestInternalAPIForSubmit(t)
	status := postMissingRoute(t, svc, "/api/dispatch/submit")
	if status != http.StatusNotFound {
		t.Fatalf("expected removed dispatch route to return 404, got=%d", status)
	}
}

func TestHandleLegacyWorkerRunSubmitRouteRemoved(t *testing.T) {
	svc := startTestInternalAPIForSubmit(t)
	status := postMissingRoute(t, svc, "/api/worker-run/submit")
	if status != http.StatusNotFound {
		t.Fatalf("expected removed worker-run submit route to return 404, got=%d", status)
	}
}

func TestHandleLegacyRunsRouteRemoved(t *testing.T) {
	svc := startTestInternalAPIForSubmit(t)
	status := postMissingRoute(t, svc, "/api/runs/1/cancel")
	if status != http.StatusNotFound {
		t.Fatalf("expected removed runs route to return 404, got=%d", status)
	}
}

func postSubmitSyncFalse(t *testing.T, svc *InternalAPI, route, requestID string) (int, map[string]any) {
	t.Helper()
	payload := map[string]any{
		"project":    "demo",
		"request_id": requestID,
		"prompt":     "继续执行任务",
		"sync":       false,
	}
	if route == "/api/subagent/submit" {
		payload["provider"] = "claude"
		payload["model"] = "sonnet"
	} else {
		payload["ticket_id"] = 1
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal request failed: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, "http://"+svc.listener.Addr().String()+route, bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("new request failed: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("http do failed: %v", err)
	}
	defer resp.Body.Close()
	bodyRaw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body failed: %v", err)
	}
	var body map[string]any
	if err := json.Unmarshal(bodyRaw, &body); err != nil {
		t.Fatalf("decode response failed: %v raw=%s", err, string(bodyRaw))
	}
	return resp.StatusCode, body
}

func postTicketLoopSubmitWithAutoStart(t *testing.T, svc *InternalAPI, requestID string, autoStart *bool, baseBranch string) (int, map[string]any) {
	t.Helper()
	payload := map[string]any{
		"project":    "demo",
		"ticket_id":  1,
		"request_id": requestID,
		"prompt":     "继续执行任务",
		"sync":       false,
	}
	if autoStart != nil {
		payload["auto_start"] = *autoStart
	}
	if strings.TrimSpace(baseBranch) != "" {
		payload["base_branch"] = strings.TrimSpace(baseBranch)
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal request failed: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, "http://"+svc.listener.Addr().String()+"/api/ticket-loops/submit", bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("new request failed: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("http do failed: %v", err)
	}
	defer resp.Body.Close()

	bodyRaw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body failed: %v", err)
	}
	var body map[string]any
	if err := json.Unmarshal(bodyRaw, &body); err != nil {
		t.Fatalf("decode response failed: %v raw=%s", err, string(bodyRaw))
	}
	return resp.StatusCode, body
}

func postMissingRoute(t *testing.T, svc *InternalAPI, route string) int {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, "http://"+svc.listener.Addr().String()+route, bytes.NewReader([]byte(`{}`)))
	if err != nil {
		t.Fatalf("new request failed: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("http do failed: %v", err)
	}
	defer resp.Body.Close()
	return resp.StatusCode
}

func postSubmitRaw(t *testing.T, svc *InternalAPI, route string, body io.Reader) (int, internalSubmitAPIError) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, "http://"+svc.listener.Addr().String()+route, body)
	if err != nil {
		t.Fatalf("new request failed: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("http do failed: %v", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response failed: %v", err)
	}
	var got internalSubmitAPIError
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("decode response failed: %v raw=%s", err, string(raw))
	}
	return resp.StatusCode, got
}

func postTicketStart(t *testing.T, svc *InternalAPI, baseBranch string) (int, map[string]any) {
	t.Helper()
	payload := map[string]any{
		"project":     "demo",
		"ticket_id":   7,
		"base_branch": baseBranch,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal request failed: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, "http://"+svc.listener.Addr().String()+"/api/tickets/start", bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("new request failed: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("http do failed: %v", err)
	}
	defer resp.Body.Close()
	bodyRaw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body failed: %v", err)
	}
	var body map[string]any
	if err := json.Unmarshal(bodyRaw, &body); err != nil {
		t.Fatalf("decode response failed: %v raw=%s", err, string(bodyRaw))
	}
	return resp.StatusCode, body
}

func TestHandleTicketStart_ForwardsBaseBranch(t *testing.T) {
	project := &testExecutionHostProject{
		ticketViews: []TicketView{
			{
				Ticket: contracts.Ticket{
					ID:             7,
					WorkflowStatus: contracts.TicketQueued,
				},
			},
		},
	}
	host, err := NewExecutionHost(&testExecutionHostResolver{project: project}, ExecutionHostOptions{})
	if err != nil {
		t.Fatalf("NewExecutionHost failed: %v", err)
	}
	svc, err := NewInternalAPI(host, InternalAPIConfig{ListenAddr: "127.0.0.1:0"}, InternalAPIOptions{})
	if err != nil {
		t.Fatalf("NewInternalAPI failed: %v", err)
	}
	if err := svc.Start(context.Background()); err != nil {
		t.Fatalf("InternalAPI Start failed: %v", err)
	}
	t.Cleanup(func() {
		_ = svc.Stop(context.Background())
	})

	status, body := postTicketStart(t, svc, "release/v2")
	if status != http.StatusOK {
		t.Fatalf("unexpected status=%d body=%+v", status, body)
	}
	if body["workflow_status"] != string(contracts.TicketQueued) {
		t.Fatalf("unexpected workflow status body=%+v", body)
	}
	if got := project.StartTicketCount(); got != 1 {
		t.Fatalf("expected one start call, got=%d", got)
	}
	if got := project.LastStartBaseBranch(); got != "release/v2" {
		t.Fatalf("expected forwarded base branch release/v2, got=%q", got)
	}
}

func TestHandleTicketLoopSubmit_QueryUsesTicketAndTaskFlags(t *testing.T) {
	svc := startTestInternalAPIForSubmit(t)
	status, body := postSubmitSyncFalse(t, svc, "/api/ticket-loops/submit", "req-worker-query-id")
	if status != http.StatusAccepted {
		t.Fatalf("unexpected status=%d body=%+v", status, body)
	}
	queryAny, ok := body["query"]
	if !ok {
		t.Fatalf("missing query field: %+v", body)
	}
	query, ok := queryAny.(map[string]any)
	if !ok {
		t.Fatalf("query should be object, got=%T", queryAny)
	}
	for _, key := range []string{"ticket", "events", "task", "task_events", "cancel"} {
		raw, ok := query[key]
		if !ok {
			t.Fatalf("missing query.%s", key)
		}
		value, ok := raw.(string)
		if !ok {
			t.Fatalf("query.%s should be string, got=%T", key, raw)
		}
		switch key {
		case "ticket", "events":
			if !strings.Contains(value, "--ticket ") {
				t.Fatalf("query.%s should contain --ticket, got=%q", key, value)
			}
		default:
			if !strings.Contains(value, "--id ") {
				t.Fatalf("query.%s should contain --id, got=%q", key, value)
			}
		}
	}
	controlAny, ok := body["control"]
	if !ok {
		t.Fatalf("missing control field: %+v", body)
	}
	control, ok := controlAny.(map[string]any)
	if !ok {
		t.Fatalf("control should be object, got=%T", controlAny)
	}
	for key, want := range map[string]string{
		"probe":  "/api/ticket-loops/1?project=demo",
		"cancel": "/api/ticket-loops/1/cancel?project=demo",
	} {
		raw, ok := control[key]
		if !ok {
			t.Fatalf("missing control.%s", key)
		}
		value, ok := raw.(string)
		if !ok {
			t.Fatalf("control.%s should be string, got=%T", key, raw)
		}
		if value != want {
			t.Fatalf("unexpected control.%s: got=%q want=%q", key, value, want)
		}
	}
}

func TestHandleTicketLoopSubmit_ForwardsAutoStartFalse(t *testing.T) {
	project := &testExecutionHostProject{}
	host, err := NewExecutionHost(&testExecutionHostResolver{project: project}, ExecutionHostOptions{})
	if err != nil {
		t.Fatalf("NewExecutionHost failed: %v", err)
	}
	svc, err := NewInternalAPI(host, InternalAPIConfig{
		ListenAddr: "127.0.0.1:0",
	}, InternalAPIOptions{})
	if err != nil {
		t.Fatalf("NewInternalAPI failed: %v", err)
	}
	if err := svc.Start(context.Background()); err != nil {
		t.Fatalf("InternalAPI Start failed: %v", err)
	}
	t.Cleanup(func() {
		_ = svc.Stop(context.Background())
	})

	autoStart := false
	status, body := postTicketLoopSubmitWithAutoStart(t, svc, "req-worker-auto-start-false", &autoStart, "release/v2")
	if status != http.StatusAccepted {
		t.Fatalf("unexpected status=%d body=%+v", status, body)
	}
	got := project.LastDirectDispatchAutoStart()
	if got == nil || *got {
		t.Fatalf("expected forwarded auto_start=false, got=%v", got)
	}
	if got := project.LastDirectDispatchBaseBranch(); got != "release/v2" {
		t.Fatalf("expected forwarded base_branch=release/v2, got=%q", got)
	}
}

func TestHandleSubagentSubmit_QueryUsesRunIDFlag(t *testing.T) {
	svc := startTestInternalAPIForSubmit(t)
	status, body := postSubmitSyncFalse(t, svc, "/api/subagent/submit", "req-subagent-query-id")
	if status != http.StatusAccepted {
		t.Fatalf("unexpected status=%d body=%+v", status, body)
	}
	queryAny, ok := body["query"]
	if !ok {
		t.Fatalf("missing query field: %+v", body)
	}
	query, ok := queryAny.(map[string]any)
	if !ok {
		t.Fatalf("query should be object, got=%T", queryAny)
	}
	wantContains := map[string]string{
		"show":   "--run-id ",
		"logs":   "--run-id ",
		"cancel": "--run-id ",
	}
	for key, marker := range wantContains {
		raw, ok := query[key]
		if !ok {
			t.Fatalf("missing query.%s", key)
		}
		value, ok := raw.(string)
		if !ok {
			t.Fatalf("query.%s should be string, got=%T", key, raw)
		}
		if !strings.Contains(value, marker) {
			t.Fatalf("query.%s should contain %q, got=%q", key, marker, value)
		}
	}
}

func TestHandleTicketLoopSubmit_RejectsOversizedBody(t *testing.T) {
	svc := startTestInternalAPIForSubmit(t)
	oversized := map[string]any{
		"project":    "demo",
		"ticket_id":  1,
		"request_id": "req-big-worker",
		"prompt":     strings.Repeat("x", int(internalAPIMaxJSONBodyBytes)),
		"sync":       false,
	}
	raw, err := json.Marshal(oversized)
	if err != nil {
		t.Fatalf("marshal request failed: %v", err)
	}
	status, body := postSubmitRaw(t, svc, "/api/ticket-loops/submit", bytes.NewReader(raw))
	if status != http.StatusBadRequest {
		t.Fatalf("unexpected status: %d body=%+v", status, body)
	}
	if body.Error != "bad_request" || !strings.Contains(body.Cause, "request body 过大") {
		t.Fatalf("unexpected response: %+v", body)
	}
}

func TestHandleTicketLoopProbe_ReturnsFlatSnapshot(t *testing.T) {
	project := &testExecutionHostProject{
		directDispatchStarted: make(chan struct{}, 1),
		directDispatchRelease: make(chan struct{}),
	}
	host, err := NewExecutionHost(&testExecutionHostResolver{project: project}, ExecutionHostOptions{})
	if err != nil {
		t.Fatalf("NewExecutionHost failed: %v", err)
	}
	svc, err := NewInternalAPI(host, InternalAPIConfig{ListenAddr: "127.0.0.1:0"}, InternalAPIOptions{})
	if err != nil {
		t.Fatalf("NewInternalAPI failed: %v", err)
	}
	if err := svc.Start(context.Background()); err != nil {
		t.Fatalf("InternalAPI Start failed: %v", err)
	}
	t.Cleanup(func() {
		_ = svc.Stop(context.Background())
		_ = host.Stop(context.Background())
	})

	receipt, err := host.SubmitTicketLoop(context.Background(), TicketLoopSubmitRequest{
		Project:   "demo",
		TicketID:  1,
		RequestID: "probe-ticket-loop",
		Prompt:    "继续执行任务",
	})
	if err != nil {
		t.Fatalf("SubmitTicketLoop failed: %v", err)
	}
	select {
	case <-project.directDispatchStarted:
	case <-time.After(2 * time.Second):
		t.Fatalf("ticket-loop goroutine not started")
	}

	req, err := http.NewRequest(http.MethodGet, "http://"+svc.listener.Addr().String()+"/api/ticket-loops/1?project=demo", nil)
	if err != nil {
		t.Fatalf("new request failed: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("http do failed: %v", err)
	}
	defer resp.Body.Close()
	var body struct {
		Found                bool       `json:"found"`
		OwnedByCurrentDaemon bool       `json:"owned_by_current_daemon"`
		Phase                string     `json:"phase"`
		Project              string     `json:"project"`
		TicketID             uint       `json:"ticket_id"`
		WorkerID             uint       `json:"worker_id"`
		RunID                uint       `json:"run_id"`
		RequestID            string     `json:"request_id"`
		CancelRequestedAt    *time.Time `json:"cancel_requested_at"`
		LastError            string     `json:"last_error"`
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response failed: %v", err)
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("decode response failed: %v raw=%s", err, string(raw))
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected status=%d body=%s", resp.StatusCode, string(raw))
	}
	if !body.Found || !body.OwnedByCurrentDaemon {
		t.Fatalf("unexpected probe found/owned: %+v", body)
	}
	if body.TicketID != 1 || body.RunID != receipt.TaskRunID || body.RequestID != "probe-ticket-loop" {
		t.Fatalf("unexpected probe body: %+v", body)
	}
	if strings.TrimSpace(body.Project) != "demo" {
		t.Fatalf("unexpected project: %+v", body)
	}
	if strings.TrimSpace(body.Phase) == "" {
		t.Fatalf("expected non-empty phase: %+v", body)
	}

	close(project.directDispatchRelease)
	waitForTicketLoopGone(t, host, "demo", 1)
}
