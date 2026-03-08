package main

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"dalek/internal/app"
)

const pmDashboardSchema = "dalek.pm.dashboard.v1"

func renderDashboardText(w io.Writer, result app.DashboardResult) error {
	if w == nil {
		return fmt.Errorf("nil writer")
	}

	var b strings.Builder
	fmt.Fprintln(&b, "=== Project Dashboard ===")

	fmt.Fprintln(&b, "-- Ticket Overview --")
	fmt.Fprintf(
		&b,
		"backlog=%d  queued=%d  active=%d  blocked=%d  done=%d  archived=%d\n",
		result.TicketCounts["backlog"],
		result.TicketCounts["queued"],
		result.TicketCounts["active"],
		result.TicketCounts["blocked"],
		result.TicketCounts["done"],
		result.TicketCounts["archived"],
	)

	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "-- Worker Utilization --")
	fmt.Fprintf(
		&b,
		"running=%d/%d  utilization=%s  blocked=%d\n",
		result.WorkerStats.Running,
		result.WorkerStats.MaxRunning,
		formatDashboardUtilization(result.WorkerStats.Running, result.WorkerStats.MaxRunning),
		result.WorkerStats.Blocked,
	)

	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "-- Planner Status --")
	fmt.Fprintf(
		&b,
		"dirty=%t  wake_version=%d  active_run=%s  cooldown_until=%s  last_run=%s",
		result.PlannerState.Dirty,
		result.PlannerState.WakeVersion,
		formatDashboardRunID(result.PlannerState.ActiveTaskRunID),
		formatDashboardTime(result.PlannerState.CooldownUntil, "not_set"),
		formatDashboardTime(result.PlannerState.LastRunAt, "never"),
	)
	if lastErr := strings.TrimSpace(result.PlannerState.LastError); lastErr != "" {
		fmt.Fprintf(&b, "  last_error=%s", lastErr)
	}
	fmt.Fprintln(&b)

	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "-- Merge Queue --")
	fmt.Fprintf(
		&b,
		"proposed=%d  checks_running=%d  ready=%d  approved=%d  merged=%d  discarded=%d  blocked=%d\n",
		result.MergeCounts["proposed"],
		result.MergeCounts["checks_running"],
		result.MergeCounts["ready"],
		result.MergeCounts["approved"],
		result.MergeCounts["merged"],
		result.MergeCounts["discarded"],
		result.MergeCounts["blocked"],
	)

	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "-- Inbox Todo --")
	fmt.Fprintf(
		&b,
		"open=%d  snoozed=%d  blockers=%d\n",
		result.InboxCounts.Open,
		result.InboxCounts.Snoozed,
		result.InboxCounts.Blockers,
	)

	_, err := io.WriteString(w, b.String())
	return err
}

func renderDashboardJSON(w io.Writer, result app.DashboardResult) error {
	if w == nil {
		return fmt.Errorf("nil writer")
	}

	payload := map[string]any{
		"schema": pmDashboardSchema,
		"data":   result,
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(payload)
}

func formatDashboardRunID(runID *uint) string {
	if runID == nil || *runID == 0 {
		return "none"
	}
	return fmt.Sprintf("%d", *runID)
}

func formatDashboardTime(v *time.Time, empty string) string {
	if v == nil || v.IsZero() {
		return empty
	}
	return v.Local().Format(time.RFC3339)
}

func formatDashboardUtilization(running, maxRunning int) string {
	if maxRunning <= 0 {
		return "n/a"
	}
	return fmt.Sprintf("%.1f%%", (float64(running)/float64(maxRunning))*100)
}
