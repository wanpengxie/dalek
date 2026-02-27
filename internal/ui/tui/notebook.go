package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"dalek/internal/app"
)

type notebookMode int

const (
	notebookModeList notebookMode = iota
	notebookModeDetail
	notebookModeAdd
)

type notebookModel struct {
	p           *app.Project
	projectName string

	mode   notebookMode
	width  int
	height int

	table      table.Model
	notes      []app.NoteView
	rowNoteIDs []uint
	titleWidth int
	loading    bool

	hasDetail      bool
	detailNote     app.NoteView
	detailNoteID   uint
	detailViewport viewport.Model
	detailLoading  bool

	addInput      textarea.Model
	addSubmitting bool

	confirming    bool
	confirmAction notebookAction
	confirmNoteID uint

	status      string
	errMsg      string
	lastRefresh time.Time
}

func newNotebookModel(p *app.Project, projectName string) notebookModel {
	cols := []table.Column{
		{Title: "ID", Width: 6},
		{Title: "状态", Width: 12},
		{Title: "标题", Width: 48},
		{Title: "创建时间", Width: 14},
	}
	t := table.New(
		table.WithColumns(cols),
		table.WithFocused(true),
		table.WithHeight(12),
	)
	t.SetStyles(tableStyles())

	ti := textarea.New()
	ti.Placeholder = "输入原始需求（支持多行），Ctrl+S 提交"
	ti.CharLimit = 10000
	ti.ShowLineNumbers = false
	ti.Prompt = ""
	ti.SetWidth(60)
	ti.SetHeight(10)

	vp := viewport.New(0, 0)

	return notebookModel{
		p:              p,
		projectName:    strings.TrimSpace(projectName),
		mode:           notebookModeList,
		table:          t,
		notes:          nil,
		rowNoteIDs:     nil,
		titleWidth:     48,
		loading:        true,
		hasDetail:      false,
		detailNote:     app.NoteView{},
		detailNoteID:   0,
		detailViewport: vp,
		detailLoading:  false,
		addInput:       ti,
		addSubmitting:  false,
		confirming:     false,
		confirmAction:  "",
		confirmNoteID:  0,
		status:         "加载 note 列表中...",
		errMsg:         "",
		lastRefresh:    time.Time{},
	}
}

func (m notebookModel) Init() tea.Cmd {
	return m.loadNotesCmd()
}

