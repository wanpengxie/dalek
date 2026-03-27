package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"dalek/internal/contracts"
)

// ---------- Layer 1: Manager 行测试 ----------

func TestManagerRowCells_NoFocus(t *testing.T) {
	m := newModel(nil, nil, "")
	status, runtime, title := m.managerRowCells()
	if status != "manager" || runtime != "就绪" || title != "项目管理员" {
		t.Fatalf("no focus: want manager/就绪/项目管理员, got %s/%s/%s", status, runtime, title)
	}
}

func TestManagerRowCells_Batch_Running(t *testing.T) {
	m := newModel(nil, nil, "")
	m.focusView = &contracts.FocusRunView{
		Run: contracts.FocusRun{
			Mode:           contracts.FocusModeBatch,
			Status:         contracts.FocusRunning,
			AgentBudget:    5,
			AgentBudgetMax: 20,
		},
		Items: []contracts.FocusRunItem{
			{Seq: 1, Status: contracts.FocusItemCompleted},
			{Seq: 2, Status: contracts.FocusItemExecuting},
			{Seq: 3, Status: contracts.FocusItemPending},
		},
	}
	m.focusView.Run.ID = 4

	status, runtime, title := m.managerRowCells()
	if status != "batch▶" {
		t.Errorf("batch running status: want batch▶, got %s", status)
	}
	if runtime != "1/3 items" {
		t.Errorf("batch running runtime: want 1/3 items, got %s", runtime)
	}
	if !strings.Contains(title, "focus#4") || !strings.Contains(title, "budget 5/20") {
		t.Errorf("batch running title: want focus#4 budget 5/20, got %s", title)
	}
}

func TestManagerRowCells_Batch_Completed(t *testing.T) {
	m := newModel(nil, nil, "")
	m.focusView = &contracts.FocusRunView{
		Run: contracts.FocusRun{
			Mode:           contracts.FocusModeBatch,
			Status:         contracts.FocusCompleted,
			AgentBudget:    18,
			AgentBudgetMax: 20,
		},
		Items: []contracts.FocusRunItem{
			{Seq: 1, Status: contracts.FocusItemCompleted},
			{Seq: 2, Status: contracts.FocusItemCompleted},
		},
	}
	m.focusView.Run.ID = 5

	status, _, _ := m.managerRowCells()
	if status != "batch✓" {
		t.Errorf("batch completed status: want batch✓, got %s", status)
	}
}

func TestManagerRowCells_Convergent_Batch_Phase(t *testing.T) {
	m := newModel(nil, nil, "")
	m.focusView = &contracts.FocusRunView{
		Run: contracts.FocusRun{
			Mode:            contracts.FocusModeConvergent,
			Status:          contracts.FocusRunning,
			ConvergentPhase: "batch",
			PMRunCount:      0,
			MaxPMRuns:       3,
			AgentBudget:     12,
			AgentBudgetMax:  20,
		},
		Items: []contracts.FocusRunItem{
			{Seq: 1, Status: contracts.FocusItemCompleted},
			{Seq: 2, Status: contracts.FocusItemExecuting},
			{Seq: 3, Status: contracts.FocusItemPending},
		},
		LatestRound: &contracts.ConvergentRound{RoundNumber: 1},
	}
	m.focusView.Run.ID = 4

	status, runtime, _ := m.managerRowCells()
	if status != "conv·bat" {
		t.Errorf("conv batch status: want conv·bat, got %s", status)
	}
	if !strings.Contains(runtime, "r1/3") {
		t.Errorf("conv batch runtime: want r1/3, got %s", runtime)
	}
}

func TestManagerRowCells_Convergent_PM_Phase(t *testing.T) {
	m := newModel(nil, nil, "")
	m.focusView = &contracts.FocusRunView{
		Run: contracts.FocusRun{
			Mode:            contracts.FocusModeConvergent,
			Status:          contracts.FocusRunning,
			ConvergentPhase: "pm_run",
			PMRunCount:      0,
			MaxPMRuns:       3,
			AgentBudget:     12,
			AgentBudgetMax:  20,
		},
		Items:       []contracts.FocusRunItem{},
		LatestRound: &contracts.ConvergentRound{RoundNumber: 1},
	}
	m.focusView.Run.ID = 4

	status, runtime, _ := m.managerRowCells()
	if status != "conv·pm" {
		t.Errorf("conv pm status: want conv·pm, got %s", status)
	}
	if !strings.Contains(runtime, "审查") {
		t.Errorf("conv pm runtime: want 审查, got %s", runtime)
	}
}

