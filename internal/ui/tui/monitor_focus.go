package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"dalek/internal/contracts"
)

// ---------- 常量 ----------

const focusEventBufferSize = 40

// ---------- 消息类型 ----------

type focusRefreshedMsg struct {
	View      *contracts.FocusRunView
	NewEvents []contracts.FocusEvent
	Err       error
}

// ---------- focus 数据刷新 ----------

func (m model) focusRefreshCmd() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()

		view, err := m.p.FocusGet(ctx, 0) // 0 = 当前活跃 focus
		if err != nil {
			// ErrRecordNotFound → 无活跃 focus，正常
			return focusRefreshedMsg{View: nil}
		}
		var events []contracts.FocusEvent
		if view.Run.ID != 0 {
			pr, _ := m.p.FocusPoll(ctx, view.Run.ID, m.focusEventLatestID)
			events = pr.Events
		}
		return focusRefreshedMsg{View: &view, NewEvents: events}
	}
}

// applyFocusRefresh 处理 focusRefreshedMsg，更新 model 中的 focus 状态。
func (m *model) applyFocusRefresh(msg focusRefreshedMsg) {
	if msg.View == nil || msg.View.Run.ID == 0 {
		m.focusView = nil
		m.focusEvents = nil
		m.focusEventLatestID = 0
		m.focusRefreshInFlight = false
		m.ticketFocusItemByID = nil
		return
	}
	m.focusView = msg.View
	m.focusRefreshInFlight = false

	// 追加新事件到环形缓冲
	if len(msg.NewEvents) > 0 {
		m.focusEvents = append(m.focusEvents, msg.NewEvents...)
		if len(m.focusEvents) > focusEventBufferSize {
			m.focusEvents = m.focusEvents[len(m.focusEvents)-focusEventBufferSize:]
		}
		m.focusEventLatestID = msg.NewEvents[len(msg.NewEvents)-1].ID
	}

	// 构建 ticketID → FocusRunItem 映射
	m.ticketFocusItemByID = make(map[uint]*contracts.FocusRunItem, len(msg.View.Items))
	for i := range msg.View.Items {
		it := &msg.View.Items[i]
		m.ticketFocusItemByID[it.TicketID] = it
	}
}

// ---------- Layer 1: Manager 行动态内容 ----------

func (m model) managerRowCells() (status, runtime, title string) {
	fv := m.focusView
	if fv == nil {
		return "manager", "就绪", "项目管理员"
	}
	r := fv.Run

	completed := countItemsByStatus(fv.Items, contracts.FocusItemCompleted)
	total := len(fv.Items)
	budget := fmt.Sprintf("budget %d/%d", r.AgentBudget, r.AgentBudgetMax)

	switch r.Mode {
	case contracts.FocusModeBatch:
		if r.IsTerminal() {
			status = batchTerminalGlyph(r.Status)
			runtime = fmt.Sprintf("%d/%d done", completed, total)
			title = fmt.Sprintf("focus#%d  %s  %s", r.ID, terminalLabel(r.Status), budget)
		} else if r.Status == contracts.FocusBlocked {
			status = "batch!"
			runtime = fmt.Sprintf("%d/%d block", completed, total)
			title = fmt.Sprintf("focus#%d  blocked  %s", r.ID, budget)
		} else {
			status = "batch▶"
			runtime = fmt.Sprintf("%d/%d items", completed, total)
			title = fmt.Sprintf("focus#%d  %s", r.ID, budget)
		}

	case contracts.FocusModeConvergent:
		roundNum := r.PMRunCount + 1
		if fv.LatestRound != nil {
			roundNum = fv.LatestRound.RoundNumber
		}
		round := fmt.Sprintf("r%d/%d", roundNum, r.MaxPMRuns)

		if r.IsTerminal() {
			status = convTerminalGlyph(r.Status)
			runtime = round
			title = fmt.Sprintf("focus#%d  %s  %s", r.ID, terminalLabel(r.Status), budget)
		} else if r.ConvergentPhase == "pm_run" {
			status = "conv·pm"
			runtime = round + " 审查"
			title = fmt.Sprintf("focus#%d  reviewing…  %s", r.ID, budget)
		} else {
			status = "conv·bat"
			runtime = fmt.Sprintf("%s %d/%d", round, completed, total)
			title = fmt.Sprintf("focus#%d  %s", r.ID, budget)
		}

	default:
		status = "focus▶"
		runtime = fmt.Sprintf("%d/%d", completed, total)
		title = fmt.Sprintf("focus#%d  %s", r.ID, budget)
	}
	return
}

