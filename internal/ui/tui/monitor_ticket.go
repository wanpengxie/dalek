package tui

import (
	"fmt"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

func (m model) updateNewTicket(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	prepareCreate := func() (tea.Model, tea.Cmd) {
		title := strings.TrimSpace(m.titleInput.Value())
		desc := strings.TrimSpace(m.newDesc.Value())
		if title == "" {
			m.errMsg = "标题不能为空"
			m.status = "新建失败：标题不能为空"
			return m, nil
		}
		if desc == "" {
			m.errMsg = "描述不能为空（请填写需求细节/文档路径）"
			m.status = "新建失败：描述不能为空"
			return m, nil
		}
		m.errMsg = ""
		m.titleInput.Blur()
		m.newDesc.Blur()
		return m, m.createTicketCmd(title, desc)
	}

	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "esc":
		m.mode = modeTable
		m.newFocus = 0
		m.titleInput.Blur()
		m.titleInput.SetValue("")
		m.newDesc.Blur()
		m.newDesc.SetValue("")
		m.errMsg = ""
		m.status = "已取消新建"
		return m, nil
	case "tab":
		if m.newFocus == 0 {
			m.newFocus = 1
			m.titleInput.Blur()
			return m, m.newDesc.Focus()
		}
		m.newFocus = 0
		m.newDesc.Blur()
		return m, m.titleInput.Focus()
	case "ctrl+s":
		return prepareCreate()
	case "enter":
		if m.newFocus == 1 {
			break
		}
		return prepareCreate()
	}

	var cmd tea.Cmd
	if m.newFocus == 0 {
		m.titleInput, cmd = m.titleInput.Update(msg)
		return m, cmd
	}
	m.newDesc, cmd = m.newDesc.Update(msg)
	return m, cmd
}

func (m model) updateEditTicket(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "esc":
		m.mode = modeTable
		m.editTicketID = 0
		m.editFocus = 0
		m.editTitle.Blur()
		m.editDesc.Blur()
		m.errMsg = ""
		m.status = "已取消编辑"
		return m, nil
	case "tab":
		if m.editFocus == 0 {
			m.editFocus = 1
			m.editTitle.Blur()
			return m, m.editDesc.Focus()
		}
		m.editFocus = 0
		m.editDesc.Blur()
		return m, m.editTitle.Focus()
	case "ctrl+s":
		id := m.editTicketID
		if id == 0 {
			return m, nil
		}
		title := m.editTitle.Value()
		desc := m.editDesc.Value()
		m.status = fmt.Sprintf("保存中 t%d...", id)
		return m, m.updateTicketTextCmd(id, title, desc)
	}

	var cmd tea.Cmd
	if m.editFocus == 0 {
		m.editTitle, cmd = m.editTitle.Update(msg)
		return m, cmd
	}
	m.editDesc, cmd = m.editDesc.Update(msg)
	return m, cmd
}

func (m model) newTicketView(width int) string {
	title := panelTitle("新建 Ticket")
	hint := faint("标题和描述都必填。Ctrl+S/Enter 保存，Tab 切换字段，Esc 取消")
	body := title + "  " + hint + "\n\n" +
		faint("标题:") + "\n" + m.titleInput.View() + "\n\n" +
		faint("描述:") + "\n" + m.newDesc.View()

	w := min(width, 110)
	return panelStyle().Width(w).Render(body)
}

func (m model) editTicketView(width int) string {
	id := m.editTicketID
	title := panelTitle(fmt.Sprintf("编辑 Ticket  t%d", id))
	hint := faint("Ctrl+S 保存，Tab 切换字段，Esc 返回")

	status := "-"
	prio := "-"
	if v, ok := m.viewsByID[id]; ok {
		status = formatTicketStatus(v.DerivedStatus)
		prio = strconv.Itoa(v.Ticket.Priority)
	}
	meta := faint(fmt.Sprintf("当前状态: %s  |  优先级: %s", status, prio))

	body := title + "  " + hint + "\n" + meta + "\n\n" +
		faint("标题:") + "\n" + m.editTitle.View() + "\n\n" +
		faint("描述:") + "\n" + m.editDesc.View()

	w := min(width, 110)
	return panelStyle().Width(w).Render(body)
}