func TestManagerRowCells_Convergent_Converged(t *testing.T) {
	m := newModel(nil, nil, "")
	m.focusView = &contracts.FocusRunView{
		Run: contracts.FocusRun{
			Mode:       contracts.FocusModeConvergent,
			Status:     contracts.FocusConverged,
			PMRunCount: 2,
			MaxPMRuns:  3,
		},
		LatestRound: &contracts.ConvergentRound{RoundNumber: 2},
	}
	m.focusView.Run.ID = 4

	status, _, title := m.managerRowCells()
	if status != "✓conv" {
		t.Errorf("converged status: want ✓conv, got %s", status)
	}
	if !strings.Contains(title, "已收敛") {
		t.Errorf("converged title should contain 已收敛, got %s", title)
	}
}

func TestManagerRowCells_Convergent_Exhausted(t *testing.T) {
	m := newModel(nil, nil, "")
	m.focusView = &contracts.FocusRunView{
		Run: contracts.FocusRun{
			Mode:       contracts.FocusModeConvergent,
			Status:     contracts.FocusExhausted,
			PMRunCount: 3,
			MaxPMRuns:  3,
		},
		LatestRound: &contracts.ConvergentRound{RoundNumber: 3},
	}
	m.focusView.Run.ID = 4

	status, _, _ := m.managerRowCells()
	if status != "⚠exhaust" {
		t.Errorf("exhausted status: want ⚠exhaust, got %s", status)
	}
}

// ---------- Layer 2: Inspector 测试 ----------

func TestFocusInspectorLeftView_BatchMode(t *testing.T) {
	m := newModel(nil, nil, "")
	m.focusView = &contracts.FocusRunView{
		Run: contracts.FocusRun{
			Mode:           contracts.FocusModeBatch,
			Status:         contracts.FocusRunning,
			AgentBudget:    5,
			AgentBudgetMax: 20,
		},
		Items: []contracts.FocusRunItem{
			{Seq: 1, TicketID: 42, Status: contracts.FocusItemCompleted},
			{Seq: 2, TicketID: 43, Status: contracts.FocusItemExecuting},
			{Seq: 3, TicketID: 44, Status: contracts.FocusItemPending},
		},
	}
	m.focusView.Run.ID = 4

	got := ansi.Strip(m.focusInspectorLeftView(80))
	if !strings.Contains(got, "focus#4") {
		t.Errorf("left view should contain focus#4, got:\n%s", got)
	}
	if !strings.Contains(got, "t#42") {
		t.Errorf("left view should contain t#42, got:\n%s", got)
	}
	if !strings.Contains(got, "progress") {
		t.Errorf("left view should contain progress, got:\n%s", got)
	}
}

func TestFocusInspectorMiddleView_Convergent(t *testing.T) {
	m := newModel(nil, nil, "")
	m.focusView = &contracts.FocusRunView{
		Run: contracts.FocusRun{
			Mode:   contracts.FocusModeConvergent,
			Status: contracts.FocusRunning,
		},
		Rounds: []contracts.ConvergentRound{
			{
				RoundNumber: 1,
				BatchStatus: "completed",
				PMRunStatus: "done",
				Verdict:     "needs_fix",
				FixTicketIDs: `[66, 67]`,
			},
			{
				RoundNumber: 2,
				BatchStatus: "running",
				PMRunStatus: "pending",
			},
		},
	}
	m.focusView.Run.ID = 4

	got := ansi.Strip(m.focusInspectorMiddleView(80))
	if !strings.Contains(got, "round 1") {
		t.Errorf("middle view should contain round 1, got:\n%s", got)
	}
	if !strings.Contains(got, "round 2") {
		t.Errorf("middle view should contain round 2, got:\n%s", got)
	}
	if !strings.Contains(got, "needs_fix") {
		t.Errorf("middle view should contain needs_fix verdict, got:\n%s", got)
	}
	if !strings.Contains(got, "t#66") {
		t.Errorf("middle view should contain fix ticket t#66, got:\n%s", got)
	}
}

func TestFocusInspectorMiddleView_Batch(t *testing.T) {
	now := time.Now()
	started := now.Add(-2 * time.Minute)
	finished := now.Add(-30 * time.Second)
	m := newModel(nil, nil, "")
	m.focusView = &contracts.FocusRunView{
		Run: contracts.FocusRun{
			Mode:   contracts.FocusModeBatch,
			Status: contracts.FocusRunning,
		},
		Items: []contracts.FocusRunItem{
			{Seq: 1, TicketID: 42, Status: contracts.FocusItemCompleted, StartedAt: &started, FinishedAt: &finished},
			{Seq: 2, TicketID: 43, Status: contracts.FocusItemExecuting},
		},
		ActiveItem: &contracts.FocusRunItem{Seq: 2, TicketID: 43, Status: contracts.FocusItemExecuting},
	}
	m.focusView.Run.ID = 4

	got := ansi.Strip(m.focusInspectorMiddleView(80))
	if !strings.Contains(got, "当前执行") {
		t.Errorf("batch middle view should contain 当前执行, got:\n%s", got)
	}
	if !strings.Contains(got, "t#43") {
		t.Errorf("batch middle view should contain active item t#43, got:\n%s", got)
	}
	if !strings.Contains(got, "已完成") {
		t.Errorf("batch middle view should contain 已完成, got:\n%s", got)
	}
}

