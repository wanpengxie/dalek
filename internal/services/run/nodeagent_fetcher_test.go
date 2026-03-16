package run

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	nodeagentsvc "dalek/internal/services/nodeagent"
)

func TestNodeAgentRunFetcher_FetchRun(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/api/node/run/query" {
			http.NotFound(w, r)
			return
		}
		var req nodeagentsvc.RunQueryRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request failed: %v", err)
		}
		_ = json.NewEncoder(w).Encode(nodeagentsvc.RunQueryResponse{
			Found:          true,
			RunID:          req.Meta.RunID,
			Status:         "running",
			Summary:        "remote running",
			SnapshotID:     "snap-1",
			VerifyTarget:   "test",
			LastEventType:  "run_verify_started",
			LastEventNote:  "verify started",
			UpdatedAt:      time.Date(2026, 3, 15, 12, 0, 0, 0, time.UTC),
			ProtocolSource: nodeagentsvc.ProtocolVersionV1,
		})
	}))
	defer server.Close()

	client, err := nodeagentsvc.NewClient(nodeagentsvc.ClientConfig{BaseURL: server.URL})
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}
	fetcher := NewNodeAgentRunFetcher(client, "demo", nodeagentsvc.ProtocolVersionV1)

	status, err := fetcher.FetchRun(context.Background(), 42)
	if err != nil {
		t.Fatalf("FetchRun failed: %v", err)
	}
	if !status.Found || status.RunID != 42 || status.Status != "running" || status.VerifyTarget != "test" {
		t.Fatalf("unexpected status: %+v", status)
	}
	if status.LastEventType != "run_verify_started" || status.LastEventNote != "verify started" {
		t.Fatalf("unexpected last event: %+v", status)
	}
}

func TestNodeAgentRunFetcher_FetchRunByRequestID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/api/node/run/query" {
			http.NotFound(w, r)
			return
		}
		var req nodeagentsvc.RunQueryRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request failed: %v", err)
		}
		if req.Meta.RequestID != "req-42" {
			t.Fatalf("unexpected request id: %+v", req.Meta)
		}
		_ = json.NewEncoder(w).Encode(nodeagentsvc.RunQueryResponse{
			Found:          true,
			RunID:          42,
			Status:         "reconciling",
			Summary:        "remote recovering",
			SnapshotID:     "snap-42",
			VerifyTarget:   "test",
			LastEventType:  "run_reconciled",
			LastEventNote:  "remote recovering",
			UpdatedAt:      time.Date(2026, 3, 16, 12, 0, 0, 0, time.UTC),
			ProtocolSource: nodeagentsvc.ProtocolVersionV1,
		})
	}))
	defer server.Close()

	client, err := nodeagentsvc.NewClient(nodeagentsvc.ClientConfig{BaseURL: server.URL})
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}
	fetcher := NewNodeAgentRunFetcher(client, "demo", nodeagentsvc.ProtocolVersionV1)

	status, err := fetcher.FetchRunByRequestID(context.Background(), "req-42")
	if err != nil {
		t.Fatalf("FetchRunByRequestID failed: %v", err)
	}
	if !status.Found || status.RunID != 42 || status.RequestID != "req-42" || status.Status != "reconciling" {
		t.Fatalf("unexpected status: %+v", status)
	}
}
