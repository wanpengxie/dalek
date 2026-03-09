package tui

import (
	"dalek/internal/contracts"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"dalek/internal/app"
)

func TestApplyViews_GroupOrderAndNoHeaderRows(t *testing.T) {
	m := newModel(nil, nil, "")
	m.archivedTickets = []contracts.Ticket{{ID: 99, Title: "已归档", WorkflowStatus: contracts.TicketArchived}}
	m.applyViews([]app.TicketView{
		{Ticket: contracts.Ticket{ID: 1, Title: "run"}, DerivedStatus: contracts.TicketActive, RuntimeHealthState: contracts.TaskHealthBusy},
		{Ticket: contracts.Ticket{ID: 2, Title: "wait"}, DerivedStatus: contracts.TicketBlocked, RuntimeNeedsUser: true},
		{Ticket: contracts.Ticket{ID: 3, Title: "backlog"}, DerivedStatus: contracts.TicketBacklog},
		{Ticket: contracts.Ticket{ID: 4, Title: "done"}, DerivedStatus: contracts.TicketDone},
	})

	if len(m.rowRefs) < 5 {
		t.Fatalf("unexpected row count: %d", len(m.rowRefs))
	}
	if m.rowRefs[0].kind != rowManager || m.rowRefs[0].section != "manager" {
		t.Fatalf("row0 should be manager: %+v", m.rowRefs[0])
	}
	gotOrder := []string{}
	for i, r := range m.rowRefs {
		if i == 0 {
			continue
		}
		gotOrder = append(gotOrder, r.section)
	}
	wantPrefix := []string{"running", "wait", "backlog", "done"}
	for i, want := range wantPrefix {
		if i >= len(gotOrder) || gotOrder[i] != want {
			t.Fatalf("unexpected section order at %d: got=%v wantPrefix=%v", i, gotOrder, wantPrefix)
		}
	}
	for _, s := range gotOrder {
		if s == "merge" {
			t.Fatalf("merge section should be removed: gotOrder=%v", gotOrder)
		}
	}
	for _, s := range gotOrder {
		if s == "archive" {
			t.Fatalf("archive should be hidden by default: gotOrder=%v", gotOrder)
		}
	}

	gotView := m.tablePanelView(140)
	if strings.Contains(gotView, "── manager") || strings.Contains(gotView, "── backlog") {
		t.Fatalf("should not render fake separator rows anymore: %q", gotView)
	}
}

func TestManagerInspectorLeftView_ShowsPendingIssuePreview(t *testing.T) {
	m := newModel(nil, nil, "")
	m.applyViews([]app.TicketView{
		{
			Ticket:             contracts.Ticket{ID: 10, Title: "等待输入"},
			DerivedStatus:      contracts.TicketActive,
			RuntimeHealthState: contracts.TaskHealthBusy,
			RuntimeNeedsUser:   true,
			RuntimeSummary:     "需要你确认输入",
		},
		{
			Ticket:        contracts.Ticket{ID: 11, Title: "待合并", WorkflowStatus: contracts.TicketDone, IntegrationStatus: contracts.IntegrationNeedsMerge},
			DerivedStatus: contracts.TicketDone,
		},
		{
			Ticket:        contracts.Ticket{ID: 12, Title: "已合并", WorkflowStatus: contracts.TicketDone, IntegrationStatus: contracts.IntegrationMerged},
			DerivedStatus: contracts.TicketDone,
		},
	})
	for i, r := range m.rowRefs {
		if r.kind == rowManager {
			m.table.SetCursor(i)
			break
		}
	}

	got := m.managerInspectorLeftView(140)
	if !strings.Contains(got, "待处理:") {
		t.Fatalf("pending section not shown: %q", got)
	}
	if !strings.Contains(got, "integration:") {
		t.Fatalf("integration section not shown: %q", got)
	}
	if !strings.Contains(got, "t10 等待输入：需要你确认输入") {
		t.Fatalf("pending preview not shown: %q", got)
	}
	if strings.Contains(got, "merge:") {
		t.Fatalf("merge summary should be removed: %q", got)
	}
}

