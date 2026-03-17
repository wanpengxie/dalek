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
			Label:     "backend",
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
	m.routesByRunID[42] = app.TaskRouteInfo{
		Role:        "dev",
		RoleSource:  "auto_route_prompt",
		RouteReason: "prompt matched development keywords",
		RouteMode:   "remote",
		RouteTarget: "http://b.example",
		RemoteRunID: 4201,
	}
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
	if !strings.Contains(got, "route: dev  http://b.example  remote=r4201") {
		t.Fatalf("should render route info, got=%q", got)
	}
	if !strings.Contains(got, "route_reason: auto_route_prompt  prompt matched development keywords") {
		t.Fatalf("should render route reason, got=%q", got)
	}
	if !strings.Contains(got, "last_event: task_dispatched") {
		t.Fatalf("should render last event, got=%q", got)
	}
	if !strings.Contains(got, "标签: backend") {
		t.Fatalf("should render label field, got=%q", got)
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
	m.routesByRunID[view.TaskRunID] = app.TaskRouteInfo{
		Role:        "run",
		RoleSource:  "auto_route_prompt",
		RouteReason: "prompt matched verify/test keywords",
		RouteMode:   "remote",
		RouteTarget: "http://c.example",
	}
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
	if !strings.Contains(got, "worker_log观察窗") {
		t.Fatalf("should render simplified output title, got=%q", got)
	}
	if strings.Contains(got, "line-1") || strings.Contains(got, "line-2") {
		t.Fatalf("should trim old lines in simplified output, got=%q", got)
	}
	if !strings.Contains(got, "line-6") {
		t.Fatalf("should keep latest line in simplified output, got=%q", got)
	}
}

func TestInspectorMiddleView_ShowsEventNoteFacts(t *testing.T) {
	m := newModel(nil, nil, "")
	now := time.Now().UTC().Add(-2 * time.Second)
	view := app.TicketView{
		Ticket: contracts.Ticket{
			ID:    3,
			Title: "事实观察",
		},
		LatestWorker: &contracts.Worker{
			ID:     33,
			Status: contracts.WorkerRunning,
		},
		SessionAlive:       true,
		DerivedStatus:      contracts.TicketActive,
		TaskRunID:          303,
		LastEventType:      "task_stream",
		LastEventNote:      "读取 ticket 相关模型并准备修改",
		SemanticNextAction: "continue",
		SemanticSummary:    "继续实现",
		RuntimeSummary:     "处理中",
		LastEventAt:        &now,
	}
	m.applyViews([]app.TicketView{view})
	m.routesByRunID[303] = app.TaskRouteInfo{
		Role:        "run",
		RoleSource:  "auto_route_prompt",
		RouteReason: "prompt matched verify/test keywords",
		RouteMode:   "remote",
		RouteTarget: "http://c.example",
	}
	for i, ref := range m.rowRefs {
		if ref.kind == rowTicket && ref.ticketID == view.Ticket.ID {
			m.table.SetCursor(i)
			break
		}
	}

	got := ansi.Strip(m.inspectorMiddleView(120))
	if !strings.Contains(got, "PM事实观察") {
		t.Fatalf("should render PM facts title, got=%q", got)
	}
	if !strings.Contains(got, "event_note: 读取 ticket 相关模型并准备修改") {
		t.Fatalf("should render event_note facts, got=%q", got)
	}
	if !strings.Contains(got, "next_action: continue") {
		t.Fatalf("should render next_action in facts panel, got=%q", got)
	}
	if !strings.Contains(got, "route: run  http://c.example") {
		t.Fatalf("should render route in facts panel, got=%q", got)
	}
	if !strings.Contains(got, "route_reason: prompt matched verify/test keywords") {
		t.Fatalf("should render route reason in facts panel, got=%q", got)
	}
}

func TestInspectorView_UsesThreePanelsOnWideScreen(t *testing.T) {
	m := newModel(nil, nil, "")
	view := app.TicketView{
		Ticket: contracts.Ticket{
			ID:    4,
			Title: "三栏布局",
		},
		LatestWorker: &contracts.Worker{
			ID:      44,
			Status:  contracts.WorkerRunning,
			LogPath: "/tmp/w44/stream.log",
		},
		SessionAlive:       true,
		DerivedStatus:      contracts.TicketActive,
		LastEventType:      "task_stream",
		LastEventNote:      "event note",
		SemanticNextAction: "continue",
	}
	m.applyViews([]app.TicketView{view})
	for i, ref := range m.rowRefs {
		if ref.kind == rowTicket && ref.ticketID == view.Ticket.ID {
			m.table.SetCursor(i)
			break
		}
	}

	got := ansi.Strip(m.inspectorView(150))
	if !strings.Contains(got, "元信息") {
		t.Fatalf("wide inspector should contain meta panel, got=%q", got)
	}
	if !strings.Contains(got, "PM事实观察") {
		t.Fatalf("wide inspector should contain PM facts panel, got=%q", got)
	}
	if !strings.Contains(got, "worker_log观察窗") {
		t.Fatalf("wide inspector should contain worker log panel, got=%q", got)
	}
}
