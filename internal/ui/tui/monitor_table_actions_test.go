package tui

import (
	"dalek/internal/contracts"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"dalek/internal/app"
)

func keyRune(r rune) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}}
}

func keyEnter() tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyEnter}
}

func TestUpdateTable_DeniedActionShowsBoundaryMessage(t *testing.T) {
	m := newModel(nil, nil, "")
	m.rowRefs = []rowRef{{kind: rowTicket, section: "done", ticketID: 10}}
	m.viewsByID = map[uint]app.TicketView{
		10: {
			Ticket:     contracts.Ticket{ID: 10, WorkflowStatus: contracts.TicketDone},
			Capability: app.TicketView{}.Capability, // zero
		},
	}
	v := m.viewsByID[10]
	v.Capability.Reason = "已完成"
	m.viewsByID[10] = v

	gotModel, cmd := m.updateTable(keyRune('s')) // done 分区不允许 start
	if cmd != nil {
		t.Fatalf("denied action should not schedule cmd")
	}
	got := gotModel.(model)
	if !strings.Contains(got.status, "不支持 启动(s)") || !strings.Contains(got.status, "已完成") {
		t.Fatalf("unexpected denied status: %q", got.status)
	}
}

func TestUpdateTable_AllowedActionReturnsCommand(t *testing.T) {
	m := newModel(nil, nil, "")
	m.rowRefs = []rowRef{{kind: rowTicket, section: "running", ticketID: 10}}
	m.viewsByID = map[uint]app.TicketView{
		10: {
			Ticket: contracts.Ticket{ID: 10, WorkflowStatus: contracts.TicketActive},
		},
	}
	v := m.viewsByID[10]
	v.Capability.CanStop = true
	m.viewsByID[10] = v

	gotModel, cmd := m.updateTable(keyRune('i')) // running 分区允许 interrupt
	if cmd == nil {
		t.Fatalf("allowed action should return cmd")
	}
	got := gotModel.(model)
	if !strings.Contains(got.status, "中断中 t10") {
		t.Fatalf("unexpected status: %q", got.status)
	}
}

func TestSelectedTicketForAction_BacklogRunningWorkerAllowsDispatch(t *testing.T) {
	m := newModel(nil, nil, "")
	m.rowRefs = []rowRef{{kind: rowTicket, section: "backlog", ticketID: 10}}
	m.viewsByID = map[uint]app.TicketView{
		10: {
			Ticket: contracts.Ticket{ID: 10, WorkflowStatus: contracts.TicketBacklog},
		},
	}
	v := m.viewsByID[10]
	v.Capability.CanQueueRun = true
	v.Capability.CanDispatch = true
	m.viewsByID[10] = v

	id, ok, denied := m.selectedTicketForAction(ticketActionDispatch)
	if denied != "" {
		t.Fatalf("unexpected denied: %s", denied)
	}
	if !ok || id != 10 {
		t.Fatalf("expected dispatch allowed for backlog fallback, ok=%v id=%d", ok, id)
	}
}

func TestSelectedTicketForAction_BacklogWithoutSessionStillDenied(t *testing.T) {
	m := newModel(nil, nil, "")
	m.rowRefs = []rowRef{{kind: rowTicket, section: "backlog", ticketID: 10}}
	m.viewsByID = map[uint]app.TicketView{
		10: {
			Ticket: contracts.Ticket{ID: 10, WorkflowStatus: contracts.TicketBacklog},
		},
	}
	v := m.viewsByID[10]
	v.Capability.CanQueueRun = false
	v.Capability.CanDispatch = false
	v.Capability.Reason = "worker 缺少 session"
	m.viewsByID[10] = v

	_, ok, denied := m.selectedTicketForAction(ticketActionDispatch)
	if ok {
		t.Fatalf("expected dispatch denied without session")
	}
	if !strings.Contains(denied, "不支持 派发(p)") || !strings.Contains(denied, "worker 缺少 session") {
		t.Fatalf("unexpected denied message: %q", denied)
	}
}

func TestUpdateTable_WorkerRunDenied(t *testing.T) {
	m := newModel(nil, nil, "")
	m.rowRefs = []rowRef{{kind: rowTicket, section: "done", ticketID: 10}}
	m.viewsByID = map[uint]app.TicketView{
		10: {
			Ticket: contracts.Ticket{ID: 10, WorkflowStatus: contracts.TicketDone},
		},
	}
	v := m.viewsByID[10]
	v.Capability.CanQueueRun = false
	v.Capability.CanDispatch = false
	v.Capability.Reason = "已完成"
	m.viewsByID[10] = v

	gotModel, cmd := m.updateTable(keyRune('r'))
	if cmd != nil {
		t.Fatalf("denied worker run should not schedule cmd")
	}
	got := gotModel.(model)
	if !strings.Contains(got.status, "不支持 重新跑(r)") || !strings.Contains(got.status, "已完成") {
		t.Fatalf("unexpected denied status: %q", got.status)
	}
}

