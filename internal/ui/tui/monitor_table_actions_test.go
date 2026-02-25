package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"dalek/internal/app"
	"dalek/internal/store"
)

func keyRune(r rune) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}}
}

func TestUpdateTable_DeniedActionShowsBoundaryMessage(t *testing.T) {
	m := newModel(nil, nil, "")
	m.rowRefs = []rowRef{{kind: rowTicket, section: "done", ticketID: 10}}
	m.viewsByID = map[uint]app.TicketView{
		10: {
			Ticket: store.Ticket{ID: 10, WorkflowStatus: store.TicketDone},
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
			Ticket: store.Ticket{ID: 10, WorkflowStatus: store.TicketActive},
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
			Ticket: store.Ticket{ID: 10, WorkflowStatus: store.TicketBacklog},
		},
	}
	v := m.viewsByID[10]
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
			Ticket: store.Ticket{ID: 10, WorkflowStatus: store.TicketBacklog},
		},
	}
	v := m.viewsByID[10]
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
