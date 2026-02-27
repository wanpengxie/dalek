package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"dalek/internal/contracts"
)

func (m model) updateTable(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "g":
		// 跳到“管理员 session”行（固定在第一行）。
		if len(m.rowRefs) == 0 {
			m.status = "暂无数据"
			return m, nil
		}
		m.table.SetCursor(0)
		sel := m.selectedRow()
		m.lastSelected = sel
		m.tailRef = sel
		m.tailPreview = contracts.TailPreview{}
		m.tailErr = ""
		m.tailUpdatedAt = time.Time{}
		if sel.kind != rowNone && !m.tailInFlight && m.canCaptureTail(sel) {
			m.tailInFlight = true
			m.tailStartedAt = time.Now()
			return m, m.tailCmd(sel)
		}
		return m, nil
	case "r":
		id, ok, denied := m.selectedTicketForAction(ticketActionWorkerRun)
		if !ok {
			if denied != "" {
				m.status = denied
				m.errMsg = ""
			}
			return m, nil
		}
		m.status = fmt.Sprintf("重新跑中 t%d...", id)
		m.errMsg = ""
		return m, m.workerRunTicketCmd(id)
	case "n":
		return m, openNotebookCmd()
	case "c":
		m.mode = modeNewTicket
		m.newFocus = 0
		m.titleInput.Focus()
		m.titleInput.SetValue("")
		m.newDesc.SetValue("")
		m.newDesc.Blur()
		m.errMsg = ""
		return m, nil
	case "s":
		id, ok, denied := m.selectedTicketForAction(ticketActionStart)
		if !ok {
			if denied != "" {
				m.status = denied
				m.errMsg = ""
			}
			return m, nil
		}
		return m, m.startTicketCmd(id)
	case "p":
		id, ok, denied := m.selectedTicketForAction(ticketActionDispatch)
		if !ok {
			if denied != "" {
				m.status = denied
				m.errMsg = ""
			}
			return m, nil
		}
		m.status = fmt.Sprintf("派发中 t%d...", id)
		m.errMsg = ""
		return m, m.dispatchTicketCmd(id)
	case "i":
		id, ok, denied := m.selectedTicketForAction(ticketActionInterrupt)
		if !ok {
			if denied != "" {
				m.status = denied
				m.errMsg = ""
			}
			return m, nil
		}
		m.status = fmt.Sprintf("中断中 t%d...", id)
		return m, m.interruptTicketCmd(id)
	case "k":
		id, ok, denied := m.selectedTicketForAction(ticketActionStop)
		if !ok {
			if denied != "" {
				m.status = denied
				m.errMsg = ""
			}
			return m, nil
		}
		return m, m.stopTicketCmd(id)
	case "a":
		sel := m.selectedRow()
		switch sel.kind {
		case rowManager:
			return m, m.attachManagerCmd()
		case rowTicket:
			id, ok, denied := m.selectedTicketForAction(ticketActionAttach)
			if !ok {
				if denied != "" {
					m.status = denied
					m.errMsg = ""
				}
				return m, nil
			}
			return m, m.attachTicketCmd(id)
		default:
			return m, nil
		}
	case "d":
		id, ok, denied := m.selectedTicketForAction(ticketActionArchive)
		if !ok {
			if denied != "" {
				m.status = denied
				m.errMsg = ""
			}
			return m, nil
		}
		return m, m.archiveTicketCmd(id)
	case "e":
		id, ok, denied := m.selectedTicketForAction(ticketActionEdit)
		if !ok {
			if denied != "" {
				m.status = denied
				m.errMsg = ""
			}
			return m, nil
		}
		v, ok := m.viewsByID[id]
		if !ok {
			m.status = "详情尚未加载（等一下自动刷新）"
			return m, nil
		}
		m.mode = modeEditTicket
		m.editTicketID = id
		m.editFocus = 0
		m.editTitle.SetValue(strings.TrimSpace(v.Ticket.Title))
		m.editDesc.SetValue(strings.TrimSpace(v.Ticket.Description))
		m.editDesc.Blur()
		m.errMsg = ""
		m.status = fmt.Sprintf("编辑 t%d（Ctrl+S 保存）", id)
		return m, m.editTitle.Focus()
	case "v":
		id, ok, denied := m.selectedTicketForAction(ticketActionEvents)
		if !ok {
			if denied != "" {
				m.status = denied
				m.errMsg = ""
			}
			return m, nil
		}
		m.mode = modeEvents
		m.eventsTicketID = id
		m.eventsWorkerID = 0
		m.eventsErr = ""
		m.eventsLoadedAt = time.Time{}
		m.eventsViewport.SetYOffset(0)
		m.eventsViewport.SetContent(faint("(加载中... 按 r 可手动刷新)"))
		m.eventsInFlight = true
		m.status = fmt.Sprintf("加载事件 t%d...", id)
		return m, m.loadEventsCmd(id)
	case "+", "=":
		id, ok, denied := m.selectedTicketForAction(ticketActionPriority)
		if !ok {
			if denied != "" {
				m.status = denied
				m.errMsg = ""
			}
			return m, nil
		}
		m.status = fmt.Sprintf("调整优先级 t%d...", id)
		return m, m.bumpPriorityCmd(id, +1)
	case "-":
		id, ok, denied := m.selectedTicketForAction(ticketActionPriority)
		if !ok {
			if denied != "" {
				m.status = denied
				m.errMsg = ""
			}
			return m, nil
		}
		m.status = fmt.Sprintf("调整优先级 t%d...", id)
		return m, m.bumpPriorityCmd(id, -1)
	case "0", "1", "2", "3", "4":
		id, ok, denied := m.selectedTicketForAction(ticketActionStatus)
		if !ok {
			if denied != "" {
				m.status = denied
				m.errMsg = ""
			}
			return m, nil
		}
		var st contracts.TicketWorkflowStatus
		switch msg.String() {
		case "0":
			st = contracts.TicketBacklog
		case "1":
			st = contracts.TicketQueued
		case "2":
			st = contracts.TicketActive
		case "3":
			st = contracts.TicketBlocked
		case "4":
			st = contracts.TicketDone
		}
		m.status = fmt.Sprintf("设置状态 t%d -> %s...", id, string(st))
		return m, m.setTicketStatusCmd(id, st)
	case "t":
		// 有些终端/主题下“自动背景色检测”不可靠，提供一个手动切换开关。
		lipgloss.SetHasDarkBackground(!lipgloss.HasDarkBackground())
		if lipgloss.HasDarkBackground() {
			m.status = "配色：深色背景"
		} else {
			m.status = "配色：浅色背景"
		}
		return m, nil
	}

	prevSel := m.selectedRow()
	var cmd tea.Cmd
	m.table, cmd = m.table.Update(msg)
	curSel := m.selectedRow()
	if m.mode == modeTable && curSel != prevSel {
		m.lastSelected = curSel
		m.tailRef = curSel
		m.tailPreview = contracts.TailPreview{}
		m.tailErr = ""
		m.tailUpdatedAt = time.Time{}
		if curSel.kind != rowNone && !m.tailInFlight && m.canCaptureTail(curSel) {
			m.tailInFlight = true
			m.tailStartedAt = time.Now()
			return m, tea.Batch(cmd, m.tailCmd(curSel))
		}
	}
	return m, cmd
}