// ---------- Layer 2: Inspector 三栏 focus 内容 ----------

// focusInspectorLeftView 渲染 focus 总览 + items 列表。
func (m model) focusInspectorLeftView(panelW int) string {
	innerW := max(10, panelW-4)
	fv := m.focusView

	modeStr := fv.Run.Mode
	statusStr := fv.Run.Status
	budgetBar := progressBar(fv.Run.AgentBudget, fv.Run.AgentBudgetMax, 10)
	budgetText := fmt.Sprintf("%d/%d", fv.Run.AgentBudget, fv.Run.AgentBudgetMax)

	lines := []string{
		panelTitle(fmt.Sprintf("focus#%d  %s  %s", fv.Run.ID, modeStr, statusStr)),
		badge(modeStr, cInfo) + " " + badge(statusStr, focusStatusColor(statusStr)),
		kvLine("mode:", modeStr, innerW),
	}

	if fv.Run.Mode == contracts.FocusModeConvergent {
		phase := fv.Run.ConvergentPhase
		if phase == "" {
			phase = "initializing"
		}
		roundNum := 0
		if fv.LatestRound != nil {
			roundNum = fv.LatestRound.RoundNumber
		}
		lines = append(lines,
			kvLine("phase:", fmt.Sprintf("%s  (round %d/%d)", phase, roundNum, fv.Run.MaxPMRuns), innerW),
			kvLine("pm_runs:", fmt.Sprintf("%d/%d", fv.Run.PMRunCount, fv.Run.MaxPMRuns), innerW),
		)
	}

	lines = append(lines, kvLine("budget:", budgetBar+"  "+budgetText, innerW))
	lines = append(lines, "")

	// items 列表
	completed := 0
	total := len(fv.Items)
	lines = append(lines, faint("本轮 items:"))
	for _, it := range fv.Items {
		sym := focusItemSymbol(it.Status)
		workerInfo := ""
		if it.CurrentWorkerID != nil {
			workerInfo = fmt.Sprintf("  w%d", *it.CurrentWorkerID)
		}
		line := fmt.Sprintf("  %s %d  t#%d  %s%s", sym, it.Seq, it.TicketID, it.Status, workerInfo)
		lines = append(lines, cutANSI(line, innerW))
		if it.Status == contracts.FocusItemCompleted {
			completed++
		}
	}

	// progress bar
	lines = append(lines, "")
	pct := 0
	if total > 0 {
		pct = completed * 100 / total
	}
	lines = append(lines, kvLine("progress:", fmt.Sprintf("%d/%d  %s  %d%%", completed, total, progressBar(completed, total, 10), pct), innerW))

	lines = padBottom(lines, 6+tailShowLines)
	return strings.Join(lines, "\n")
}

// focusInspectorMiddleView 渲染阶段详情。
func (m model) focusInspectorMiddleView(panelW int) string {
	innerW := max(10, panelW-4)
	fv := m.focusView

	if fv.Run.Mode == contracts.FocusModeConvergent {
		return m.focusConvergentMiddleView(innerW)
	}
	return m.focusBatchMiddleView(innerW)
}

func (m model) focusConvergentMiddleView(innerW int) string {
	fv := m.focusView
	lines := []string{
		panelTitle("收敛轮次"),
		badge("convergent", cInfo),
	}

	for _, rd := range fv.Rounds {
		sym := "·"
		if rd.BatchStatus == "completed" && rd.PMRunStatus == "done" {
			sym = "✓"
		} else if rd.BatchStatus == "running" || rd.PMRunStatus == "running" {
			sym = "▶"
		}
		lines = append(lines, "")
		lines = append(lines, fmt.Sprintf("round %d  %s", rd.RoundNumber, sym))

		batchSym := focusBatchPhaseGlyph(rd.BatchStatus)
		lines = append(lines, cutANSI(fmt.Sprintf("  batch   %s %s", batchSym, rd.BatchStatus), innerW))

		pmSym := focusPMPhaseGlyph(rd.PMRunStatus)
		lines = append(lines, cutANSI(fmt.Sprintf("  pm_run  %s %s", pmSym, rd.PMRunStatus), innerW))

		if rd.Verdict != "" {
			verdictLine := fmt.Sprintf("  verdict %s", rd.Verdict)
			if rd.Verdict == "needs_fix" {
				fixIDs := parseTicketIDs(rd.FixTicketIDs)
				if len(fixIDs) > 0 {
					parts := make([]string, len(fixIDs))
					for i, id := range fixIDs {
						parts[i] = fmt.Sprintf("t#%d", id)
					}
					verdictLine += " → " + strings.Join(parts, " ")
				}
			}
			lines = append(lines, cutANSI(verdictLine, innerW))
		}
	}

	// 待处理 issues
	issues := collectPendingIssues(m.orderedViews(), 6)
	if len(issues) > 0 {
		lines = append(lines, "")
		lines = append(lines, faint(fmt.Sprintf("待处理 issues (%d项)", len(issues))))
		for _, it := range issues {
			lines = append(lines, cutANSI(" - "+oneLine(it), innerW))
		}
	}

	lines = padBottom(lines, 4+tailShowLines)
	return strings.Join(lines, "\n")
}

