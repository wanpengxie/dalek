package daemon

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"dalek/internal/contracts"
)

func startTestInternalAPIForTickets(t *testing.T, project *testExecutionHostProject) *InternalAPI {
	t.Helper()

	if project == nil {
		project = &testExecutionHostProject{}
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
	return svc
}

func requestInternalAPIJSON(t *testing.T, svc *InternalAPI, method, path string, body io.Reader) (int, map[string]any) {
	t.Helper()

	req, err := http.NewRequest(method, "http://"+svc.listener.Addr().String()+path, body)
	if err != nil {
		t.Fatalf("new request failed: %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("http do failed: %v", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response failed: %v", err)
	}
	decoded := map[string]any{}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &decoded); err != nil {
			t.Fatalf("decode response failed: %v raw=%s", err, string(raw))
		}
	}
	return resp.StatusCode, decoded
}

func TestHandleTickets_ListAndStatusFilter(t *testing.T) {
	now := time.Date(2026, 3, 8, 15, 4, 0, 0, time.UTC)
	startedAt := now.Add(-30 * time.Minute)
	project := &testExecutionHostProject{
		ticketViews: []TicketView{
			{
				Ticket: contracts.Ticket{
					ID:             11,
					CreatedAt:      now,
					UpdatedAt:      now,
					Title:          "active ticket",
					Description:    "desc",
					Label:          "web",
					WorkflowStatus: contracts.TicketActive,
					Priority:       3,
				},
				LatestWorker: &contracts.Worker{
					ID:        21,
					TicketID:  11,
					Status:    contracts.WorkerRunning,
					Branch:    "ts/demo-ticket-11",
					LogPath:   "/tmp/w21.log",
					StartedAt: &startedAt,
					CreatedAt: now,
					UpdatedAt: now,
				},
				SessionAlive:       true,
				DerivedStatus:      contracts.TicketActive,
				Capability:         contracts.TicketCapability{CanQueueRun: true, CanDispatch: true},
				TaskRunID:          901,
				RuntimeHealthState: contracts.TaskHealthBusy,
				RuntimeSummary:     "running",
				LastEventType:      "task.runtime",
			},
			{
				Ticket: contracts.Ticket{
					ID:             12,
					CreatedAt:      now,
					UpdatedAt:      now,
					Title:          "blocked ticket",
					Description:    "wait",
					Label:          "backend",
					WorkflowStatus: contracts.TicketBlocked,
					Priority:       1,
				},
				DerivedStatus: contracts.TicketBlocked,
				Capability:    contracts.TicketCapability{Reason: "等待输入"},
			},
		},
	}
	svc := startTestInternalAPIForTickets(t, project)

	status, body := requestInternalAPIJSON(t, svc, http.MethodGet, "/api/v1/tickets?project=demo", nil)
	if status != http.StatusOK {
		t.Fatalf("unexpected status=%d body=%+v", status, body)
	}
	ticketsAny, ok := body["tickets"].([]any)
	if !ok {
		t.Fatalf("tickets should be array, got=%T", body["tickets"])
	}
	if len(ticketsAny) != 2 {
		t.Fatalf("unexpected ticket count: got=%d", len(ticketsAny))
	}
	first, ok := ticketsAny[0].(map[string]any)
	if !ok {
		t.Fatalf("ticket item should be object, got=%T", ticketsAny[0])
	}
	ticketAny, ok := first["ticket"].(map[string]any)
	if !ok {
		t.Fatalf("ticket field should be object, got=%T", first["ticket"])
	}
	if gotID, _ := ticketAny["id"].(float64); uint(gotID) != 11 {
		t.Fatalf("unexpected first ticket id: got=%v", ticketAny["id"])
	}

	status, body = requestInternalAPIJSON(t, svc, http.MethodGet, "/api/v1/tickets?project=demo&status=active", nil)
	if status != http.StatusOK {
		t.Fatalf("unexpected status=%d body=%+v", status, body)
	}
	ticketsAny, ok = body["tickets"].([]any)
	if !ok {
		t.Fatalf("tickets should be array, got=%T", body["tickets"])
	}
	if len(ticketsAny) != 1 {
		t.Fatalf("unexpected filtered ticket count: got=%d", len(ticketsAny))
	}
	filtered, ok := ticketsAny[0].(map[string]any)
	if !ok {
		t.Fatalf("filtered item should be object, got=%T", ticketsAny[0])
	}
	ticketAny, ok = filtered["ticket"].(map[string]any)
	if !ok {
		t.Fatalf("ticket field should be object, got=%T", filtered["ticket"])
	}
	if gotID, _ := ticketAny["id"].(float64); uint(gotID) != 11 {
		t.Fatalf("unexpected filtered ticket id: got=%v", ticketAny["id"])
	}
}

