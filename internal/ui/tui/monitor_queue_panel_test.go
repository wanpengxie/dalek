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
	m.mergeItems = []contracts.MergeItem{{ID: 7, TicketID: 3, Status: contracts.MergeProposed, Branch: "ts/demo/t3-abc"}}
	m.archivedTickets = []contracts.Ticket{{ID: 99, Title: "已归档", WorkflowStatus: contracts.TicketArchived}}
	m.applyViews([]app.TicketView{
		{Ticket: contracts.Ticket{ID: 1, Title: "run"}, DerivedStatus: contracts.TicketActive, RuntimeHealthState: contracts.TaskHealthBusy},
		{Ticket: contracts.Ticket{ID: 2, Title: "wait"}, DerivedStatus: contracts.TicketBlocked, RuntimeNeedsUser: true},
		{Ticket: contracts.Ticket{ID: 3, Title: "backlog"}, DerivedStatus: contracts.TicketBacklog},
		{Ticket: contracts.Ticket{ID: 4, Title: "done"}, DerivedStatus: contracts.TicketDone},
	})

	if len(m.rowRefs) < 6 {
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
	wantPrefix := []string{"running", "wait", "merge", "backlog", "done"}
	for i, want := range wantPrefix {
		if i >= len(gotOrder) || gotOrder[i] != want {
			t.Fatalf("unexpected section order at %d: got=%v wantPrefix=%v", i, gotOrder, wantPrefix)
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
	m.mergeItems = []contracts.MergeItem{
		{ID: 21, TicketID: 10, Status: contracts.MergeProposed, Branch: "ts/demo/t10-x1"},
		{ID: 22, TicketID: 11, Status: contracts.MergeApproved, Branch: "ts/demo/t11-x2"},
	}
	m.applyViews([]app.TicketView{
		{
			Ticket:             contracts.Ticket{ID: 10, Title: "等待输入"},
			DerivedStatus:      contracts.TicketActive,
			RuntimeHealthState: contracts.TaskHealthBusy,
			RuntimeNeedsUser:   true,
			RuntimeSummary:     "需要你确认输入",
		},
		{
			Ticket:        contracts.Ticket{ID: 11, Title: "卡住"},
			DerivedStatus: contracts.TicketBlocked,
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
	if !strings.Contains(got, "merge:") {
		t.Fatalf("merge section not shown: %q", got)
	}
	if !strings.Contains(got, "t10 等待输入：需要你确认输入") {
		t.Fatalf("pending preview not shown: %q", got)
	}
	if !strings.Contains(got, "m21 t10 proposed") {
		t.Fatalf("merge preview not shown: %q", got)
	}
}

func TestSelectedRow_ReturnsManagerAndMergeKinds(t *testing.T) {
	m := newModel(nil, nil, "")
	m.mergeItems = []contracts.MergeItem{{ID: 31, TicketID: 10, Status: contracts.MergeProposed, Branch: "ts/demo/t10-x1"}}
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

	foundMerge := false
	for i, r := range m.rowRefs {
		if r.kind != rowMerge {
			continue
		}
		m.table.SetCursor(i)
		sel := m.selectedRow()
		if sel.kind != rowMerge || sel.mergeID != 31 {
			t.Fatalf("unexpected merge row: %+v", sel)
		}
		foundMerge = true
		break
	}
	if !foundMerge {
		t.Fatalf("merge row not found")
	}
}

func TestApplyViews_MergeSectionHidesDiscardedAndMerged(t *testing.T) {
	m := newModel(nil, nil, "")
	m.mergeItems = []contracts.MergeItem{
		{ID: 31, TicketID: 10, Status: contracts.MergeProposed, Branch: "ts/demo/t10-x1"},
		{ID: 32, TicketID: 11, Status: contracts.MergeDiscarded, Branch: "ts/demo/t11-x2"},
		{ID: 33, TicketID: 12, Status: contracts.MergeMerged, Branch: "ts/demo/t12-x3"},
	}
	m.applyViews([]app.TicketView{
		{Ticket: contracts.Ticket{ID: 10, Title: "run"}, DerivedStatus: contracts.TicketActive, RuntimeHealthState: contracts.TaskHealthBusy},
	})

	for _, r := range m.rowRefs {
		if r.kind != rowMerge {
			continue
		}
		if r.mergeID != 31 {
			t.Fatalf("only active merge item should remain visible, got mergeID=%d", r.mergeID)
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