func (m notebookModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.updateLayout()
		return m, nil

	case notebookListLoadedMsg:
		m.loading = false
		if msg.Err != nil {
			m.errMsg = msg.Err.Error()
			m.status = "加载 note 列表失败"
			return m, nil
		}
		m.errMsg = ""
		keepID := m.selectedNoteID()
		m.notes = msg.Notes
		m.rebuildRows(keepID)
		m.lastRefresh = msg.LoadedAt
		if strings.TrimSpace(m.status) == "" || strings.Contains(m.status, "加载") || strings.Contains(m.status, "刷新") {
			m.status = fmt.Sprintf("已加载 %d 条 note", len(m.notes))
		}
		if m.mode == notebookModeDetail && m.hasDetail {
			if note, ok := m.noteByID(m.detailNoteID); ok {
				m.setDetailNote(note, true)
			}
		}
		return m, nil

	case notebookDetailLoadedMsg:
		if msg.NoteID != m.detailNoteID {
			return m, nil
		}
		m.detailLoading = false
		if msg.Err != nil {
			m.errMsg = msg.Err.Error()
			m.status = fmt.Sprintf("加载 note #%d 详情失败", msg.NoteID)
			return m, nil
		}
		if msg.Note == nil {
			m.errMsg = ""
			m.status = fmt.Sprintf("note #%d 不存在", msg.NoteID)
			m.hasDetail = false
			m.detailViewport.SetContent(faint("note 不存在"))
			return m, nil
		}
		m.errMsg = ""
		m.setDetailNote(*msg.Note, false)
		m.status = fmt.Sprintf("已加载 note #%d 详情", msg.NoteID)
		return m, nil

	case notebookActionDoneMsg:
		if msg.Err != nil {
			m.errMsg = msg.Err.Error()
			m.status = fmt.Sprintf("%s note #%d 失败", notebookActionName(msg.Action), msg.NoteID)
			return m, nil
		}
		m.errMsg = ""
		switch msg.Action {
		case notebookActionApprove:
			if msg.TicketID != 0 {
				m.status = fmt.Sprintf("note #%d 已 approve，创建 ticket t%d", msg.NoteID, msg.TicketID)
			} else {
				m.status = fmt.Sprintf("note #%d 已 approve", msg.NoteID)
			}
		case notebookActionReject:
			m.status = fmt.Sprintf("note #%d 已 reject", msg.NoteID)
		case notebookActionDiscard:
			m.status = fmt.Sprintf("note #%d 已 discard", msg.NoteID)
		default:
			m.status = fmt.Sprintf("note #%d 操作完成", msg.NoteID)
		}
		cmds := []tea.Cmd{m.loadNotesCmd()}
		if m.mode == notebookModeDetail && m.detailNoteID == msg.NoteID {
			m.detailLoading = true
			cmds = append(cmds, m.loadNoteDetailCmd(msg.NoteID))
		}
		return m, tea.Batch(cmds...)

	case notebookAddDoneMsg:
		m.addSubmitting = false
		if msg.Err != nil {
			m.errMsg = msg.Err.Error()
			m.status = "新增 note 失败"
			return m, nil
		}
		m.errMsg = ""
		m.mode = notebookModeList
		m.addInput.SetValue("")
		m.addInput.Blur()
		if msg.Result.Deduped {
			m.status = fmt.Sprintf("note 已去重，复用 #%d", msg.Result.NoteID)
		} else {
			m.status = fmt.Sprintf("新增 note #%d 成功", msg.Result.NoteID)
		}
		m.loading = true
		return m, m.loadNotesCmd()

	case tea.KeyMsg:
		if msg.String() == "q" || msg.String() == "ctrl+c" {
			return m, tea.Quit
		}
		if m.confirming {
			return m.updateConfirm(msg)
		}
		switch m.mode {
		case notebookModeDetail:
			return m.updateDetail(msg)
		case notebookModeAdd:
			return m.updateAdd(msg)
		default:
			return m.updateList(msg)
		}
	}
	return m, nil
}

func (m notebookModel) View() string {
	w := m.width
	if w <= 0 {
		w = 100
	}
	innerW := max(20, w-4)

	header := m.headerView(innerW)
	body := ""
	switch m.mode {
	case notebookModeDetail:
		body = m.detailView(innerW)
	case notebookModeAdd:
		body = m.addView(innerW)
	default:
		body = m.listView(innerW)
	}
	if m.confirming {
		body = lipgloss.JoinVertical(lipgloss.Left, body, "", m.confirmView(innerW))
	}
	footer := m.footerView(innerW)

	content := lipgloss.JoinVertical(lipgloss.Left, header, body, footer)
	return appStyle().Render(content) + "\n"
}

func (m notebookModel) updateList(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		return m, closeNotebookCmd()
	case "enter":
		noteID := m.selectedNoteID()
		if noteID == 0 {
			m.status = "暂无可查看的 note"
			m.errMsg = ""
			return m, nil
		}
		m.mode = notebookModeDetail
		m.detailNoteID = noteID
		m.detailLoading = true
		m.errMsg = ""
		if note, ok := m.noteByID(noteID); ok {
			m.setDetailNote(note, false)
		} else {
			m.hasDetail = false
			m.detailViewport.SetContent(faint("加载中..."))
		}
		return m, m.loadNoteDetailCmd(noteID)
	case "a":
		return m.beginConfirm(notebookActionApprove)
	case "x":
		return m.beginConfirm(notebookActionReject)
	case "D":
		return m.beginConfirm(notebookActionDiscard)
	case "c":
		m.mode = notebookModeAdd
		m.addSubmitting = false
		m.addInput.SetValue("")
		m.addInput.Focus()
		m.errMsg = ""
		m.status = "输入需求后按 Ctrl+S 提交"
		return m, nil
	case "r":
		m.loading = true
		m.errMsg = ""
		m.status = "刷新 note 列表中..."
		return m, m.loadNotesCmd()
	}

	var cmd tea.Cmd
	m.table, cmd = m.table.Update(msg)
	return m, cmd
}