func TestFocusInspectorRightView_Events(t *testing.T) {
	m := newModel(nil, nil, "")
	m.focusView = &contracts.FocusRunView{
		Run: contracts.FocusRun{
			Mode:   contracts.FocusModeBatch,
			Status: contracts.FocusRunning,
		},
	}
	m.focusView.Run.ID = 4
	m.focusEvents = []contracts.FocusEvent{
		{ID: 1, Kind: "item.selected", Summary: "t#42"},
		{ID: 2, Kind: "item.adopted", Summary: "t#42 w16"},
		{ID: 3, Kind: "merge.started", Summary: "t#42"},
	}

	got := ansi.Strip(m.focusInspectorRightView(80))
	if !strings.Contains(got, "focus 事件流") {
		t.Errorf("right view should contain title, got:\n%s", got)
	}
	if !strings.Contains(got, "item.selected") {
		t.Errorf("right view should contain event kind, got:\n%s", got)
	}
	if !strings.Contains(got, "#3") {
		t.Errorf("right view should contain event ID #3, got:\n%s", got)
	}
}

func TestFocusInspectorRightView_NoEvents(t *testing.T) {
	m := newModel(nil, nil, "")
	m.focusView = &contracts.FocusRunView{
		Run: contracts.FocusRun{
			Mode:   contracts.FocusModeBatch,
			Status: contracts.FocusRunning,
		},
	}
	m.focusView.Run.ID = 4

	got := ansi.Strip(m.focusInspectorRightView(80))
	if !strings.Contains(got, "暂无事件") {
		t.Errorf("right view should show no events hint, got:\n%s", got)
	}
}

// ---------- Layer 3: Ticket 行标注测试 ----------

func TestFocusItemRuntimeOverlay_Merging(t *testing.T) {
	m := newModel(nil, nil, "")
	m.ticketFocusItemByID = map[uint]*contracts.FocusRunItem{
		42: {TicketID: 42, Status: contracts.FocusItemMerging},
		43: {TicketID: 43, Status: contracts.FocusItemAwaitingMergeObservation},
		44: {TicketID: 44, Status: contracts.FocusItemCompleted},
	}

	overlay, ok := m.focusItemRuntimeOverlay(42)
	if !ok || overlay != "合并中▶" {
		t.Errorf("merging overlay: want 合并中▶, got %s (ok=%v)", overlay, ok)
	}

	overlay, ok = m.focusItemRuntimeOverlay(43)
	if !ok || overlay != "待观测…" {
		t.Errorf("awaiting overlay: want 待观测…, got %s (ok=%v)", overlay, ok)
	}

	_, ok = m.focusItemRuntimeOverlay(44)
	if ok {
		t.Errorf("completed should not have overlay")
	}

	_, ok = m.focusItemRuntimeOverlay(99)
	if ok {
		t.Errorf("unknown ticket should not have overlay")
	}
}

func TestFocusLabelPrefix(t *testing.T) {
	m := newModel(nil, nil, "")
	m.ticketFocusItemByID = map[uint]*contracts.FocusRunItem{
		42: {TicketID: 42, Status: contracts.FocusItemExecuting},
	}

	if prefix := m.focusLabelPrefix(42); prefix != "◈" {
		t.Errorf("focus ticket should have ◈ prefix, got %q", prefix)
	}
	if prefix := m.focusLabelPrefix(99); prefix != "" {
		t.Errorf("non-focus ticket should have empty prefix, got %q", prefix)
	}
}

// ---------- 辅助函数测试 ----------

func TestCountItemsByStatus(t *testing.T) {
	items := []contracts.FocusRunItem{
		{Status: contracts.FocusItemCompleted},
		{Status: contracts.FocusItemCompleted},
		{Status: contracts.FocusItemExecuting},
		{Status: contracts.FocusItemPending},
	}
	if n := countItemsByStatus(items, contracts.FocusItemCompleted); n != 2 {
		t.Errorf("countItemsByStatus completed: want 2, got %d", n)
	}
}

func TestProgressBar(t *testing.T) {
	bar := progressBar(5, 10, 10)
	if bar != "█████░░░░░" {
		t.Errorf("progressBar 5/10: want █████░░░░░, got %s", bar)
	}
	bar = progressBar(0, 0, 5)
	if bar != "░░░░░" {
		t.Errorf("progressBar 0/0: want ░░░░░, got %s", bar)
	}
}

