package main

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"dalek/internal/app"
)

func renderPMWorkspaceStateText(w io.Writer, state app.PMWorkspaceState) error {
	if w == nil {
		return fmt.Errorf("nil writer")
	}
	var b strings.Builder
	fmt.Fprintln(&b, "=== PM Workspace State ===")
	fmt.Fprintf(&b, "phase=%s  ticket=%s  status=%s  feature=%s\n",
		emptyAsValue(state.Runtime.CurrentPhase, "none"),
		emptyAsValue(state.Runtime.CurrentTicket, "none"),
		emptyAsValue(state.Runtime.CurrentStatus, "idle"),
		emptyAsValue(state.Runtime.CurrentFeature, "none"),
	)
	fmt.Fprintf(&b, "last_action=%s\n", emptyAsValue(state.Runtime.LastAction, "-"))
	fmt.Fprintf(&b, "next_action=%s\n", emptyAsValue(state.Runtime.NextAction, "-"))
	fmt.Fprintf(&b, "blocker=%s\n", emptyAsValue(state.Runtime.Blocker, "无"))
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "-- Files --")
	fmt.Fprintf(&b, "plan=%s\n", emptyAsValue(state.Files.PlanPath, "-"))
	fmt.Fprintf(&b, "state=%s\n", emptyAsValue(state.Files.StatePath, "-"))
	fmt.Fprintf(&b, "acceptance=%s\n", emptyAsValue(state.Files.AcceptancePath, "-"))
	fmt.Fprintln(&b)
	if state.Feature.Title != "" || len(state.Feature.Tickets) > 0 || state.Feature.Acceptance.Status != "" {
		fmt.Fprintln(&b, "-- Feature --")
		fmt.Fprintf(&b, "title=%s  acceptance_status=%s  planned_tickets=%d\n",
			emptyAsValue(state.Feature.Title, "none"),
			emptyAsValue(state.Feature.Acceptance.Status, "pending"),
			len(state.Feature.Tickets),
		)
		if len(state.Feature.Docs) > 0 {
			fmt.Fprintf(&b, "docs=%s\n", formatPMFeatureDocs(state.Feature.Docs))
		}
		if len(state.Feature.Tickets) > 0 {
			for _, ticket := range state.Feature.Tickets {
				fmt.Fprintf(&b, "ticket=%s batch=%s status=%s deps=%s deliverable=%s\n",
					emptyAsValue(ticket.ID, "-"),
					emptyAsValue(ticket.Batch, "-"),
					emptyAsValue(ticket.Status, "planned"),
					formatStringList(ticket.DependsOn),
					emptyAsValue(ticket.Deliverable, "-"),
				)
			}
		}
		if len(state.Feature.Acceptance.RequiredChecks) > 0 {
			fmt.Fprintf(&b, "acceptance_checks=%d\n", len(state.Feature.Acceptance.RequiredChecks))
		}
		if state.Feature.Acceptance.StartupCommand != "" || state.Feature.Acceptance.URL != "" {
			fmt.Fprintf(&b, "acceptance_env: cmd=%s url=%s\n",
				emptyAsValue(state.Feature.Acceptance.StartupCommand, "-"),
				emptyAsValue(state.Feature.Acceptance.URL, "-"),
			)
		}
		if state.Feature.Acceptance.Conclusion != "" {
			fmt.Fprintf(&b, "acceptance_conclusion=%s\n", state.Feature.Acceptance.Conclusion)
		}
		fmt.Fprintln(&b)
	}
	fmt.Fprintln(&b, "-- Snapshot --")
	fmt.Fprintf(&b, "tickets: backlog=%d queued=%d active=%d blocked=%d done=%d archived=%d\n",
		state.Snapshot.TicketCounts["backlog"],
		state.Snapshot.TicketCounts["queued"],
		state.Snapshot.TicketCounts["active"],
		state.Snapshot.TicketCounts["blocked"],
		state.Snapshot.TicketCounts["done"],
		state.Snapshot.TicketCounts["archived"],
	)
	fmt.Fprintf(&b, "workers: running=%d/%d blocked=%d\n",
		state.Snapshot.WorkerStats.Running,
		state.Snapshot.WorkerStats.MaxRunning,
		state.Snapshot.WorkerStats.Blocked,
	)
	fmt.Fprintf(&b, "planner: dirty=%t wake_version=%d active_run=%s last_run=%s\n",
		state.Snapshot.PlannerState.Dirty,
		state.Snapshot.PlannerState.WakeVersion,
		formatDashboardRunID(state.Snapshot.PlannerState.ActiveTaskRunID),
		formatPMWorkspaceTime(state.Snapshot.PlannerState.LastRunAt),
	)
	fmt.Fprintf(&b, "merges: proposed=%d checks_running=%d ready=%d approved=%d merged=%d discarded=%d blocked=%d\n",
		state.Snapshot.MergeCounts["proposed"],
		state.Snapshot.MergeCounts["checks_running"],
		state.Snapshot.MergeCounts["ready"],
		state.Snapshot.MergeCounts["approved"],
		state.Snapshot.MergeCounts["merged"],
		state.Snapshot.MergeCounts["discarded"],
		state.Snapshot.MergeCounts["blocked"],
	)
	fmt.Fprintf(&b, "inbox: open=%d snoozed=%d blockers=%d\n",
		state.Snapshot.InboxCounts.Open,
		state.Snapshot.InboxCounts.Snoozed,
		state.Snapshot.InboxCounts.Blockers,
	)
	fmt.Fprintln(&b)
	fmt.Fprintf(&b, "updated_at=%s\n", formatPMWorkspaceTime(&state.UpdatedAt))
	_, err := io.WriteString(w, b.String())
	return err
}

func formatPMWorkspaceTime(v *time.Time) string {
	if v == nil || v.IsZero() {
		return "never"
	}
	return v.UTC().Format(time.RFC3339)
}

func emptyAsValue(v, fallback string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return fallback
	}
	return v
}

func formatPMFeatureDocs(docs []app.PMWorkspaceDoc) string {
	if len(docs) == 0 {
		return "-"
	}
	items := make([]string, 0, len(docs))
	for _, doc := range docs {
		items = append(items, fmt.Sprintf("%s=%s(exists=%t)",
			emptyAsValue(doc.Kind, "doc"),
			emptyAsValue(doc.Path, "-"),
			doc.Exists,
		))
	}
	sort.Strings(items)
	return strings.Join(items, ", ")
}

func formatStringList(items []string) string {
	if len(items) == 0 {
		return "-"
	}
	parts := make([]string, 0, len(items))
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		parts = append(parts, item)
	}
	if len(parts) == 0 {
		return "-"
	}
	return strings.Join(parts, ",")
}