func (m notebookModel) updateDetail(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.mode = notebookModeList
		return m, nil
	case "a":
		return m.beginConfirm(notebookActionApprove)
	case "x":
		return m.beginConfirm(notebookActionReject)
	case "D":
		return m.beginConfirm(notebookActionDiscard)
	case "c":
		m.mode = notebookModeAdd
		m.addSubmitting = false
		m.addInput.SetValue("")
		m.addInput.Focus()
		m.errMsg = ""
		m.status = "输入需求后按 Ctrl+S 提交"
		return m, nil
	case "r":
		if m.detailNoteID == 0 {
			return m, nil
		}
		m.loading = true
		m.detailLoading = true
		m.errMsg = ""
		m.status = fmt.Sprintf("刷新 note #%d 中...", m.detailNoteID)
		return m, tea.Batch(m.loadNotesCmd(), m.loadNoteDetailCmd(m.detailNoteID))
	case "j":
		m.detailViewport.LineDown(1)
		return m, nil
	case "k":
		m.detailViewport.LineUp(1)
		return m, nil
	}
	var cmd tea.Cmd
	m.detailViewport, cmd = m.detailViewport.Update(msg)
	return m, cmd
}

func (m notebookModel) updateAdd(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.mode = notebookModeList
		m.addSubmitting = false
		m.addInput.Blur()
		m.status = "已取消新增"
		m.errMsg = ""
		return m, nil
	case "ctrl+s":
		if m.addSubmitting {
			return m, nil
		}
		raw := strings.TrimSpace(m.addInput.Value())
		if raw == "" {
			m.status = "note 文本不能为空"
			m.errMsg = ""
			return m, nil
		}
		m.addSubmitting = true
		m.errMsg = ""
		m.status = "新增 note 中..."
		return m, m.addNoteCmd(raw)
	}
	var cmd tea.Cmd
	m.addInput, cmd = m.addInput.Update(msg)
	return m, cmd
}

func (m notebookModel) updateConfirm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "n", "esc":
		m.confirming = false
		m.confirmAction = ""
		m.confirmNoteID = 0
		m.status = "已取消操作"
		return m, nil
	case "enter", "y":
		noteID := m.confirmNoteID
		action := m.confirmAction
		m.confirming = false
		m.confirmAction = ""
		m.confirmNoteID = 0
		m.errMsg = ""
		m.status = fmt.Sprintf("%s note #%d 中...", notebookActionName(action), noteID)
		return m, m.runNoteActionCmd(action, noteID)
	default:
		return m, nil
	}
}

func (m notebookModel) beginConfirm(action notebookAction) (tea.Model, tea.Cmd) {
	note, ok := m.currentNote()
	if !ok || note.ID == 0 {
		m.status = "请先选择 note"
		m.errMsg = ""
		return m, nil
	}
	if !notebookCanAction(note, action) {
		m.status = fmt.Sprintf("当前状态不支持 %s", notebookActionName(action))
		m.errMsg = ""
		return m, nil
	}
	m.confirming = true
	m.confirmAction = action
	m.confirmNoteID = note.ID
	return m, nil
}

func (m notebookModel) currentNote() (app.NoteView, bool) {
	if m.mode == notebookModeDetail && m.hasDetail && m.detailNote.ID != 0 {
		return m.detailNote, true
	}
	noteID := m.selectedNoteID()
	if noteID == 0 {
		return app.NoteView{}, false
	}
	return m.noteByID(noteID)
}

func (m notebookModel) selectedNoteID() uint {
	i := m.table.Cursor()
	if i < 0 || i >= len(m.rowNoteIDs) {
		return 0
	}
	return m.rowNoteIDs[i]
}

func (m notebookModel) noteByID(noteID uint) (app.NoteView, bool) {
	for _, note := range m.notes {
		if note.ID == noteID {
			return note, true
		}
	}
	return app.NoteView{}, false
}