func TestUpdateTable_WorkerRunAllowed(t *testing.T) {
	m := newModel(nil, nil, "")
	m.projectName = "demo"
	m.rowRefs = []rowRef{{kind: rowTicket, section: "running", ticketID: 10}}
	m.viewsByID = map[uint]app.TicketView{
		10: {
			Ticket: contracts.Ticket{ID: 10, WorkflowStatus: contracts.TicketActive},
		},
	}
	v := m.viewsByID[10]
	v.Capability.CanQueueRun = true
	v.Capability.CanDispatch = true
	m.viewsByID[10] = v

	gotModel, cmd := m.updateTable(keyRune('r'))
	if cmd == nil {
		t.Fatalf("allowed worker run should return cmd")
	}
	got := gotModel.(model)
	if !strings.Contains(got.status, "重新跑中 t10") {
		t.Fatalf("unexpected status: %q", got.status)
	}
	if got.refreshManual {
		t.Fatalf("worker run should not mark manual refresh")
	}

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("worker run cmd should not panic, got=%v", r)
		}
	}()
	msg := cmd()
	if _, ok := msg.(workerRunMsg); !ok {
		t.Fatalf("expected workerRunMsg, got=%T", msg)
	}
}

func TestUpdateEvents_RKeepsRefreshSemantics(t *testing.T) {
	m := newModel(nil, nil, "")
	m.eventsTicketID = 9

	gotModel, cmd := m.updateEvents(keyRune('r'))
	if cmd == nil {
		t.Fatalf("events r should return load events cmd")
	}
	got := gotModel.(model)
	if !got.eventsInFlight {
		t.Fatalf("events r should mark loading in flight")
	}
	if !strings.Contains(got.status, "加载事件 t9") {
		t.Fatalf("unexpected events status: %q", got.status)
	}
}

func TestPlanBacklogReorder_MoveUp(t *testing.T) {
	m := newModel(nil, nil, "")
	m.applyViews([]app.TicketView{
		{Ticket: contracts.Ticket{ID: 1, WorkflowStatus: contracts.TicketBacklog, Priority: 3}},
		{Ticket: contracts.Ticket{ID: 2, WorkflowStatus: contracts.TicketBacklog, Priority: 2}},
	})
	m.table.SetCursor(2)

	plan, ok, denied := m.planBacklogReorder(-1)
	if !ok {
		t.Fatalf("expected reorder plan, denied=%q", denied)
	}
	if plan.ticketID != 2 || plan.targetTicketID != 1 {
		t.Fatalf("unexpected pair: %+v", plan)
	}
	if plan.ticketPriority != 3 || plan.targetPriority != 2 {
		t.Fatalf("unexpected priority swap: %+v", plan)
	}
}

func TestPlanBacklogReorder_EqualPriorityMoveDown(t *testing.T) {
	m := newModel(nil, nil, "")
	m.applyViews([]app.TicketView{
		{Ticket: contracts.Ticket{ID: 1, WorkflowStatus: contracts.TicketBacklog, Priority: 2}},
		{Ticket: contracts.Ticket{ID: 2, WorkflowStatus: contracts.TicketBacklog, Priority: 2}},
	})
	m.table.SetCursor(1)

	plan, ok, denied := m.planBacklogReorder(+1)
	if !ok {
		t.Fatalf("expected reorder plan, denied=%q", denied)
	}
	if plan.ticketPriority != 1 || plan.targetPriority != 2 {
		t.Fatalf("unexpected equal-priority move plan: %+v", plan)
	}
}

func TestUpdateTable_BacklogReorderBoundary(t *testing.T) {
	m := newModel(nil, nil, "")
	m.applyViews([]app.TicketView{
		{Ticket: contracts.Ticket{ID: 1, WorkflowStatus: contracts.TicketBacklog, Priority: 2}},
	})
	m.table.SetCursor(1)

	gotModel, cmd := m.updateTable(keyRune('K'))
	if cmd != nil {
		t.Fatalf("top boundary should not return cmd")
	}
	got := gotModel.(model)
	if !strings.Contains(got.status, "顶部") {
		t.Fatalf("unexpected status: %q", got.status)
	}
}