func TestHandleTickets_Show(t *testing.T) {
	project := &testExecutionHostProject{
		ticketViews: []TicketView{{
			Ticket: contracts.Ticket{ID: 7, Title: "ticket-7", WorkflowStatus: contracts.TicketBacklog},
		}},
	}
	svc := startTestInternalAPIForTickets(t, project)

	status, body := requestInternalAPIJSON(t, svc, http.MethodGet, "/api/v1/tickets/7?project=demo", nil)
	if status != http.StatusOK {
		t.Fatalf("unexpected status=%d body=%+v", status, body)
	}
	ticket, ok := body["ticket"].(map[string]any)
	if !ok {
		t.Fatalf("ticket should be object, got=%T", body["ticket"])
	}
	ticketModel, ok := ticket["ticket"].(map[string]any)
	if !ok {
		t.Fatalf("ticket.ticket should be object, got=%T", ticket["ticket"])
	}
	if gotID, _ := ticketModel["id"].(float64); uint(gotID) != 7 {
		t.Fatalf("unexpected ticket id: got=%v", ticketModel["id"])
	}
}

func TestHandleTickets_ShowNotFound(t *testing.T) {
	svc := startTestInternalAPIForTickets(t, &testExecutionHostProject{})

	status, body := requestInternalAPIJSON(t, svc, http.MethodGet, "/api/v1/tickets/99?project=demo", nil)
	if status != http.StatusNotFound {
		t.Fatalf("unexpected status=%d body=%+v", status, body)
	}
	if got := strings.TrimSpace(asString(body["error"])); got != "not_found" {
		t.Fatalf("unexpected error code: %q", got)
	}
}

func TestHandleTickets_MissingProject(t *testing.T) {
	svc := startTestInternalAPIForTickets(t, &testExecutionHostProject{})

	status, body := requestInternalAPIJSON(t, svc, http.MethodGet, "/api/v1/tickets", nil)
	if status != http.StatusBadRequest {
		t.Fatalf("unexpected status=%d body=%+v", status, body)
	}
	if got := strings.TrimSpace(asString(body["error"])); got != "bad_request" {
		t.Fatalf("unexpected error code: %q", got)
	}
}

func TestHandleTickets_InvalidStatus(t *testing.T) {
	svc := startTestInternalAPIForTickets(t, &testExecutionHostProject{})

	status, body := requestInternalAPIJSON(t, svc, http.MethodGet, "/api/v1/tickets?project=demo&status=unknown", nil)
	if status != http.StatusBadRequest {
		t.Fatalf("unexpected status=%d body=%+v", status, body)
	}
	if got := strings.TrimSpace(asString(body["error"])); got != "bad_request" {
		t.Fatalf("unexpected error code: %q", got)
	}
}

func TestHandleTickets_MethodNotAllowed(t *testing.T) {
	svc := startTestInternalAPIForTickets(t, &testExecutionHostProject{})

	status, body := requestInternalAPIJSON(t, svc, http.MethodPost, "/api/v1/tickets?project=demo", nil)
	if status != http.StatusMethodNotAllowed {
		t.Fatalf("unexpected status=%d body=%+v", status, body)
	}
	if got := strings.TrimSpace(asString(body["error"])); got != "method_not_allowed" {
		t.Fatalf("unexpected error code: %q", got)
	}
}

func TestParseTicketRoute(t *testing.T) {
	ticketID, tail, ok := parseTicketRoute("/api/v1/tickets/123")
	if !ok || ticketID != 123 || tail != "" {
		t.Fatalf("unexpected parse result: ok=%v ticket_id=%d tail=%q", ok, ticketID, tail)
	}
	if _, _, ok := parseTicketRoute("/api/v1/tickets/x"); ok {
		t.Fatalf("expected invalid id route to fail")
	}
	if _, _, ok := parseTicketRoute("/api/v1/tickets/1/events/extra"); ok {
		t.Fatalf("expected deep path route to fail")
	}
}

func asString(v any) string {
	s, _ := v.(string)
	return s
}