func TestSelectedRow_ReturnsManagerAndTicketKinds(t *testing.T) {
	m := newModel(nil, nil, "")
	m.applyViews([]app.TicketView{{
		Ticket:             contracts.Ticket{ID: 10, Title: "等待输入"},
		DerivedStatus:      contracts.TicketActive,
		RuntimeHealthState: contracts.TaskHealthBusy,
		RuntimeNeedsUser:   true,
		RuntimeSummary:     "需要你确认输入",
	}})

	m.table.SetCursor(0)
	mgr := m.selectedRow()
	if mgr.kind != rowManager || mgr.section != "manager" {
		t.Fatalf("unexpected manager row: %+v", mgr)
	}

	foundTicket := false
	for i, r := range m.rowRefs {
		if r.kind != rowTicket {
			continue
		}
		m.table.SetCursor(i)
		sel := m.selectedRow()
		if sel.kind != rowTicket || sel.ticketID != 10 {
			t.Fatalf("unexpected ticket row: %+v", sel)
		}
		foundTicket = true
		break
	}
	if !foundTicket {
		t.Fatalf("ticket row not found")
	}
}

func TestApplyViews_DoneRowsRenderIntegrationStatus(t *testing.T) {
	m := newModel(nil, nil, "")
	m.applyViews([]app.TicketView{
		{Ticket: contracts.Ticket{ID: 10, Title: "need merge", WorkflowStatus: contracts.TicketDone, IntegrationStatus: contracts.IntegrationNeedsMerge}, DerivedStatus: contracts.TicketDone},
		{Ticket: contracts.Ticket{ID: 11, Title: "merged", WorkflowStatus: contracts.TicketDone, IntegrationStatus: contracts.IntegrationMerged}, DerivedStatus: contracts.TicketDone},
		{Ticket: contracts.Ticket{ID: 12, Title: "abandoned", WorkflowStatus: contracts.TicketDone, IntegrationStatus: contracts.IntegrationAbandoned}, DerivedStatus: contracts.TicketDone},
	})

	got := ansi.Strip(m.tablePanelView(180))
	if !strings.Contains(got, "集成") {
		t.Fatalf("integration column should be visible, got=%q", got)
	}
	for _, want := range []string{"待合并", "已合并", "已放弃"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing integration status %q in table: %q", want, got)
		}
	}
}

func TestColorizePartitionLine_PreservesLayoutWidth(t *testing.T) {
	raw := "manager mgr    - 状态     运行       标题                                   输出"
	colored := colorizePartitionLine(raw)
	if ansi.Strip(colored) != raw {
		t.Fatalf("strip after colorize should keep original text, got=%q", ansi.Strip(colored))
	}
	if ansi.StringWidth(colored) != ansi.StringWidth(raw) {
		t.Fatalf("colorize should preserve display width, raw=%d colored=%d", ansi.StringWidth(raw), ansi.StringWidth(colored))
	}
}

func TestApplyViews_RendersLabelColumnAndPlaceholder(t *testing.T) {
	m := newModel(nil, nil, "")
	m.applyViews([]app.TicketView{
		{Ticket: contracts.Ticket{ID: 1, Title: "has label", Label: "backend"}, DerivedStatus: contracts.TicketBacklog},
		{Ticket: contracts.Ticket{ID: 2, Title: "no label", Label: ""}, DerivedStatus: contracts.TicketBacklog},
	})

	view := ansi.Strip(m.tablePanelView(160))
	if !strings.Contains(view, "标签") {
		t.Fatalf("table should render label column header, got=%q", view)
	}
	if !strings.Contains(view, "backend") {
		t.Fatalf("table should render non-empty label value, got=%q", view)
	}
	if !strings.Contains(view, "no label") || !strings.Contains(view, "  -  ") {
		t.Fatalf("table should render dash placeholder for empty label, got=%q", view)
	}
}
