package daemon

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"

	"dalek/internal/contracts"
)

func startTestInternalAPIForDashboard(t *testing.T, project *testExecutionHostProject) *InternalAPI {
	t.Helper()

	if project == nil {
		project = &testExecutionHostProject{}
	}
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
	return svc
}

func doDashboardRequest(t *testing.T, svc *InternalAPI, method, path string) (int, []byte) {
	t.Helper()

	req, err := http.NewRequest(method, "http://"+svc.listener.Addr().String()+path, nil)
	if err != nil {
		t.Fatalf("new request failed: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("http do failed: %v", err)
	}
	defer resp.Body.Close()

	bodyRaw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response body failed: %v", err)
	}
	return resp.StatusCode, bodyRaw
}

func decodeDashboardAPIError(t *testing.T, raw []byte) internalSubmitAPIError {
	t.Helper()
	var got internalSubmitAPIError
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("decode api error failed: %v raw=%s", err, string(raw))
	}
	return got
}

func TestHandleOverview_Success(t *testing.T) {
	lastRunAt := time.Now().UTC().Round(time.Second)
	project := &testExecutionHostProject{
		dashboardResult: DashboardResult{
			TicketCounts: map[string]int{
				"backlog": 2,
			},
			WorkerStats: DashboardWorkerStats{
				Running:    1,
				MaxRunning: 3,
				Blocked:    1,
			},
			PlannerState: DashboardPlannerInfo{
				Dirty:       true,
				WakeVersion: 7,
				LastRunAt:   &lastRunAt,
				LastError:   "planner failed once",
			},
			MergeCounts: map[string]int{
				"proposed": 2,
			},
			InboxCounts: DashboardInboxCounts{
				Open:     3,
				Snoozed:  1,
				Blockers: 1,
			},
		},
	}
	svc := startTestInternalAPIForDashboard(t, project)

	status, raw := doDashboardRequest(t, svc, http.MethodGet, "/api/v1/overview?project=demo")
	if status != http.StatusOK {
		t.Fatalf("unexpected status=%d raw=%s", status, string(raw))
	}

	var got DashboardResult
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("decode overview failed: %v raw=%s", err, string(raw))
	}
	if got.TicketCounts["backlog"] != 2 {
		t.Fatalf("unexpected ticket_counts.backlog: %d", got.TicketCounts["backlog"])
	}
	if got.WorkerStats.Running != 1 || got.WorkerStats.MaxRunning != 3 || got.WorkerStats.Blocked != 1 {
		t.Fatalf("unexpected worker_stats: %+v", got.WorkerStats)
	}
	if !got.PlannerState.Dirty || got.PlannerState.WakeVersion != 7 {
		t.Fatalf("unexpected planner_state: %+v", got.PlannerState)
	}
	if got.MergeCounts["proposed"] != 2 {
		t.Fatalf("unexpected merge_counts.proposed: %d", got.MergeCounts["proposed"])
	}
	if got.InboxCounts.Open != 3 || got.InboxCounts.Snoozed != 1 || got.InboxCounts.Blockers != 1 {
		t.Fatalf("unexpected inbox_counts: %+v", got.InboxCounts)
	}
}

func TestHandlePlanner_Success(t *testing.T) {
	activeRunID := uint(99)
	cooldown := time.Now().UTC().Add(10 * time.Minute).Round(time.Second)
	lastRunAt := time.Now().UTC().Round(time.Second)
	project := &testExecutionHostProject{
		pmState: contracts.PMState{
			PlannerDirty:           true,
			PlannerWakeVersion:     11,
			PlannerActiveTaskRunID: &activeRunID,
			PlannerCooldownUntil:   &cooldown,
			PlannerLastRunAt:       &lastRunAt,
			PlannerLastError:       "planner timeout",
		},
	}
	svc := startTestInternalAPIForDashboard(t, project)

	status, raw := doDashboardRequest(t, svc, http.MethodGet, "/api/v1/planner?project=demo")
	if status != http.StatusOK {
		t.Fatalf("unexpected status=%d raw=%s", status, string(raw))
	}

	var got DashboardPlannerInfo
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("decode planner failed: %v raw=%s", err, string(raw))
	}
	if !got.Dirty || got.WakeVersion != 11 {
		t.Fatalf("unexpected planner basic fields: %+v", got)
	}
	if got.ActiveTaskRunID == nil || *got.ActiveTaskRunID != activeRunID {
		t.Fatalf("unexpected active_task_run_id: %+v", got.ActiveTaskRunID)
	}
	if got.CooldownUntil == nil || !got.CooldownUntil.Equal(cooldown) {
		t.Fatalf("unexpected cooldown_until: %+v", got.CooldownUntil)
	}
	if got.LastRunAt == nil || !got.LastRunAt.Equal(lastRunAt) {
		t.Fatalf("unexpected last_run_at: %+v", got.LastRunAt)
	}
	if got.LastError != "planner timeout" {
		t.Fatalf("unexpected last_error: %q", got.LastError)
	}
}