func (m *notebookModel) rebuildRows(keepID uint) {
	rows := make([]table.Row, 0, len(m.notes))
	ids := make([]uint, 0, len(m.notes))
	for _, note := range m.notes {
		rows = append(rows, table.Row{
			fmt.Sprintf("%d", note.ID),
			notebookStatusBadge(note),
			trimCell(notebookTitle(note), m.titleWidth),
			notebookCreatedAt(note.CreatedAt),
		})
		ids = append(ids, note.ID)
	}
	m.table.SetRows(rows)
	m.rowNoteIDs = ids

	if len(rows) == 0 {
		return
	}
	target := 0
	if keepID != 0 {
		for i, id := range ids {
			if id == keepID {
				target = i
				break
			}
		}
	}
	m.table.SetCursor(target)
}

func (m *notebookModel) updateLayout() {
	w := m.width
	h := m.height
	if w <= 0 {
		w = 100
	}
	if h <= 0 {
		h = 30
	}

	innerW := max(40, w-8)
	idW := 6
	statusW := 12
	timeW := 14
	titleW := max(24, innerW-idW-statusW-timeW-8)
	m.titleWidth = titleW

	m.table.SetColumns([]table.Column{
		{Title: "ID", Width: idW},
		{Title: "状态", Width: statusW},
		{Title: "标题", Width: titleW},
		{Title: "创建时间", Width: timeW},
	})
	m.table.SetWidth(idW + statusW + titleW + timeW + 6)
	m.table.SetHeight(max(6, h-8))

	m.addInput.SetWidth(max(30, innerW-4))
	m.addInput.SetHeight(max(8, h-14))

	m.detailViewport.Width = max(30, innerW-4)
	m.detailViewport.Height = max(8, h-14)
	if m.hasDetail {
		m.setDetailNote(m.detailNote, true)
	}
}

func (m *notebookModel) setDetailNote(note app.NoteView, keepOffset bool) {
	offset := 0
	if keepOffset {
		offset = m.detailViewport.YOffset
	}
	m.hasDetail = true
	m.detailNote = note
	m.detailNoteID = note.ID
	m.detailViewport.SetContent(m.renderDetail(note))
	if keepOffset {
		m.detailViewport.SetYOffset(offset)
	} else {
		m.detailViewport.SetYOffset(0)
	}
}

func (m notebookModel) renderDetail(note app.NoteView) string {
	b := strings.Builder{}
	b.WriteString(kv("状态:", notebookStatusText(note)))
	b.WriteString("\n")
	b.WriteString(kv("创建:", notebookCreatedAt(note.CreatedAt)))
	b.WriteString("\n")
	b.WriteString(kv("更新时间:", notebookCreatedAt(note.UpdatedAt)))
	b.WriteString("\n\n")

	b.WriteString(panelTitle("原始需求"))
	b.WriteString("\n")
	raw := strings.TrimSpace(note.Text)
	if raw == "" {
		raw = "(empty)"
	}
	b.WriteString(raw)
	b.WriteString("\n")

	if note.Shaped != nil {
		b.WriteString("\n")
		b.WriteString(panelTitle("Shaped 结果"))
		b.WriteString("\n")
		b.WriteString(kv("标题:", strings.TrimSpace(note.Shaped.Title)))
		b.WriteString("\n")
		b.WriteString(kv("描述:", strings.TrimSpace(note.Shaped.Description)))
		b.WriteString("\n")

		items := notebookAcceptanceItems(note.Shaped.AcceptanceJSON)
		if len(items) > 0 {
			b.WriteString("Acceptance:\n")
			for _, item := range items {
				b.WriteString("  - ")
				b.WriteString(strings.TrimSpace(item))
				b.WriteString("\n")
			}
		}

		if scope := strings.TrimSpace(note.Shaped.ScopeEstimate); scope != "" {
			b.WriteString(kv("规模:", scope))
			b.WriteString("\n")
		}
		if pm := strings.TrimSpace(note.Shaped.PMNotes); pm != "" {
			b.WriteString(kv("PM 注记:", pm))
			b.WriteString("\n")
		}
		if rc := strings.TrimSpace(note.Shaped.ReviewComment); rc != "" {
			b.WriteString(kv("评审备注:", rc))
			b.WriteString("\n")
		}
	}

	if strings.TrimSpace(note.LastError) != "" {
		b.WriteString("\n")
		b.WriteString(panelTitle("最近错误"))
		b.WriteString("\n")
		b.WriteString(lipgloss.NewStyle().Foreground(cDanger).Render(note.LastError))
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String())
}