func TestFocusItemSymbol(t *testing.T) {
	tests := map[string]string{
		contracts.FocusItemCompleted:                "✓",
		contracts.FocusItemExecuting:                "▶",
		contracts.FocusItemMerging:                  "⇄",
		contracts.FocusItemAwaitingMergeObservation: "◎",
		contracts.FocusItemBlocked:                  "!",
		contracts.FocusItemPending:                  "·",
		contracts.FocusItemQueued:                   "·",
		contracts.FocusItemStopped:                  "✗",
		contracts.FocusItemFailed:                   "✗",
		contracts.FocusItemCanceled:                 "✗",
	}
	for status, want := range tests {
		got := focusItemSymbol(status)
		if got != want {
			t.Errorf("focusItemSymbol(%s): want %s, got %s", status, want, got)
		}
	}
}

func TestParseTicketIDs(t *testing.T) {
	ids := parseTicketIDs(`[1, 2, 3]`)
	if len(ids) != 3 || ids[0] != 1 || ids[1] != 2 || ids[2] != 3 {
		t.Errorf("parseTicketIDs: want [1 2 3], got %v", ids)
	}

	ids = parseTicketIDs(`[]`)
	if len(ids) != 0 {
		t.Errorf("parseTicketIDs empty: want [], got %v", ids)
	}

	ids = parseTicketIDs(``)
	if ids != nil {
		t.Errorf("parseTicketIDs blank: want nil, got %v", ids)
	}
}

func TestApplyFocusRefresh_NilView(t *testing.T) {
	m := newModel(nil, nil, "")
	m.focusView = &contracts.FocusRunView{Run: contracts.FocusRun{Status: contracts.FocusRunning}}
	m.focusView.Run.ID = 1
	m.focusEvents = []contracts.FocusEvent{{ID: 1}}

	m.applyFocusRefresh(focusRefreshedMsg{View: nil})
	if m.focusView != nil {
		t.Error("applyFocusRefresh(nil) should clear focusView")
	}
	if m.focusEvents != nil {
		t.Error("applyFocusRefresh(nil) should clear focusEvents")
	}
}

func TestApplyFocusRefresh_EventRingBuffer(t *testing.T) {
	m := newModel(nil, nil, "")

	// 填充超过 buffer size 的事件
	events := make([]contracts.FocusEvent, focusEventBufferSize+10)
	for i := range events {
		events[i] = contracts.FocusEvent{ID: uint(i + 1), Kind: "test"}
	}
	view := &contracts.FocusRunView{
		Run: contracts.FocusRun{
			Mode:   contracts.FocusModeBatch,
			Status: contracts.FocusRunning,
		},
	}
	view.Run.ID = 1

	m.applyFocusRefresh(focusRefreshedMsg{View: view, NewEvents: events})
	if len(m.focusEvents) != focusEventBufferSize {
		t.Errorf("ring buffer should cap at %d, got %d", focusEventBufferSize, len(m.focusEvents))
	}
	// 最新事件应在末尾
	if m.focusEvents[len(m.focusEvents)-1].ID != uint(focusEventBufferSize+10) {
		t.Errorf("latest event should be at tail")
	}
}

// ---------- Inspector 路由测试 ----------

func TestManagerInspectorRouting_WithFocus(t *testing.T) {
	m := newModel(nil, nil, "")
	m.focusView = &contracts.FocusRunView{
		Run: contracts.FocusRun{
			Mode:           contracts.FocusModeBatch,
			Status:         contracts.FocusRunning,
			AgentBudget:    5,
			AgentBudgetMax: 20,
		},
		Items: []contracts.FocusRunItem{
			{Seq: 1, TicketID: 42, Status: contracts.FocusItemCompleted},
		},
	}
	m.focusView.Run.ID = 4

	// 选中 manager 行
	m.applyViews(nil)
	m.table.SetCursor(0)

	leftView := ansi.Strip(m.managerInspectorLeftView(80))
	if !strings.Contains(leftView, "focus#4") {
		t.Errorf("left view with focus should show focus info, got:\n%s", leftView)
	}

	rightView := ansi.Strip(m.managerInspectorRightView(80))
	if !strings.Contains(rightView, "focus 事件流") {
		t.Errorf("right view with focus should show event stream, got:\n%s", rightView)
	}
}

func TestManagerInspectorRouting_NoFocus(t *testing.T) {
	m := newModel(nil, nil, "")
	m.focusView = nil

	leftView := ansi.Strip(m.managerInspectorLeftView(80))
	if !strings.Contains(leftView, "manager") {
		t.Errorf("left view without focus should show manager info, got:\n%s", leftView)
	}

	rightView := ansi.Strip(m.managerInspectorRightView(80))
	if !strings.Contains(rightView, "已移除") {
		t.Errorf("right view without focus should show 已移除, got:\n%s", rightView)
	}
}