func TestHandleMerges_StatusFilter(t *testing.T) {
	project := &testExecutionHostProject{
		mergeItems: []contracts.MergeItem{
			{ID: 1, Status: contracts.MergeProposed, TicketID: 10},
			{ID: 2, Status: contracts.MergeMerged, TicketID: 11},
		},
	}
	svc := startTestInternalAPIForDashboard(t, project)

	status, raw := doDashboardRequest(t, svc, http.MethodGet, "/api/v1/merges?project=demo&status=proposed")
	if status != http.StatusOK {
		t.Fatalf("unexpected status=%d raw=%s", status, string(raw))
	}

	var got []contracts.MergeItem
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("decode merges failed: %v raw=%s", err, string(raw))
	}
	if len(got) != 1 || got[0].Status != contracts.MergeProposed {
		t.Fatalf("unexpected merges payload: %+v", got)
	}
	opt := project.LastMergeOptions()
	if opt.Status != contracts.MergeProposed {
		t.Fatalf("status filter not propagated, got=%q", opt.Status)
	}
	if opt.Limit != internalAPIDashboardListCap {
		t.Fatalf("limit mismatch: got=%d want=%d", opt.Limit, internalAPIDashboardListCap)
	}
}

func TestHandleInbox_StatusFilter(t *testing.T) {
	project := &testExecutionHostProject{
		inboxItems: []contracts.InboxItem{
			{ID: 1, Status: contracts.InboxOpen, Title: "open"},
			{ID: 2, Status: contracts.InboxDone, Title: "done"},
		},
	}
	svc := startTestInternalAPIForDashboard(t, project)

	status, raw := doDashboardRequest(t, svc, http.MethodGet, "/api/v1/inbox?project=demo&status=open")
	if status != http.StatusOK {
		t.Fatalf("unexpected status=%d raw=%s", status, string(raw))
	}

	var got []contracts.InboxItem
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("decode inbox failed: %v raw=%s", err, string(raw))
	}
	if len(got) != 1 || got[0].Status != contracts.InboxOpen {
		t.Fatalf("unexpected inbox payload: %+v", got)
	}
	opt := project.LastInboxOptions()
	if opt.Status != contracts.InboxOpen {
		t.Fatalf("status filter not propagated, got=%q", opt.Status)
	}
	if opt.Limit != internalAPIDashboardListCap {
		t.Fatalf("limit mismatch: got=%d want=%d", opt.Limit, internalAPIDashboardListCap)
	}
}

func TestDashboardEndpoints_RequireProjectQuery(t *testing.T) {
	svc := startTestInternalAPIForDashboard(t, &testExecutionHostProject{})
	for _, path := range []string{
		"/api/v1/overview",
		"/api/v1/planner",
		"/api/v1/merges",
		"/api/v1/inbox",
	} {
		status, raw := doDashboardRequest(t, svc, http.MethodGet, path)
		if status != http.StatusBadRequest {
			t.Fatalf("path=%s unexpected status=%d raw=%s", path, status, string(raw))
		}
		got := decodeDashboardAPIError(t, raw)
		if got.Error != "bad_request" {
			t.Fatalf("path=%s unexpected error code=%s", path, got.Error)
		}
	}
}

func TestDashboardEndpoints_MethodNotAllowed(t *testing.T) {
	svc := startTestInternalAPIForDashboard(t, &testExecutionHostProject{})
	for _, path := range []string{
		"/api/v1/overview?project=demo",
		"/api/v1/planner?project=demo",
		"/api/v1/merges?project=demo",
		"/api/v1/inbox?project=demo",
	} {
		status, raw := doDashboardRequest(t, svc, http.MethodPost, path)
		if status != http.StatusMethodNotAllowed {
			t.Fatalf("path=%s unexpected status=%d raw=%s", path, status, string(raw))
		}
		got := decodeDashboardAPIError(t, raw)
		if got.Error != "method_not_allowed" {
			t.Fatalf("path=%s unexpected error code=%s", path, got.Error)
		}
	}
}

func TestDashboardEndpoints_InvalidStatusQuery(t *testing.T) {
	svc := startTestInternalAPIForDashboard(t, &testExecutionHostProject{})

	status, raw := doDashboardRequest(t, svc, http.MethodGet, "/api/v1/merges?project=demo&status=unknown")
	if status != http.StatusBadRequest {
		t.Fatalf("merge status invalid should return 400, got=%d raw=%s", status, string(raw))
	}
	got := decodeDashboardAPIError(t, raw)
	if got.Error != "bad_request" {
		t.Fatalf("unexpected merge error code: %s", got.Error)
	}

	status, raw = doDashboardRequest(t, svc, http.MethodGet, "/api/v1/inbox?project=demo&status=unknown")
	if status != http.StatusBadRequest {
		t.Fatalf("inbox status invalid should return 400, got=%d raw=%s", status, string(raw))
	}
	got = decodeDashboardAPIError(t, raw)
	if got.Error != "bad_request" {
		t.Fatalf("unexpected inbox error code: %s", got.Error)
	}
}
