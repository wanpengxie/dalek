package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
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

func TestHandleDispatchSubmit_RejectsSyncTrue(t *testing.T) {
	svc := startTestInternalAPIForSubmit(t)

	status, body, _ := postSubmitSyncTrue(t, svc, "/api/dispatch/submit")
	assertSubmitSyncRejected(t, status, body)
}

func TestHandleWorkerRunSubmit_RejectsSyncTrue(t *testing.T) {
	svc := startTestInternalAPIForSubmit(t)

	status, body, _ := postSubmitSyncTrue(t, svc, "/api/worker-run/submit")
	assertSubmitSyncRejected(t, status, body)
}

func TestHandleSubagentSubmit_RejectsSyncTrue(t *testing.T) {
	svc := startTestInternalAPIForSubmit(t)

	status, body, _ := postSubmitSyncTrue(t, svc, "/api/subagent/submit")
	assertSubmitSyncRejected(t, status, body)
}

func TestSubmitEndpoints_SyncTrueResponseConsistency(t *testing.T) {
	svc := startTestInternalAPIForSubmit(t)

	dispatchStatus, dispatchBody, dispatchRaw := postSubmitSyncTrue(t, svc, "/api/dispatch/submit")
	workerStatus, workerBody, workerRaw := postSubmitSyncTrue(t, svc, "/api/worker-run/submit")

	assertSubmitSyncRejected(t, dispatchStatus, dispatchBody)
	assertSubmitSyncRejected(t, workerStatus, workerBody)

	if dispatchStatus != workerStatus {
		t.Fatalf("status mismatch: dispatch=%d worker=%d", dispatchStatus, workerStatus)
	}
	if dispatchBody != workerBody {
		t.Fatalf("payload mismatch: dispatch=%+v worker=%+v", dispatchBody, workerBody)
	}
	if dispatchRaw != workerRaw {
		t.Fatalf("raw response mismatch: dispatch=%s worker=%s", dispatchRaw, workerRaw)
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

func postDispatchSubmitWithAutoStart(t *testing.T, svc *InternalAPI, requestID string, autoStart *bool) (int, map[string]any) {
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
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal request failed: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, "http://"+svc.listener.Addr().String()+"/api/dispatch/submit", bytes.NewReader(raw))
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

func TestHandleDispatchSubmit_QueryUsesIDFlag(t *testing.T) {
	svc := startTestInternalAPIForSubmit(t)
	status, body := postSubmitSyncFalse(t, svc, "/api/dispatch/submit", "req-dispatch-query-id")
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
	for _, key := range []string{"show", "events", "cancel"} {
		raw, ok := query[key]
		if !ok {
			t.Fatalf("missing query.%s", key)
		}
		value, ok := raw.(string)
		if !ok {
			t.Fatalf("query.%s should be string, got=%T", key, raw)
		}
		if !strings.Contains(value, "--id ") {
			t.Fatalf("query.%s should contain --id, got=%q", key, value)
		}
		if strings.Contains(value, "--run ") {
			t.Fatalf("query.%s should not contain --run, got=%q", key, value)
		}
	}
}

func TestHandleWorkerRunSubmit_QueryUsesIDFlag(t *testing.T) {
	svc := startTestInternalAPIForSubmit(t)
	status, body := postSubmitSyncFalse(t, svc, "/api/worker-run/submit", "req-worker-query-id")
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
	for _, key := range []string{"show", "events", "cancel"} {
		raw, ok := query[key]
		if !ok {
			t.Fatalf("missing query.%s", key)
		}
		value, ok := raw.(string)
		if !ok {
			t.Fatalf("query.%s should be string, got=%T", key, raw)
		}
		if !strings.Contains(value, "--id ") {
			t.Fatalf("query.%s should contain --id, got=%q", key, value)
		}
		if strings.Contains(value, "--run ") {
			t.Fatalf("query.%s should not contain --run, got=%q", key, value)
		}
	}
}

func TestHandleDispatchSubmit_ForwardsAutoStartFalse(t *testing.T) {
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
	status, body := postDispatchSubmitWithAutoStart(t, svc, "req-dispatch-auto-start-false", &autoStart)
	if status != http.StatusAccepted {
		t.Fatalf("unexpected status=%d body=%+v", status, body)
	}
	got := project.LastDispatchAutoStart()
	if got == nil || *got {
		t.Fatalf("expected forwarded auto_start=false, got=%v", got)
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

func TestHandleDispatchSubmit_RejectsOversizedBody(t *testing.T) {
	svc := startTestInternalAPIForSubmit(t)
	oversized := map[string]any{
		"project":    "demo",
		"ticket_id":  1,
		"request_id": "req-big-dispatch",
		"prompt":     strings.Repeat("x", int(internalAPIMaxJSONBodyBytes)),
		"sync":       false,
	}
	raw, err := json.Marshal(oversized)
	if err != nil {
		t.Fatalf("marshal request failed: %v", err)
	}
	status, body := postSubmitRaw(t, svc, "/api/dispatch/submit", bytes.NewReader(raw))
	if status != http.StatusBadRequest {
		t.Fatalf("unexpected status: %d body=%+v", status, body)
	}
	if body.Error != "bad_request" || !strings.Contains(body.Cause, "request body 过大") {
		t.Fatalf("unexpected response: %+v", body)
	}
}

func TestHandleWorkerRunSubmit_RejectsOversizedBody(t *testing.T) {
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
	status, body := postSubmitRaw(t, svc, "/api/worker-run/submit", bytes.NewReader(raw))
	if status != http.StatusBadRequest {
		t.Fatalf("unexpected status: %d body=%+v", status, body)
	}
	if body.Error != "bad_request" || !strings.Contains(body.Cause, "request body 过大") {
		t.Fatalf("unexpected response: %+v", body)
	}
}
