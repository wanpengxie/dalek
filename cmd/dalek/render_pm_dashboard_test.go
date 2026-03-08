package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"dalek/internal/app"
)

func TestRenderDashboardText_SectionsAndValues(t *testing.T) {
	activeRun := uint(42)
	cooldown := time.Date(2026, 3, 8, 1, 0, 0, 0, time.UTC)
	lastRun := time.Date(2026, 3, 8, 0, 30, 0, 0, time.UTC)
	result := app.DashboardResult{
		TicketCounts: map[string]int{
			"backlog":  1,
			"queued":   2,
			"active":   3,
			"blocked":  4,
			"done":     5,
			"archived": 6,
		},
		WorkerStats: app.DashboardWorkerStats{
			Running:    3,
			MaxRunning: 6,
			Blocked:    4,
		},
		PlannerState: app.DashboardPlannerInfo{
			Dirty:           true,
			WakeVersion:     9,
			ActiveTaskRunID: &activeRun,
			CooldownUntil:   &cooldown,
			LastRunAt:       &lastRun,
			LastError:       "boom",
		},
		MergeCounts: map[string]int{
			"proposed":       1,
			"checks_running": 2,
			"ready":          3,
			"approved":       4,
			"merged":         5,
			"discarded":      6,
			"blocked":        7,
		},
		InboxCounts: app.DashboardInboxCounts{
			Open:     8,
			Snoozed:  9,
			Blockers: 10,
		},
	}

	var buf bytes.Buffer
	if err := renderDashboardText(&buf, result); err != nil {
		t.Fatalf("renderDashboardText error: %v", err)
	}
	out := buf.String()

	parts := []string{
		"=== Project Dashboard ===",
		"-- Ticket Overview --",
		"backlog=1  queued=2  active=3  blocked=4  done=5  archived=6",
		"-- Worker Utilization --",
		"running=3/6  utilization=50.0%  blocked=4",
		"-- Planner Status --",
		"dirty=true  wake_version=9  active_run=42",
		"cooldown_until=" + cooldown.Local().Format(time.RFC3339),
		"last_run=" + lastRun.Local().Format(time.RFC3339),
		"last_error=boom",
		"-- Merge Queue --",
		"proposed=1  checks_running=2  ready=3  approved=4  merged=5  discarded=6  blocked=7",
		"-- Inbox Todo --",
		"open=8  snoozed=9  blockers=10",
	}
	for _, part := range parts {
		if !strings.Contains(out, part) {
			t.Fatalf("missing output part %q in:\n%s", part, out)
		}
	}
}

func TestRenderDashboardText_DefaultPlaceholders(t *testing.T) {
	result := app.DashboardResult{
		WorkerStats: app.DashboardWorkerStats{
			Running:    0,
			MaxRunning: 0,
			Blocked:    0,
		},
	}

	var buf bytes.Buffer
	if err := renderDashboardText(&buf, result); err != nil {
		t.Fatalf("renderDashboardText error: %v", err)
	}
	out := buf.String()

	parts := []string{
		"utilization=n/a",
		"active_run=none",
		"cooldown_until=not_set",
		"last_run=never",
	}
	for _, part := range parts {
		if !strings.Contains(out, part) {
			t.Fatalf("missing output part %q in:\n%s", part, out)
		}
	}
}

func TestRenderDashboardJSON_Envelope(t *testing.T) {
	activeRun := uint(7)
	lastRun := time.Date(2026, 3, 8, 0, 0, 0, 0, time.UTC)
	result := app.DashboardResult{
		TicketCounts: map[string]int{
			"backlog": 1,
		},
		WorkerStats: app.DashboardWorkerStats{
			Running:    1,
			MaxRunning: 2,
			Blocked:    3,
		},
		PlannerState: app.DashboardPlannerInfo{
			Dirty:           true,
			WakeVersion:     5,
			ActiveTaskRunID: &activeRun,
			LastRunAt:       &lastRun,
		},
		MergeCounts: map[string]int{
			"proposed": 4,
		},
		InboxCounts: app.DashboardInboxCounts{
			Open:     5,
			Snoozed:  6,
			Blockers: 7,
		},
	}

	var buf bytes.Buffer
	if err := renderDashboardJSON(&buf, result); err != nil {
		t.Fatalf("renderDashboardJSON error: %v", err)
	}

	var payload struct {
		Schema string              `json:"schema"`
		Data   app.DashboardResult `json:"data"`
	}
	if err := json.Unmarshal(buf.Bytes(), &payload); err != nil {
		t.Fatalf("json unmarshal error: %v\nraw: %s", err, buf.String())
	}
	if payload.Schema != pmDashboardSchema {
		t.Fatalf("schema mismatch: got=%q want=%q", payload.Schema, pmDashboardSchema)
	}
	if payload.Data.WorkerStats != result.WorkerStats {
		t.Fatalf("worker stats mismatch: got=%+v want=%+v", payload.Data.WorkerStats, result.WorkerStats)
	}
	if payload.Data.InboxCounts != result.InboxCounts {
		t.Fatalf("inbox counts mismatch: got=%+v want=%+v", payload.Data.InboxCounts, result.InboxCounts)
	}
	if payload.Data.TicketCounts["backlog"] != 1 {
		t.Fatalf("ticket count mismatch: got=%d want=1", payload.Data.TicketCounts["backlog"])
	}
	if payload.Data.MergeCounts["proposed"] != 4 {
		t.Fatalf("merge count mismatch: got=%d want=4", payload.Data.MergeCounts["proposed"])
	}
	if payload.Data.PlannerState.ActiveTaskRunID == nil || *payload.Data.PlannerState.ActiveTaskRunID != activeRun {
		t.Fatalf("active task run mismatch: got=%v want=%d", payload.Data.PlannerState.ActiveTaskRunID, activeRun)
	}
	if payload.Data.PlannerState.LastRunAt == nil || !payload.Data.PlannerState.LastRunAt.Equal(lastRun) {
		t.Fatalf("last run mismatch: got=%v want=%s", payload.Data.PlannerState.LastRunAt, lastRun.Format(time.RFC3339))
	}
}

func TestRenderDashboardNilWriter(t *testing.T) {
	if err := renderDashboardText(nil, app.DashboardResult{}); err == nil {
		t.Fatalf("expected error for nil writer in text renderer")
	}
	if err := renderDashboardJSON(nil, app.DashboardResult{}); err == nil {
		t.Fatalf("expected error for nil writer in json renderer")
	}
}