func (m model) focusBatchMiddleView(innerW int) string {
	fv := m.focusView
	lines := []string{
		panelTitle("当前执行"),
		badge("batch", cInfo),
	}

	// active item
	if fv.ActiveItem != nil {
		ai := fv.ActiveItem
		workerStr := ""
		if ai.CurrentWorkerID != nil {
			workerStr = fmt.Sprintf("  w%d", *ai.CurrentWorkerID)
		}
		lines = append(lines, "")
		lines = append(lines, kvLine("active:", fmt.Sprintf("t#%d%s  %s", ai.TicketID, workerStr, ai.Status), innerW))
	} else {
		lines = append(lines, kvLine("active:", "-", innerW))
	}

	// 已完成列表
	completedItems := make([]contracts.FocusRunItem, 0)
	for _, it := range fv.Items {
		if it.Status == contracts.FocusItemCompleted {
			completedItems = append(completedItems, it)
		}
	}
	if len(completedItems) > 0 {
		lines = append(lines, "")
		lines = append(lines, faint("已完成"))
		for _, it := range completedItems {
			dur := ""
			if it.StartedAt != nil && it.FinishedAt != nil {
				dur = "  " + shortDuration(it.FinishedAt.Sub(*it.StartedAt))
			}
			lines = append(lines, cutANSI(fmt.Sprintf("t#%d  ✓  completed%s", it.TicketID, dur), innerW))
		}
	}

	// 待处理 issues
	issues := collectPendingIssues(m.orderedViews(), 6)
	if len(issues) > 0 {
		lines = append(lines, "")
		lines = append(lines, faint(fmt.Sprintf("待处理 issues (%d项)", len(issues))))
		for _, it := range issues {
			lines = append(lines, cutANSI(" - "+oneLine(it), innerW))
		}
	}

	lines = padBottom(lines, 4+tailShowLines)
	return strings.Join(lines, "\n")
}

// focusInspectorRightView 渲染实时 focus 事件流。
func (m model) focusInspectorRightView(panelW int) string {
	innerW := max(10, panelW-4)

	lines := []string{
		panelTitle("focus 事件流"),
		badge("live", cOk),
	}

	if len(m.focusEvents) == 0 {
		lines = append(lines, faint("(暂无事件)"))
	} else {
		for _, ev := range m.focusEvents {
			color := focusEventColor(ev.Kind)
			kindStr := lipgloss.NewStyle().Foreground(color).Render(ev.Kind)
			summary := strings.TrimSpace(ev.Summary)
			if summary == "" {
				summary = ""
			} else {
				summary = "  " + summary
			}
			eventLine := fmt.Sprintf("[#%d] %s%s", ev.ID, kindStr, summary)
			lines = append(lines, cutANSI(eventLine, innerW))
		}
	}

	lines = padBottom(lines, 4+tailShowLines)
	return strings.Join(lines, "\n")
}

// ---------- Layer 3: Ticket 行 focus 标注 ----------

// focusItemRuntimeOverlay 返回 ticket 行的 runtime 列叠加文本。
// 当 ticket 是 focus item 且处于 merging/awaiting_merge_observation 时，覆盖 runtime 列。
func (m model) focusItemRuntimeOverlay(ticketID uint) (string, bool) {
	if m.ticketFocusItemByID == nil {
		return "", false
	}
	item, ok := m.ticketFocusItemByID[ticketID]
	if !ok {
		return "", false
	}
	switch item.Status {
	case contracts.FocusItemMerging:
		return "合并中▶", true
	case contracts.FocusItemAwaitingMergeObservation:
		return "待观测…", true
	}
	return "", false
}

