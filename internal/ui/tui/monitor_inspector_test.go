package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"dalek/internal/app"
	"dalek/internal/contracts"
)

func TestInspectorLeftView_ShowsDispatchRunningProcess(t *testing.T) {
	m := newModel(nil, nil, "")
	now := time.Now().UTC().Add(-3 * time.Second)
	view := app.TicketView{
		Ticket: contracts.Ticket{
			ID:        1,
			Title:     "dispatch 可视化",
			UpdatedAt: now,
		},
		LatestWorker: &contracts.Worker{
			ID:                 11,
			Status:             contracts.WorkerRunning,
			WorktreePath:       "/tmp/worktree/t1",
			RuntimePaneCommand: "codex exec",
			RuntimePaneMode:    "normal",
		},
		SessionAlive:       true,
		DerivedStatus:      contracts.TicketActive,
		TaskRunID:          42,
		RuntimeHealthState: contracts.TaskHealthBusy,
		RuntimeSummary:     "正在执行 worker 命令",
		RuntimeObservedAt:  &now,
		SemanticPhase:      contracts.TaskPhaseImplementing,
		SemanticNextAction: "continue",
		SemanticSummary:    "继续实现中",
		SemanticReportedAt: &now,
		LastEventType:      "task_dispatched",
		LastEventNote:      "dispatch 已发出",
		LastEventAt:        &now,
	}
	m.applyViews([]app.TicketView{view})
	for i, ref := range m.rowRefs {
		if ref.kind == rowTicket && ref.ticketID == view.Ticket.ID {
			m.table.SetCursor(i)
			break
		}
	}
	m.dispatchTicketID = view.Ticket.ID

	got := ansi.Strip(m.inspectorLeftView(140))
	if !strings.Contains(got, "dispatch/running") {
		t.Fatalf("should render dispatch/running title, got=%q", got)
	}
	if !strings.Contains(got, "流程: dispatch请求中") {
		t.Fatalf("should render process state, got=%q", got)
	}
	if !strings.Contains(got, "run: r42") {
		t.Fatalf("should render run id, got=%q", got)
	}
	if !strings.Contains(got, "last_event: task_dispatched") {
		t.Fatalf("should render last event, got=%q", got)
	}
}

func TestInspectorRightView_SimplifiesTailToRecentLines(t *testing.T) {
	m := newModel(nil, nil, "")
	now := time.Now().UTC().Add(-2 * time.Second)
	view := app.TicketView{
		Ticket: contracts.Ticket{
			ID:    2,
			Title: "tail 简化",
		},
		LatestWorker: &contracts.Worker{
			ID:     22,
			Status: contracts.WorkerRunning,
		},
		SessionAlive: true,
	}
	m.applyViews([]app.TicketView{view})
	for i, ref := range m.rowRefs {
		if ref.kind == rowTicket && ref.ticketID == view.Ticket.ID {
			m.table.SetCursor(i)
			break
		}
	}
	m.tailRef = rowRef{kind: rowTicket, ticketID: view.Ticket.ID}
	m.tailUpdatedAt = now
	m.tailPreview = contracts.TailPreview{
		Lines: []string{"line-1", "line-2", "line-3", "line-4", "line-5", "line-6"},
	}

	got := ansi.Strip(m.inspectorRightView(120))
	if !strings.Contains(got, "实时输出(简版)") {
		t.Fatalf("should render simplified output title, got=%q", got)
	}
	if strings.Contains(got, "line-1") || strings.Contains(got, "line-2") {
		t.Fatalf("should trim old lines in simplified output, got=%q", got)
	}
	if !strings.Contains(got, "line-6") {
		t.Fatalf("should keep latest line in simplified output, got=%q", got)
	}
}