func (m notebookModel) headerView(width int) string {
	left := panelTitle("Notebook")
	project := strings.TrimSpace(m.projectName)
	if project == "" {
		project = "-"
	}
	meta := faint("project: " + trimCell(project, max(10, width-30)))
	right := faint(fmt.Sprintf("notes: %d", len(m.notes)))
	fullLeft := left + "  " + meta
	if lipgloss.Width(fullLeft)+1+lipgloss.Width(right) > width {
		return headerStyle().Width(width).Render(fullLeft)
	}
	space := strings.Repeat(" ", max(0, width-lipgloss.Width(fullLeft)-lipgloss.Width(right)))
	return headerStyle().Width(width).Render(fullLeft + space + right)
}

func (m notebookModel) footerView(width int) string {
	help := m.helpForMode()
	left := help + "  |  "
	if m.errMsg != "" {
		left += lipgloss.NewStyle().Foreground(cDanger).Bold(true).Render("错误: " + m.errMsg)
	} else {
		left += strings.TrimSpace(m.status)
	}
	if !m.lastRefresh.IsZero() {
		left += faint("  |  刷新: " + m.lastRefresh.Format("15:04:05"))
	}
	if lipgloss.Width(left) > width {
		left = cutANSI(left, width)
	}
	return footerStyle().Width(width).Render(left)
}

func (m notebookModel) helpForMode() string {
	if m.confirming {
		return "Enter/y 确认  n/Esc 取消"
	}
	switch m.mode {
	case notebookModeDetail:
		return "j/k 滚动  a approve  x reject  D discard  c 新增  r 刷新  Esc 返回列表  q 退出"
	case notebookModeAdd:
		return "输入需求  Ctrl+S 提交  Esc 取消  q 退出"
	default:
		return "j/k 上下  Enter 详情  a approve  x reject  D discard  c 新增  r 刷新  Esc 返回  q 退出"
	}
}

func (m notebookModel) listView(width int) string {
	head := panelTitle(fmt.Sprintf("Notes (%d)", len(m.notes)))
	if m.loading {
		head += "  " + faint("loading...")
	}
	content := head + "\n" + m.table.View()
	if len(m.notes) == 0 && !m.loading {
		content += "\n" + faint("暂无 note，按 c 新增")
	}
	return panelStyle().Width(width).Render(content)
}

func (m notebookModel) detailView(width int) string {
	head := panelTitle("Note Detail")
	if m.detailNoteID != 0 {
		head = panelTitle(fmt.Sprintf("Note #%d", m.detailNoteID))
	}
	if m.detailLoading {
		head += "  " + faint("loading...")
	}
	content := head + "\n"
	if m.hasDetail {
		content += m.detailViewport.View()
	} else {
		content += faint("暂无详情")
	}
	return panelStyle().Width(width).Render(content)
}

func (m notebookModel) addView(width int) string {
	head := panelTitle("新增 Note")
	if m.addSubmitting {
		head += "  " + faint("submitting...")
	}
	content := head + "\n" + faint("输入原始需求后按 Ctrl+S 提交，Esc 取消") + "\n\n" + m.addInput.View()
	return panelStyle().Width(width).Render(content)
}

func (m notebookModel) confirmView(width int) string {
	w := min(78, max(40, width))
	borderColor := cWarn
	if m.confirmAction == notebookActionDiscard || m.confirmAction == notebookActionReject {
		borderColor = cDanger
	}
	content := panelTitle("二次确认") +
		"\n\n" + notebookActionPrompt(m.confirmAction, m.confirmNoteID) +
		"\n\n" + faint("Enter/y 确认  n/Esc 取消")
	return panelStyle().
		BorderForeground(borderColor).
		Width(w).
		Render(content)
}