// focusLabelPrefix 返回 ticket 标签前缀（属于 active focus 时加 ◈）。
func (m model) focusLabelPrefix(ticketID uint) string {
	if m.ticketFocusItemByID == nil {
		return ""
	}
	if _, ok := m.ticketFocusItemByID[ticketID]; ok {
		return "◈"
	}
	return ""
}

// ---------- 辅助函数 ----------

func countItemsByStatus(items []contracts.FocusRunItem, status string) int {
	n := 0
	for _, it := range items {
		if it.Status == status {
			n++
		}
	}
	return n
}

func batchTerminalGlyph(status string) string {
	switch status {
	case contracts.FocusCompleted:
		return "batch✓"
	case contracts.FocusStopped:
		return "batch■"
	case contracts.FocusFailed:
		return "batch✗"
	case contracts.FocusCanceled:
		return "batch✗"
	default:
		return "batch✓"
	}
}

func convTerminalGlyph(status string) string {
	switch status {
	case contracts.FocusConverged:
		return "✓conv"
	case contracts.FocusExhausted:
		return "⚠exhaust"
	case contracts.FocusCompleted:
		return "✓conv"
	case contracts.FocusStopped:
		return "conv■"
	case contracts.FocusFailed:
		return "conv✗"
	case contracts.FocusCanceled:
		return "conv✗"
	default:
		return "conv✓"
	}
}

func terminalLabel(status string) string {
	switch status {
	case contracts.FocusConverged:
		return "✓ 已收敛"
	case contracts.FocusExhausted:
		return "未收敛·达上限"
	case contracts.FocusCompleted:
		return "✓ completed"
	case contracts.FocusStopped:
		return "已停止"
	case contracts.FocusFailed:
		return "失败"
	case contracts.FocusCanceled:
		return "已取消"
	default:
		return status
	}
}

func focusItemSymbol(status string) string {
	switch status {
	case contracts.FocusItemCompleted:
		return "✓"
	case contracts.FocusItemExecuting:
		return "▶"
	case contracts.FocusItemMerging:
		return "⇄"
	case contracts.FocusItemAwaitingMergeObservation:
		return "◎"
	case contracts.FocusItemBlocked:
		return "!"
	case contracts.FocusItemPending, contracts.FocusItemQueued:
		return "·"
	case contracts.FocusItemStopped, contracts.FocusItemFailed, contracts.FocusItemCanceled:
		return "✗"
	default:
		return "?"
	}
}

func focusStatusColor(status string) lipgloss.TerminalColor {
	switch status {
	case contracts.FocusRunning, contracts.FocusQueued:
		return cInfo
	case contracts.FocusCompleted, contracts.FocusConverged:
		return cOk
	case contracts.FocusBlocked:
		return cWarn
	case contracts.FocusFailed:
		return cDanger
	case contracts.FocusExhausted:
		return cWarn
	case contracts.FocusStopped, contracts.FocusCanceled:
		return cNeutral
	default:
		return cMuted
	}
}

func focusEventColor(kind string) lipgloss.TerminalColor {
	switch {
	case strings.HasPrefix(kind, "item."):
		return cOk
	case strings.HasPrefix(kind, "merge."):
		return cInfo
	case strings.HasPrefix(kind, "convergent.converged"):
		return cOk
	case strings.HasPrefix(kind, "convergent.exhausted"):
		return cWarn
	case strings.HasPrefix(kind, "convergent."):
		return cInfo
	case strings.Contains(kind, "blocked") || strings.Contains(kind, "error"):
		return cWarn
	default:
		return cMuted
	}
}

func focusBatchPhaseGlyph(status string) string {
	switch status {
	case "completed":
		return "✓"
	case "running":
		return "▶"
	case "pending":
		return "·"
	case "failed", "canceled", "blocked":
		return "✗"
	default:
		return "·"
	}
}

func focusPMPhaseGlyph(status string) string {
	switch status {
	case "done":
		return "✓"
	case "running":
		return "▶"
	case "pending":
		return "·"
	case "failed", "canceled":
		return "✗"
	default:
		return "·"
	}
}

func progressBar(current, total, width int) string {
	if total <= 0 {
		return strings.Repeat("░", width)
	}
	filled := current * width / total
	if filled > width {
		filled = width
	}
	return strings.Repeat("█", filled) + strings.Repeat("░", width-filled)
}

func parseTicketIDs(jsonStr string) []uint {
	jsonStr = strings.TrimSpace(jsonStr)
	if jsonStr == "" || jsonStr == "[]" {
		return nil
	}
	var ids []uint
	_ = json.Unmarshal([]byte(jsonStr), &ids)
	return ids
}