func TestUpdateTable_BacklogReorderAllowed(t *testing.T) {
	m := newModel(nil, nil, "")
	m.applyViews([]app.TicketView{
		{Ticket: contracts.Ticket{ID: 1, WorkflowStatus: contracts.TicketBacklog, Priority: 3}},
		{Ticket: contracts.Ticket{ID: 2, WorkflowStatus: contracts.TicketBacklog, Priority: 2}},
	})
	m.table.SetCursor(2)

	gotModel, cmd := m.updateTable(keyRune('K'))
	if cmd == nil {
		t.Fatalf("allowed backlog reorder should return cmd")
	}
	got := gotModel.(model)
	if !strings.Contains(got.status, "上移中 t2") {
		t.Fatalf("unexpected status: %q", got.status)
	}
}

func TestUpdateTable_EnterOpensTmuxForTicketRow(t *testing.T) {
	m := newModel(nil, nil, "")
	wt := t.TempDir()
	m.rowRefs = []rowRef{{kind: rowTicket, section: "running", ticketID: 10}}
	m.viewsByID = map[uint]app.TicketView{
		10: {
			Ticket: contracts.Ticket{ID: 10, WorkflowStatus: contracts.TicketActive},
			LatestWorker: &contracts.Worker{
				ID:           101,
				WorktreePath: wt,
			},
		},
	}

	gotModel, cmd := m.updateTable(keyEnter())
	if cmd == nil {
		t.Fatalf("enter on ticket row should return cmd")
	}
	got := gotModel.(model)
	if !strings.Contains(got.status, "打开 tmux t10") {
		t.Fatalf("unexpected status: %q", got.status)
	}
}

func TestUpdateTable_EnterRequiresExistingWorktree(t *testing.T) {
	m := newModel(nil, nil, "")
	m.rowRefs = []rowRef{{kind: rowTicket, section: "running", ticketID: 10}}
	m.viewsByID = map[uint]app.TicketView{
		10: {
			Ticket: contracts.Ticket{ID: 10, WorkflowStatus: contracts.TicketActive},
		},
	}

	gotModel, cmd := m.updateTable(keyEnter())
	if cmd != nil {
		t.Fatalf("enter without ready worktree should not return cmd")
	}
	got := gotModel.(model)
	if !strings.Contains(got.status, "尚未 start") {
		t.Fatalf("unexpected status: %q", got.status)
	}
}

func TestUpdateTable_EnterOnManagerRowNoop(t *testing.T) {
	m := newModel(nil, nil, "")
	m.rowRefs = []rowRef{{kind: rowManager, section: "manager"}}

	gotModel, cmd := m.updateTable(keyEnter())
	if cmd != nil {
		t.Fatalf("enter on manager row should not return cmd")
	}
	got := gotModel.(model)
	if !strings.Contains(got.status, "Enter 仅支持 ticket 行") {
		t.Fatalf("unexpected status: %q", got.status)
	}
}

func TestUpdateTable_EPrefillsEditLabel(t *testing.T) {
	m := newModel(nil, nil, "")
	m.rowRefs = []rowRef{{kind: rowTicket, section: "backlog", ticketID: 10}}
	m.viewsByID = map[uint]app.TicketView{
		10: {
			Ticket: contracts.Ticket{
				ID:             10,
				WorkflowStatus: contracts.TicketBacklog,
				Title:          "needs label",
				Description:    "desc",
				Label:          "ops",
			},
		},
	}

	gotModel, cmd := m.updateTable(keyRune('e'))
	if cmd == nil {
		t.Fatalf("e should return focus cmd")
	}
	got := gotModel.(model)
	if got.mode != modeEditTicket {
		t.Fatalf("mode=%v, want=%v", got.mode, modeEditTicket)
	}
	if got.editLabel.Value() != "ops" {
		t.Fatalf("edit label should be prefilled, got=%q", got.editLabel.Value())
	}
}

func TestUpdateTable_AOpensWorkerLogPage(t *testing.T) {
	m := newModel(nil, nil, "")
	m.rowRefs = []rowRef{{kind: rowTicket, section: "running", ticketID: 10}}
	m.viewsByID = map[uint]app.TicketView{
		10: {
			Ticket: contracts.Ticket{ID: 10, WorkflowStatus: contracts.TicketActive},
			LatestWorker: &contracts.Worker{
				ID:      11,
				LogPath: "/tmp/t10.log",
			},
		},
	}
	v := m.viewsByID[10]
	v.Capability.CanAttach = true
	m.viewsByID[10] = v

	gotModel, cmd := m.updateTable(keyRune('a'))
	if cmd == nil {
		t.Fatalf("a on attachable ticket should return cmd")
	}
	got := gotModel.(model)
	if got.mode != modeWorkerLog {
		t.Fatalf("mode=%v, want=%v", got.mode, modeWorkerLog)
	}
	if !strings.Contains(got.status, "加载日志 t10") {
		t.Fatalf("unexpected status: %q", got.status)
	}
	if !got.workerLogInFlight {
		t.Fatalf("worker log should be in flight")
	}
}
