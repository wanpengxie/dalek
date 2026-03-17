package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestChannelPMActionAdapter_DispatchTicket_AutoRoutesToTaskRequest(t *testing.T) {
	p := newIntegrationProject(t)
	ctx := context.Background()

	tk, err := p.CreateTicketWithDescription(ctx, "auto route dispatch", "dispatch should become task request")
	if err != nil {
		t.Fatalf("CreateTicket failed: %v", err)
	}

	var submitCalls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/worker-run/submit":
			submitCalls.Add(1)
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatalf("decode submit payload failed: %v", err)
			}
			if strings.TrimSpace(payload["prompt"].(string)) != "continue fixing ticket" {
				t.Fatalf("unexpected prompt: %#v", payload["prompt"])
			}
			w.WriteHeader(http.StatusAccepted)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"accepted":    true,
				"project":     "demo",
				"request_id":  "legacy-dispatch-req-1",
				"task_run_id": 211,
				"ticket_id":   tk.ID,
				"worker_id":   17,
			})
		case r.Method == http.MethodGet && r.URL.Path == "/api/runs/211":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"run": map[string]any{
					"run_id":               211,
					"project":              "demo",
					"owner_type":           "worker",
					"task_type":            "worker_run",
					"ticket_id":            tk.ID,
					"worker_id":            17,
					"orchestration_state":  "running",
					"runtime_health_state": "busy",
					"runtime_needs_user":   false,
					"runtime_summary":      "remote worker accepted",
					"semantic_next_action": "continue",
					"semantic_summary":     "remote worker accepted",
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

	adapter := channelPMActionAdapter{svc: p.pm, project: p}
	res, err := adapter.DispatchTicket(ctx, tk.ID, "continue fixing ticket")
	if err != nil {
		t.Fatalf("DispatchTicket failed: %v", err)
	}
	if submitCalls.Load() != 1 {
		t.Fatalf("expected remote submit call, got=%d", submitCalls.Load())
	}
	if res.TicketID != tk.ID || res.TaskRunID == 0 || res.WorkerID != 17 {
		t.Fatalf("unexpected dispatch result: %+v", res)
	}
}
