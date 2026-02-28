package tui

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"dalek/internal/app"
	"dalek/internal/contracts"
)

type mode int

const (
	modeTable mode = iota
	modeNewTicket
	modeEditTicket
	modeEvents
	modeWorkerLog
)

type rowKind int

const (
	rowNone rowKind = iota
	rowManager
	rowTicket
	rowMerge
)

type rowRef struct {
	section  string
	kind     rowKind
	ticketID uint
	mergeID  uint
}

const (
	inspectorHeight = 18

	tailRefreshEvery = 2 * time.Second
	tailCaptureLines = 60
	tailShowLines    = 8
	ticketTailLines  = 4

	workerLogRefreshEvery = 1 * time.Second
	workerLogCaptureLines = 240
)

type tableLayout struct {
	section  int
	id       int
	priority int
	status   int
	runtime  int
	title    int
	output   int
}

type tickMsg struct{}

type refreshedMsg struct {
	Views           []app.TicketView
	MergeItems      []contracts.MergeItem
	ArchivedTickets []contracts.Ticket
	MergeErr        error
	ArchiveErr      error
	Err             error

	Manual     bool
	TicketID   uint
	StartedAt  time.Time
	FinishedAt time.Time
}

type startedMsg struct {
	TicketID uint
	Err      error
}

type stoppedMsg struct {
	TicketID uint
	Err      error
}

type archivedMsg struct {
	TicketID uint
	Err      error
}

type createdMsg struct {
	TicketID uint
	Err      error
}

type attachedMsg struct {
	TicketID uint
	Err      error
}

type dispatchedMsg struct {
	TicketID uint
	Receipt  app.DaemonDispatchSubmitReceipt
	Err      error
}

type workerRunMsg struct {
	TicketID uint
	Receipt  app.DaemonWorkerRunSubmitReceipt
	Err      error
}

type interruptedMsg struct {
	TicketID uint
	Result   app.InterruptResult
	Err      error
}

type tailMsg struct {
	Ref        rowRef
	Preview    contracts.TailPreview
	Err        error
	StartedAt  time.Time
	FinishedAt time.Time
}

type ticketStatusMsg struct {
	TicketID uint
	Status   contracts.TicketWorkflowStatus
	Err      error
}

type ticketPriorityMsg struct {
	TicketID uint
	Priority int
	Delta    int
	Err      error
}

type ticketReorderMsg struct {
	TicketID       uint
	TargetTicketID uint
	TicketPriority int
	TargetPriority int
	Direction      int
	Err            error
}

type ticketTextMsg struct {
	TicketID uint
	Err      error
}

type eventsLoadedMsg struct {
	TicketID uint
	WorkerID uint
	Events   []app.TaskEvent
	Err      error
}

type workerLogLoadedMsg struct {
	TicketID uint
	WorkerID uint
	Preview  contracts.TailPreview
	Err      error
}

type model struct {
	p           *app.Project
	home        *app.Home
	projectName string

	mode   mode
	width  int
	height int

	table           table.Model
	tableLayout     tableLayout
	rowRefs         []rowRef
	viewsByID       map[uint]app.TicketView
	ticketsByID     map[uint]contracts.Ticket
	mergeItems      []contracts.MergeItem
	archivedTickets []contracts.Ticket
	showArchiveRows bool
	mergeErr        string
	archiveErr      string
	helpMsg         string
	status          string
	errMsg          string

	refreshInFlight bool
	refreshManual   bool
	refreshTicketID uint
	refreshStarted  time.Time
	lastRefresh     time.Time

	titleInput textinput.Model
	newDesc    textarea.Model
	newFocus   int // 0=title, 1=desc

	lastSelected rowRef

	tailRef       rowRef
	tailPreview   contracts.TailPreview
	tailErr       string
	tailInFlight  bool
	tailStartedAt time.Time
	tailUpdatedAt time.Time

	editTicketID uint
	editTitle    textinput.Model
	editDesc     textarea.Model
	editFocus    int // 0=title, 1=desc

	eventsViewport viewport.Model
	eventsTicketID uint
	eventsWorkerID uint
	eventsInFlight bool
	eventsErr      string
	eventsLoadedAt time.Time

	workerLogViewport viewport.Model
	workerLogTicketID uint
	workerLogWorkerID uint
	workerLogLogPath  string
	workerLogSource   string
	workerLogInFlight bool
	workerLogErr      string
	workerLogLoadedAt time.Time

	dispatchTicketID uint
}

func newModel(p *app.Project, home *app.Home, projectName string) model {
	layout := defaultTableLayout()
	cols := tableColumns(layout)
	t := table.New(
		table.WithColumns(cols),
		table.WithFocused(true),
		table.WithHeight(12),
	)
	t.SetWidth(tableTotalWidth(layout))
	t.SetStyles(tableStyles())

	ti := textinput.New()
	ti.Placeholder = "标题（必填）"
	ti.CharLimit = 120
	ti.Width = 60

	nd := textarea.New()
	nd.Placeholder = "描述（必填，多行，可写需求细节/文档路径）"
	nd.CharLimit = 4000
	nd.ShowLineNumbers = false
	nd.Prompt = ""
	nd.SetWidth(60)
	nd.SetHeight(8)

	et := textinput.New()
	et.Placeholder = "标题（必填）"
	et.CharLimit = 120
	et.Width = 60

	ed := textarea.New()
	ed.Placeholder = "描述（可选，多行）"
	ed.CharLimit = 4000
	ed.ShowLineNumbers = false
	ed.Prompt = ""
	ed.SetWidth(60)
	ed.SetHeight(8)

	vp := viewport.New(0, 0)
	lvp := viewport.New(0, 0)

	return model{
		p:               p,
		home:            home,
		projectName:     projectName,
		mode:            modeTable,
		table:           t,
		tableLayout:     layout,
		rowRefs:         nil,
		viewsByID:       map[uint]app.TicketView{},
		ticketsByID:     map[uint]contracts.Ticket{},
		mergeItems:      nil,
		archivedTickets: nil,
		showArchiveRows: false,
		mergeErr:        "",
		archiveErr:      "",
		helpMsg:         "g 管理员  n notebook  c 新建  Enter tmux  s 启动  p 派发  i 中断  a 日志  k 停止  d 归档  r 重新跑  e 编辑  v 事件  Shift+K/J backlog排序  +/- 优先级  0-4 状态  t 配色  q 退出",
		status:          "就绪",
		errMsg:          "",
		titleInput:      ti,
		newDesc:         nd,
		newFocus:        0,

		lastSelected:  rowRef{kind: rowNone},
		tailRef:       rowRef{kind: rowNone},
		tailPreview:   contracts.TailPreview{},
		tailErr:       "",
		tailInFlight:  false,
		tailStartedAt: time.Time{},
		tailUpdatedAt: time.Time{},

		editTicketID: 0,
		editTitle:    et,
		editDesc:     ed,
		editFocus:    0,

		eventsViewport: vp,
		eventsTicketID: 0,
		eventsWorkerID: 0,
		eventsInFlight: false,
		eventsErr:      "",
		eventsLoadedAt: time.Time{},

		workerLogViewport: lvp,
		workerLogTicketID: 0,
		workerLogWorkerID: 0,
		workerLogLogPath:  "",
		workerLogSource:   "",
		workerLogInFlight: false,
		workerLogErr:      "",
		workerLogLoadedAt: time.Time{},

		dispatchTicketID: 0,
	}
}

func (m model) Init() tea.Cmd {
	return tea.Batch(m.refreshCmd(), m.tickCmd())
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.table.SetHeight(max(6, msg.Height-8-inspectorHeight))

		// edit/events 的控件尺寸（粗略适配即可；更精确的布局在 View 里由 panel 再包一层）
		formW := max(30, msg.Width-12)
		m.titleInput.Width = min(80, formW-4)
		m.newDesc.SetWidth(min(90, formW-4))
		m.newDesc.SetHeight(max(6, msg.Height-12))
		m.editTitle.Width = min(80, formW-4)
		m.editDesc.SetWidth(min(90, formW-4))
		m.editDesc.SetHeight(max(6, msg.Height-10))

		m.eventsViewport.Width = max(30, msg.Width-10)
		m.eventsViewport.Height = max(8, msg.Height-8)
		m.workerLogViewport.Width = max(30, msg.Width-10)
		m.workerLogViewport.Height = max(8, msg.Height-8)
		return m, nil

	case tickMsg:
		// 每秒 tick 用于两件事：
		// 1) 自动刷新列表（只读 DB，不跑 watcher）
		// 2) 刷新中显示耗时，让用户有可观测性
		now := time.Now()
		cmds := []tea.Cmd{m.tickCmd()}

		// Tail 预览：选中变化时立即抓一次；否则每 10s 抓一次。
		if m.mode == modeTable {
			sel := m.selectedRow()
			if sel != m.lastSelected {
				m.lastSelected = sel
				m.tailRef = sel
				m.tailPreview = contracts.TailPreview{}
				m.tailErr = ""
				m.tailUpdatedAt = time.Time{}
				// selection 变化时，即使列表还没刷新也先安排一次抓取（失败会显示错误）。
				if sel.kind != rowNone && !m.tailInFlight && m.canCaptureTail(sel) {
					m.tailInFlight = true
					m.tailStartedAt = now
					cmds = append(cmds, m.tailCmd(sel))
				}
			} else if sel.kind != rowNone && sel == m.tailRef && !m.tailInFlight && m.canCaptureTail(sel) {
				if m.tailUpdatedAt.IsZero() || now.Sub(m.tailUpdatedAt) >= tailRefreshEvery {
					m.tailInFlight = true
					m.tailStartedAt = now
					cmds = append(cmds, m.tailCmd(sel))
				}
			}
		}

		// 自动刷新列表
		if m.mode == modeTable && !m.refreshInFlight {
			m.refreshInFlight = true
			m.refreshManual = false
			m.refreshTicketID = 0
			m.refreshStarted = now
			cmds = append(cmds, m.refreshCmd())
		}
		if m.mode == modeWorkerLog && m.workerLogTicketID != 0 && !m.workerLogInFlight {
			if m.workerLogLoadedAt.IsZero() || now.Sub(m.workerLogLoadedAt) >= workerLogRefreshEvery {
				m.workerLogInFlight = true
				cmds = append(cmds, m.loadWorkerLogCmd(m.workerLogTicketID))
			}
		}
		// 避免自动刷新导致“就绪 <-> 自动刷新中”快速切换引起视觉闪烁：
		// 只在手动刷新时显示耗时。
		if m.refreshInFlight && m.refreshManual && !m.refreshStarted.IsZero() {
			sec := time.Since(m.refreshStarted).Seconds()
			if m.refreshTicketID != 0 {
				m.status = fmt.Sprintf("刷新中 t%d... (%.0fs)", m.refreshTicketID, sec)
			} else {
				m.status = fmt.Sprintf("刷新中... (%.0fs)", sec)
			}
		}
		return m, tea.Batch(cmds...)

	case refreshedMsg:
		m.refreshInFlight = false
		m.refreshManual = false
		m.refreshTicketID = 0
		m.refreshStarted = time.Time{}
		if msg.Err != nil {
			m.errMsg = msg.Err.Error()
			return m, nil
		}
		m.errMsg = ""
		m.lastRefresh = time.Now()
		m.mergeItems = msg.MergeItems
		m.archivedTickets = msg.ArchivedTickets
		if msg.MergeErr != nil {
			m.mergeErr = msg.MergeErr.Error()
		} else {
			m.mergeErr = ""
		}
		if msg.ArchiveErr != nil {
			m.archiveErr = msg.ArchiveErr.Error()
		} else {
			m.archiveErr = ""
		}
		m.applyViews(msg.Views)
		if msg.Manual {
			d := msg.FinishedAt.Sub(msg.StartedAt)
			if d <= 0 {
				d = time.Since(m.lastRefresh)
			}
			if msg.TicketID != 0 {
				m.status = fmt.Sprintf("刷新完成 t%d (%.1fs)", msg.TicketID, d.Seconds())
			} else {
				m.status = fmt.Sprintf("刷新完成 (%.1fs)", d.Seconds())
			}
		} else if strings.HasPrefix(m.status, "自动刷新中") || m.status == "刷新已在进行中" || strings.TrimSpace(m.status) == "" {
			// 自动刷新结束：仅在状态栏当前处于“自动刷新中”时才回到就绪，
			// 避免覆盖用户操作产生的状态提示（也减少闪烁）。
			m.status = "就绪"
		}
		return m, nil

	case tailMsg:
		// 只显示“当前选中行”的 tail；过时结果忽略（但要清掉 inFlight，避免卡住）。
		m.tailInFlight = false
		if msg.Ref.kind == rowNone || msg.Ref != m.tailRef {
			return m, nil
		}
		if msg.Err != nil {
			m.tailErr = msg.Err.Error()
			m.tailUpdatedAt = msg.FinishedAt
			return m, nil
		}
		m.tailErr = ""
		m.tailPreview = msg.Preview
		m.tailUpdatedAt = msg.FinishedAt
		return m, nil

	case ticketStatusMsg:
		if msg.Err != nil {
			m.errMsg = msg.Err.Error()
			return m, nil
		}
		m.errMsg = ""
		m.status = fmt.Sprintf("t%d 状态已更新为 %s", msg.TicketID, string(msg.Status))
		m.refreshInFlight = true
		m.refreshManual = false
		m.refreshTicketID = 0
		m.refreshStarted = time.Now()
		return m, m.refreshCmd()

	case ticketPriorityMsg:
		if msg.Err != nil {
			m.errMsg = msg.Err.Error()
			return m, nil
		}
		m.errMsg = ""
		sign := "+"
		if msg.Delta < 0 {
			sign = ""
		}
		m.status = fmt.Sprintf("t%d 优先级 %s%d => %d", msg.TicketID, sign, msg.Delta, msg.Priority)
		m.refreshInFlight = true
		m.refreshManual = false
		m.refreshTicketID = 0
		m.refreshStarted = time.Now()
		return m, m.refreshCmd()

	case ticketReorderMsg:
		if msg.Err != nil {
			m.errMsg = msg.Err.Error()
			return m, nil
		}
		m.errMsg = ""
		action := "下移"
		if msg.Direction < 0 {
			action = "上移"
		}
		m.status = fmt.Sprintf("%s t%d（与 t%d 交换）", action, msg.TicketID, msg.TargetTicketID)
		m.refreshInFlight = true
		m.refreshManual = false
		m.refreshTicketID = 0
		m.refreshStarted = time.Now()
		return m, m.refreshCmd()

	case ticketTextMsg:
		if msg.Err != nil {
			m.errMsg = msg.Err.Error()
			return m, nil
		}
		m.errMsg = ""
		m.status = fmt.Sprintf("t%d 已保存", msg.TicketID)
		// 保存后回到列表并刷新
		m.mode = modeTable
		m.editTicketID = 0
		m.editFocus = 0
		m.refreshInFlight = true
		m.refreshManual = false
		m.refreshTicketID = 0
		m.refreshStarted = time.Now()
		return m, m.refreshCmd()

	case eventsLoadedMsg:
		m.eventsInFlight = false
		if msg.Err != nil {
			m.eventsErr = msg.Err.Error()
			m.status = fmt.Sprintf("事件加载失败：%v", msg.Err)
			return m, nil
		}
		m.eventsErr = ""
		m.eventsTicketID = msg.TicketID
		m.eventsWorkerID = msg.WorkerID
		m.eventsLoadedAt = time.Now()
		m.eventsViewport.SetContent(renderEvents(msg.Events))
		m.status = fmt.Sprintf("事件已加载：t%d w%d (%d 条)", msg.TicketID, msg.WorkerID, len(msg.Events))
		return m, nil

	case workerLogLoadedMsg:
		m.workerLogInFlight = false
		if msg.Err != nil {
			m.workerLogErr = msg.Err.Error()
			m.status = fmt.Sprintf("日志加载失败 t%d：%v", msg.TicketID, msg.Err)
			return m, nil
		}
		m.workerLogErr = ""
		m.workerLogTicketID = msg.TicketID
		m.workerLogWorkerID = msg.WorkerID
		m.workerLogLoadedAt = time.Now()
		m.workerLogLogPath = strings.TrimSpace(msg.Preview.LogPath)
		m.workerLogSource = strings.TrimSpace(msg.Preview.Source)
		m.workerLogViewport.SetContent(renderWorkerLog(msg.Preview))
		m.workerLogViewport.GotoBottom()
		m.status = fmt.Sprintf("日志已加载：t%d w%d (%d 行)", msg.TicketID, msg.WorkerID, len(msg.Preview.Lines))
		return m, nil

	case createdMsg:
		if msg.Err != nil {
			m.errMsg = msg.Err.Error()
			return m, nil
		}
		m.errMsg = ""
		m.mode = modeTable
		m.newFocus = 0
		m.titleInput.SetValue("")
		m.newDesc.SetValue("")
		m.newDesc.Blur()
		m.status = fmt.Sprintf("已创建 ticket #%d", msg.TicketID)
		m.refreshInFlight = true
		m.refreshManual = false
		m.refreshTicketID = 0
		m.refreshStarted = time.Now()
		return m, m.refreshCmd()

	case startedMsg:
		if msg.Err != nil {
			m.errMsg = msg.Err.Error()
			return m, nil
		}
		m.errMsg = ""
		m.status = fmt.Sprintf("已启动 ticket #%d", msg.TicketID)
		m.refreshInFlight = true
		m.refreshManual = false
		m.refreshTicketID = 0
		m.refreshStarted = time.Now()
		return m, m.refreshCmd()

	case stoppedMsg:
		if msg.Err != nil {
			m.errMsg = msg.Err.Error()
			return m, nil
		}
		m.errMsg = ""
		m.status = fmt.Sprintf("已停止 ticket #%d", msg.TicketID)
		m.refreshInFlight = true
		m.refreshManual = false
		m.refreshTicketID = 0
		m.refreshStarted = time.Now()
		return m, m.refreshCmd()

	case archivedMsg:
		if msg.Err != nil {
			m.errMsg = msg.Err.Error()
			return m, nil
		}
		m.errMsg = ""
		m.status = fmt.Sprintf("已归档 ticket #%d", msg.TicketID)
		m.refreshInFlight = true
		m.refreshManual = false
		m.refreshTicketID = 0
		m.refreshStarted = time.Now()
		return m, m.refreshCmd()

	case attachedMsg:
		if msg.Err != nil {
			m.errMsg = msg.Err.Error()
		} else {
			m.errMsg = ""
			if msg.TicketID == 0 {
				m.status = "已返回（manager）"
			} else {
				m.status = fmt.Sprintf("已返回（ticket #%d）", msg.TicketID)
			}
		}
		m.refreshInFlight = true
		m.refreshManual = false
		m.refreshTicketID = 0
		m.refreshStarted = time.Now()
		return m, m.refreshCmd()

	case dispatchedMsg:
		if msg.Err != nil {
			m.errMsg = msg.Err.Error()
			m.status = fmt.Sprintf("派发失败 t%d：%v", msg.TicketID, msg.Err)
		} else {
			m.errMsg = ""
			m.status = fmt.Sprintf("已派发 t%d", msg.TicketID)
		}
		m.refreshInFlight = true
		m.refreshManual = false
		m.refreshTicketID = 0
		m.refreshStarted = time.Now()
		return m, m.refreshCmd()

	case workerRunMsg:
		if msg.Err != nil {
			m.errMsg = msg.Err.Error()
			m.status = fmt.Sprintf("重新跑失败 t%d：%v", msg.TicketID, msg.Err)
		} else {
			m.errMsg = ""
			worker := "-"
			if msg.Receipt.WorkerID != 0 {
				worker = fmt.Sprintf("w%d", msg.Receipt.WorkerID)
			}
			m.status = fmt.Sprintf("重新跑已提交 t%d（worker=%s stages=待回传 next_action=待回传）", msg.TicketID, worker)
		}
		m.refreshInFlight = true
		m.refreshManual = false
		m.refreshTicketID = 0
		m.refreshStarted = time.Now()
		return m, m.refreshCmd()

	case interruptedMsg:
		if msg.Err != nil {
			m.errMsg = msg.Err.Error()
			m.status = fmt.Sprintf("中断失败 t%d：%v", msg.TicketID, msg.Err)
		} else {
			m.errMsg = ""
			m.status = fmt.Sprintf("已中断 t%d（Ctrl+C）", msg.TicketID)
		}
		m.refreshInFlight = true
		m.refreshManual = false
		m.refreshTicketID = 0
		m.refreshStarted = time.Now()
		return m, m.refreshCmd()

	case tea.KeyMsg:
		switch m.mode {
		case modeNewTicket:
			return m.updateNewTicket(msg)
		case modeEditTicket:
			return m.updateEditTicket(msg)
		case modeEvents:
			return m.updateEvents(msg)
		case modeWorkerLog:
			return m.updateWorkerLog(msg)
		default:
			return m.updateTable(msg)
		}
	}

	var cmd tea.Cmd
	m.table, cmd = m.table.Update(msg)
	return m, cmd
}

func (m model) View() string {
	w := m.width
	if w <= 0 {
		w = 100
	}
	innerW := max(20, w-4) // 扣掉 app padding（大致值）

	top := m.headerView(innerW)

	var main string
	switch m.mode {
	case modeNewTicket:
		main = m.newTicketView(innerW)
	case modeEditTicket:
		main = m.editTicketView(innerW)
	case modeEvents:
		main = m.eventsView(innerW)
	case modeWorkerLog:
		main = m.workerLogView(innerW)
	default:
		main = lipgloss.JoinVertical(lipgloss.Left,
			m.tablePanelView(innerW),
			"",
			m.inspectorView(innerW),
		)
	}

	footer := m.footerView(innerW)

	content := lipgloss.JoinVertical(lipgloss.Left, top, main, footer)
	return appStyle().Render(content) + "\n"
}

func (m model) headerView(width int) string {
	name := lipgloss.NewStyle().Bold(true).Render("dalek")
	repo := trimCell(m.p.RepoRoot(), max(10, width-20))
	meta := faint("repo: " + repo)
	right := faint("runtime: process")
	left := name + "  " + meta
	if lipgloss.Width(left)+1+lipgloss.Width(right) > width {
		return headerStyle().Width(width).Render(left)
	}
	space := strings.Repeat(" ", max(0, width-lipgloss.Width(left)-lipgloss.Width(right)))
	return headerStyle().Width(width).Render(left + space + right)
}

func (m model) footerView(width int) string {
	left := m.helpForMode() + "  |  "
	if m.errMsg != "" {
		left = left + lipgloss.NewStyle().Foreground(cDanger).Bold(true).Render("错误: "+m.errMsg)
	} else {
		left = left + m.status
	}
	if !m.lastRefresh.IsZero() {
		left = left + faint("  |  刷新: "+m.lastRefresh.Format("15:04:05"))
	}

	if lipgloss.Width(left) > width {
		left = cutANSI(left, width)
	}
	return footerStyle().Width(width).Render(left)
}

func (m model) helpForMode() string {
	switch m.mode {
	case modeNewTicket:
		return "Enter 保存  Esc 取消"
	case modeEditTicket:
		return "Tab 切换  Ctrl+S 保存  Esc 返回  Ctrl+C 退出"
	case modeEvents:
		return "↑↓ 滚动  r 刷新  Esc 返回  Ctrl+C 退出"
	case modeWorkerLog:
		return "↑↓ 滚动  r 刷新  Esc 返回  Ctrl+C 退出"
	default:
		return m.helpMsg
	}
}

func (m model) tablePanelView(width int) string {
	head := panelTitle(fmt.Sprintf("Tickets (%d)", m.ticketCount()))
	sub := faint("↑↓ 选择  |  Enter tmux  |  g 管理员  |  r 重新跑")
	left := head + "  " + sub

	content := left + "\n" + colorizePartitionColumn(m.table.View())
	return panelStyle().Width(width).Render(content)
}

func (m *model) applyViews(views []app.TicketView) {
	extraRows := len(m.mergeItems) + 6
	if m.showArchiveRows {
		extraRows += len(m.archivedTickets)
	}
	rows := make([]table.Row, 0, len(views)+extraRows)
	refs := make([]rowRef, 0, len(rows))
	viewsByID := make(map[uint]app.TicketView, len(views))
	ticketsByID := make(map[uint]contracts.Ticket, len(views)+len(m.archivedTickets))
	keep := m.selectedRow()

	running := make([]app.TicketView, 0, len(views))
	waiting := make([]app.TicketView, 0, len(views))
	backlog := make([]app.TicketView, 0, len(views))
	done := make([]app.TicketView, 0, len(views))
	for _, v := range views {
		viewsByID[v.Ticket.ID] = v
		ticketsByID[v.Ticket.ID] = v.Ticket
		switch {
		case v.DerivedStatus == contracts.TicketDone:
			done = append(done, v)
		case v.RuntimeNeedsUser || v.DerivedStatus == contracts.TicketBlocked || v.DerivedStatus == contracts.TicketQueued:
			waiting = append(waiting, v)
		case v.DerivedStatus == contracts.TicketActive:
			running = append(running, v)
		default:
			backlog = append(backlog, v)
		}
	}

	rows = append(rows, table.Row{
		partitionCell("manager"),
		trimCell("mgr", m.tableLayout.id),
		"-",
		trimCell("manager", m.tableLayout.status),
		trimCell("就绪", m.tableLayout.runtime),
		trimCell("项目管理员", m.tableLayout.title),
		trimCell("-", m.tableLayout.output),
	})
	refs = append(refs, rowRef{kind: rowManager, section: "manager"})

	addTicketRows := func(sectionKey string, list []app.TicketView) {
		for _, v := range list {
			t := v.Ticket
			a := v.LatestWorker

			outputRef := "-"
			if a != nil {
				if strings.TrimSpace(a.LogPath) != "" {
					outputRef = filepath.Base(strings.TrimSpace(a.LogPath))
				}
			}

			status := "待办"
			switch v.DerivedStatus {
			case contracts.TicketDone:
				status = "完成"
			case contracts.TicketBlocked:
				status = "阻塞"
			case contracts.TicketQueued:
				status = "排队"
			case contracts.TicketActive:
				status = "进行中"
			default:
				status = "待办"
			}

			runtime := formatRuntimeState(v)

			rows = append(rows, table.Row{
				partitionCell(sectionKey),
				trimCell(strconv.Itoa(int(t.ID)), m.tableLayout.id),
				trimCell(strconv.Itoa(t.Priority), m.tableLayout.priority),
				trimCell(status, m.tableLayout.status),
				trimCell(runtime, m.tableLayout.runtime),
				trimCell(t.Title, m.tableLayout.title),
				trimCell(outputRef, m.tableLayout.output),
			})
			refs = append(refs, rowRef{kind: rowTicket, section: sectionKey, ticketID: t.ID})
		}
	}

	// 分区顺序：manager、running、wait、merge、backlog、done（archive 默认不展示）
	addTicketRows("running", running)
	addTicketRows("wait", waiting)

	mergeItems := m.activeMergeItems()
	for _, mi := range mergeItems {
		branch := trimCell(strings.TrimSpace(mi.Branch), m.tableLayout.output)
		if branch == "" {
			branch = "-"
		}
		rows = append(rows, table.Row{
			partitionCell("merge"),
			trimCell(fmt.Sprintf("m%d", mi.ID), m.tableLayout.id),
			"-",
			trimCell(strings.TrimSpace(string(mi.Status)), m.tableLayout.status),
			"-",
			trimCell(fmt.Sprintf("t%d merge item", mi.TicketID), m.tableLayout.title),
			branch,
		})
		refs = append(refs, rowRef{kind: rowMerge, section: "merge", mergeID: mi.ID, ticketID: mi.TicketID})
	}

	addTicketRows("backlog", backlog)
	addTicketRows("done", done)

	if m.showArchiveRows {
		archived := make([]contracts.Ticket, 0, len(m.archivedTickets))
		for _, t := range m.archivedTickets {
			if t.WorkflowStatus != contracts.TicketArchived {
				continue
			}
			archived = append(archived, t)
			ticketsByID[t.ID] = t
		}
		for _, t := range archived {
			rows = append(rows, table.Row{
				partitionCell("archive"),
				trimCell(strconv.Itoa(int(t.ID)), m.tableLayout.id),
				trimCell(strconv.Itoa(t.Priority), m.tableLayout.priority),
				trimCell("归档", m.tableLayout.status),
				"-",
				trimCell(t.Title, m.tableLayout.title),
				"-",
			})
			refs = append(refs, rowRef{kind: rowTicket, section: "archive", ticketID: t.ID})
		}
	}

	m.table.SetRows(rows)
	m.rowRefs = refs
	m.viewsByID = viewsByID
	m.ticketsByID = ticketsByID

	for i, r := range refs {
		if sameRowRef(r, keep) {
			m.table.SetCursor(i)
			return
		}
	}
	if idx := m.managerRowIndex(); idx >= 0 {
		m.table.SetCursor(idx)
		return
	}
	if len(refs) == 0 {
		m.table.SetCursor(0)
		return
	}
	cur := m.table.Cursor()
	if cur < 0 {
		m.table.SetCursor(0)
		return
	}
	if cur >= len(refs) {
		m.table.SetCursor(len(refs) - 1)
		return
	}
}

func sameRowRef(a, b rowRef) bool {
	return a.kind == b.kind &&
		a.section == b.section &&
		a.ticketID == b.ticketID &&
		a.mergeID == b.mergeID
}

func (m model) managerRowIndex() int {
	for i, r := range m.rowRefs {
		if r.kind == rowManager {
			return i
		}
	}
	return -1
}

func (m model) mergeItemByID(id uint) (contracts.MergeItem, bool) {
	for _, mi := range m.mergeItems {
		if mi.ID == id {
			return mi, true
		}
	}
	return contracts.MergeItem{}, false
}

func (m model) mergeInspectorLeftView(panelW int, mergeID uint) string {
	innerW := max(10, panelW-4)
	mi, ok := m.mergeItemByID(mergeID)
	if !ok {
		lines := []string{
			panelTitle(fmt.Sprintf("元信息  m%d", mergeID)),
			faint("merge item 不存在（可能已被更新）"),
		}
		lines = padBottom(lines, 6+tailShowLines)
		return strings.Join(lines, "\n")
	}
	approved := "-"
	if strings.TrimSpace(mi.ApprovedBy) != "" {
		approved = strings.TrimSpace(mi.ApprovedBy)
	}
	approvedAt := "-"
	if mi.ApprovedAt != nil {
		approvedAt = mi.ApprovedAt.Local().Format("01-02 15:04")
	}
	lines := []string{
		panelTitle(fmt.Sprintf("元信息  m%d", mi.ID)),
		badge("merge", cWarn) + " " + badge(strings.TrimSpace(string(mi.Status)), cNeutral),
		kvLine("ticket:", fmt.Sprintf("t%d", mi.TicketID), innerW),
		kvLine("worker:", fmt.Sprintf("w%d", mi.WorkerID), innerW),
		kvLine("branch:", oneLine(strings.TrimSpace(mi.Branch)), innerW),
		kvLine("approved:", approved+" @ "+approvedAt, innerW),
		kvLine("updated:", mi.UpdatedAt.Local().Format("01-02 15:04"), innerW),
	}
	if strings.TrimSpace(mi.ChecksJSON.String()) != "" {
		lines = append(lines, kvLine("checks:", oneLine(strings.TrimSpace(mi.ChecksJSON.String())), innerW))
	}
	lines = padBottom(lines, 6+tailShowLines)
	return strings.Join(lines, "\n")
}

func (m model) mergeInspectorRightView(panelW int, mergeID uint) string {
	innerW := max(10, panelW-4)
	mi, ok := m.mergeItemByID(mergeID)
	if !ok {
		lines := []string{
			panelTitle("merge detail"),
			faint("(merge item 不存在)"),
		}
		lines = padBottom(lines, 4+tailShowLines)
		return strings.Join(lines, "\n")
	}
	lines := []string{
		panelTitle("merge detail"),
		kvLine("merge id:", fmt.Sprintf("m%d", mi.ID), innerW),
		kvLine("status:", strings.TrimSpace(string(mi.Status)), innerW),
		kvLine("branch:", oneLine(strings.TrimSpace(mi.Branch)), innerW),
		faint("当前为 merge 队列条目；不提供 pane tail"),
	}
	lines = padBottom(lines, 4+tailShowLines)
	return strings.Join(lines, "\n")
}

func (m model) inspectorView(width int) string {
	if width <= 0 {
		width = 80
	}

	sel := m.selectedRow()
	if sel.kind == rowNone {
		content := panelTitle("检查器") + "\n" + faint("未选择（用 ↑↓ 选择一行）")
		return panelStyle().Width(width).Render(content)
	}

	// 宽屏：三栏（元信息 / PM事实观察 / worker_log）；窄屏：上下三块。
	if width < 126 {
		meta := panelStyle().Width(width).Render(m.inspectorLeftView(width))
		facts := panelStyle().Width(width).Render(m.inspectorMiddleView(width))
		logs := panelStyle().Width(width).Render(m.inspectorRightView(width))
		return lipgloss.JoinVertical(lipgloss.Left, meta, facts, logs)
	}

	metaW := max(36, (width*38)/100)
	factsW := max(32, (width*26)/100)
	logW := width - metaW - factsW - 2
	if logW < 34 {
		logW = 34
		if metaW > 40 {
			metaW = max(36, metaW-2)
		}
		factsW = max(32, width-metaW-logW-2)
	}

	meta := panelStyle().Width(metaW).Render(m.inspectorLeftView(metaW))
	facts := panelStyle().Width(factsW).Render(m.inspectorMiddleView(factsW))
	logs := panelStyle().Width(logW).Render(m.inspectorRightView(logW))
	return lipgloss.JoinHorizontal(lipgloss.Top, meta, " ", facts, " ", logs)
}

func (m model) inspectorLeftView(panelW int) string {
	innerW := max(10, panelW-4) // border(2) + padding(2)

	sel := m.selectedRow()
	switch sel.kind {
	case rowManager:
		return m.managerInspectorLeftView(panelW)
	case rowMerge:
		return m.mergeInspectorLeftView(panelW, sel.mergeID)
	}
	id := sel.ticketID
	v, ok := m.viewsByID[id]
	if !ok {
		if t, tok := m.ticketsByID[id]; tok {
			title := oneLine(strings.TrimSpace(t.Title))
			if title == "" {
				title = "-"
			}
			desc := oneLine(strings.TrimSpace(t.Description))
			if desc == "" {
				desc = "-"
			}
			status := "待办"
			status = formatTicketStatus(t.WorkflowStatus)
			lines := []string{
				panelTitle(fmt.Sprintf("元信息  t%d", id)),
				badge(status, cNeutral),
				kvLine("标题:", title, innerW),
				kvLine("描述:", desc, innerW),
				kvLine("状态:", string(t.WorkflowStatus), innerW),
				kvLine("更新:", t.UpdatedAt.Local().Format("01-02 15:04"), innerW),
				faint("该 ticket 当前不在活跃 worker 视图中"),
			}
			lines = padBottom(lines, 6+tailShowLines)
			return strings.Join(lines, "\n")
		}
		title := panelTitle(fmt.Sprintf("元信息  t%d", id))
		return title + "\n" + faint("详情尚未加载（等一下自动刷新）")
	}
	t := v.Ticket
	a := v.LatestWorker

	title := oneLine(strings.TrimSpace(t.Title))
	if title == "" {
		title = "-"
	}

	statusB := ticketStatusBadge(v.DerivedStatus)
	runtimeB := runtimeStatusBadge(v)
	processB := badge(m.dispatchProcessState(v), cInfo)

	runtimeRef := "-"
	worker := "-"
	lifecycle := "-"
	cmd := "-"
	mode := "-"
	summary := oneLine(strings.TrimSpace(v.RuntimeSummary))
	if summary == "" {
		summary = "-"
	}
	worktree := "-"
	if a != nil {
		worktree = strings.TrimSpace(a.WorktreePath)
	}
	if worktree == "" {
		worktree = "-"
	} else {
		worktree = trimLeft(worktree, 60)
	}

	if a != nil {
		if strings.TrimSpace(a.LogPath) != "" {
			runtimeRef = trimLeft(strings.TrimSpace(a.LogPath), 48)
		}
		worker = fmt.Sprintf("w%d", a.ID)
		lifecycle = string(a.Status)
		if strings.TrimSpace(a.RuntimePaneCommand) != "" {
			cmd = strings.TrimSpace(a.RuntimePaneCommand)
		}
		if strings.TrimSpace(a.RuntimePaneMode) != "" {
			mode = strings.TrimSpace(a.RuntimePaneMode)
		}
		if a.RuntimePaneInMode {
			mode = "in:" + mode
		}
		if strings.TrimSpace(mode) == "" {
			mode = "-"
		}
	}
	runID := "-"
	if v.TaskRunID != 0 {
		runID = fmt.Sprintf("r%d", v.TaskRunID)
	}
	semPhase := strings.TrimSpace(string(v.SemanticPhase))
	if semPhase == "" {
		semPhase = "-"
	}
	semNext := strings.TrimSpace(v.SemanticNextAction)
	if semNext == "" {
		semNext = "-"
	}
	semSummary := oneLine(strings.TrimSpace(v.SemanticSummary))
	if semSummary == "" {
		semSummary = "-"
	}
	eventType := strings.TrimSpace(v.LastEventType)
	if eventType == "" {
		eventType = "-"
	}
	runtimeState := formatRuntimeState(v)
	sessionState := formatSessionState(v)

	lines := []string{
		panelTitle(fmt.Sprintf("元信息  t%d · dispatch/running", t.ID)),
		statusB + " " + runtimeB + " " + processB,
		kvLine("ticket:", title, innerW),
		kvLine("流程:", m.dispatchProcessState(v), innerW),
		kvLine("run:", runID+"  runtime="+runtimeState, innerW),
		kvLine("phase:", semPhase+"  next="+semNext, innerW),
		kvLine("semantic:", semSummary, innerW),
		kvLine("last_event:", eventType+"  @ "+timeAndAge(v.LastEventAt), innerW),
		kvLine("runtime_observed:", timeAndAge(v.RuntimeObservedAt)+"  result="+summary, innerW),
		kvLine("worker:", worker+"  "+lifecycle+"  "+runtimeRef+"  "+sessionState, innerW),
		kvLine("runtime/worktree:", "cmd="+cmd+"  mode="+mode+"  "+worktree, innerW),
	}
	lines = padBottom(lines, 4+tailShowLines)
	return strings.Join(lines, "\n")
}

func (m model) inspectorMiddleView(panelW int) string {
	innerW := max(10, panelW-4) // border(2) + padding(2)

	sel := m.selectedRow()
	switch sel.kind {
	case rowManager:
		return m.managerInspectorMiddleView(panelW)
	case rowMerge:
		return m.mergeInspectorMiddleView(panelW, sel.mergeID)
	}

	id := sel.ticketID
	v, ok := m.viewsByID[id]
	if !ok {
		if t, tok := m.ticketsByID[id]; tok {
			lines := []string{
				panelTitle(fmt.Sprintf("PM事实观察  t%d", id)),
				badge(formatTicketStatus(t.WorkflowStatus), cNeutral),
				kvLine("来源:", "event_note", innerW),
				kvLine("状态:", string(t.WorkflowStatus), innerW),
				faint("该 ticket 暂无活跃运行事实（等待刷新或尚未派发）"),
			}
			lines = padBottom(lines, 4+tailShowLines)
			return strings.Join(lines, "\n")
		}
		return panelTitle(fmt.Sprintf("PM事实观察  t%d", id)) + "\n" + faint("事实尚未加载（等一下自动刷新）")
	}

	eventType := strings.TrimSpace(v.LastEventType)
	if eventType == "" {
		eventType = "-"
	}
	eventNote := oneLine(strings.TrimSpace(v.LastEventNote))
	if eventNote == "" {
		eventNote = "-"
	}
	semNext := strings.TrimSpace(v.SemanticNextAction)
	if semNext == "" {
		semNext = "-"
	}
	semSummary := oneLine(strings.TrimSpace(v.SemanticSummary))
	if semSummary == "" {
		semSummary = "-"
	}
	runtimeSummary := oneLine(strings.TrimSpace(v.RuntimeSummary))
	if runtimeSummary == "" {
		runtimeSummary = "-"
	}

	lines := []string{
		panelTitle(fmt.Sprintf("PM事实观察  t%d", v.Ticket.ID)),
		badge("event_note", cInfo) + " " + badge(eventType, cNeutral),
		kvLine("流程:", m.dispatchProcessState(v), innerW),
		kvLine("last_event:", eventType+"  @ "+timeAndAge(v.LastEventAt), innerW),
		kvLine("next_action:", semNext, innerW),
		kvLine("semantic:", semSummary, innerW),
		kvLine("runtime:", runtimeSummary, innerW),
		kvLine("event_note:", eventNote, innerW),
	}
	lines = padBottom(lines, 4+tailShowLines)
	return strings.Join(lines, "\n")
}

func (m model) mergeInspectorMiddleView(panelW int, mergeID uint) string {
	innerW := max(10, panelW-4)
	mi, ok := m.mergeItemByID(mergeID)
	if !ok {
		lines := []string{
			panelTitle(fmt.Sprintf("PM事实观察  m%d", mergeID)),
			faint("merge item 不存在（可能已被更新）"),
		}
		lines = padBottom(lines, 4+tailShowLines)
		return strings.Join(lines, "\n")
	}
	checks := oneLine(strings.TrimSpace(mi.ChecksJSON.String()))
	if checks == "" || checks == "{}" {
		checks = "-"
	}
	lines := []string{
		panelTitle(fmt.Sprintf("PM事实观察  m%d", mi.ID)),
		badge("merge", cWarn) + " " + badge(strings.TrimSpace(string(mi.Status)), cNeutral),
		kvLine("ticket:", fmt.Sprintf("t%d", mi.TicketID), innerW),
		kvLine("facts:", "merge 没有 worker event_note，观察 checks_json", innerW),
		kvLine("checks:", checks, innerW),
	}
	lines = padBottom(lines, 4+tailShowLines)
	return strings.Join(lines, "\n")
}

func (m model) managerInspectorMiddleView(panelW int) string {
	innerW := max(10, panelW-4)

	issues := collectPendingIssues(m.orderedViews(), 6)
	lines := []string{
		panelTitle("PM事实观察  manager"),
		badge("event_note", cInfo),
		kvLine("来源:", "ticket 运行态事件聚合", innerW),
	}
	if m.lastRefresh.IsZero() {
		lines = append(lines, kvLine("刷新:", "-", innerW))
	} else {
		lines = append(lines, kvLine("刷新:", m.lastRefresh.Format("15:04:05"), innerW))
	}
	if len(issues) == 0 {
		lines = append(lines, faint("暂无待处理事实"))
	} else {
		for _, it := range issues {
			lines = append(lines, cutANSI(" - "+oneLine(it), innerW))
		}
	}
	lines = padBottom(lines, 4+tailShowLines)
	return strings.Join(lines, "\n")
}

func (m model) managerInspectorLeftView(panelW int) string {
	innerW := max(10, panelW-4) // border(2) + padding(2)

	repo := "-"
	cwd := "-"
	stateDir := "-"
	if m.p != nil {
		if r := strings.TrimSpace(m.p.RepoRoot()); r != "" {
			repo = trimLeft(r, 60)
			cwd = trimLeft(r, 60)
		}
		if d := strings.TrimSpace(m.p.ProjectDir()); d != "" {
			stateDir = trimLeft(d, 60)
		}
	}

	views := m.orderedViews()
	summary := summarizeQueueStatus(views)
	issueTotal := len(collectPendingIssues(views, len(views)+1))
	issuesPreview := collectPendingIssues(views, 2)
	mergeItems := m.activeMergeItems()
	mergeSummary := summarizeMergeQueue(mergeItems)

	lines := []string{
		panelTitle("元信息  manager"),
		badge("manager", cInfo) + " " + badge("runtime", cNeutral),
		kvLine("repo:", repo, innerW),
		kvLine("state:", stateDir, innerW),
		kvLine("交互:", "manager attach 已移除", innerW),
		kvLine("队列:", fmt.Sprintf("待办%d 排队%d 阻塞%d 运行%d 完成%d", summary.Backlog, summary.Queued, summary.Blocked, summary.Running, summary.Done), innerW),
		kvLine("merge:", fmt.Sprintf("总%d proposed%d checks%d ready%d approved%d blocked%d", mergeSummary.Total, mergeSummary.Proposed, mergeSummary.ChecksRunning, mergeSummary.Ready, mergeSummary.Approved, mergeSummary.Blocked), innerW),
		kvLine("待处理:", fmt.Sprintf("%d 项", issueTotal), innerW),
		kvLine("提示:", "使用 manager tick/status 查看状态", innerW),
	}
	if strings.TrimSpace(cwd) != "" && cwd != repo {
		lines = append(lines, kvLine("cwd:", cwd, innerW))
	}
	if strings.TrimSpace(m.mergeErr) != "" {
		lines = append(lines, cutANSI(" merge加载失败: "+oneLine(m.mergeErr), innerW))
	}
	if strings.TrimSpace(m.archiveErr) != "" {
		lines = append(lines, cutANSI(" archive加载失败: "+oneLine(m.archiveErr), innerW))
	}
	for _, it := range issuesPreview {
		lines = append(lines, cutANSI(" - "+oneLine(it), innerW))
	}
	if issueTotal > len(issuesPreview) {
		lines = append(lines, cutANSI(fmt.Sprintf(" ... 还有 %d 项", issueTotal-len(issuesPreview)), innerW))
	}
	for _, mi := range mergeItems {
		if len(lines) >= 14 {
			break
		}
		branch := oneLine(strings.TrimSpace(mi.Branch))
		if len(branch) > 28 {
			branch = branch[:28] + "..."
		}
		lines = append(lines, cutANSI(fmt.Sprintf(" - m%d t%d %s %s", mi.ID, mi.TicketID, strings.TrimSpace(string(mi.Status)), branch), innerW))
	}
	lines = padBottom(lines, 6+tailShowLines)
	return strings.Join(lines, "\n")
}

func (m model) inspectorRightView(panelW int) string {
	innerW := max(10, panelW-4) // border(2) + padding(2)

	sel := m.selectedRow()
	switch sel.kind {
	case rowManager:
		return m.managerInspectorRightView(panelW)
	case rowMerge:
		return m.mergeInspectorRightView(panelW, sel.mergeID)
	}
	id := sel.ticketID
	view, viewOK := m.viewsByID[id]

	// 输出元信息：优先 tailPreview（最贴近实时输出）。
	source := "-"
	logPath := "-"
	isTail := m.tailRef.kind == rowTicket && m.tailRef.ticketID == id
	if viewOK && view.LatestWorker != nil {
		if strings.TrimSpace(view.LatestWorker.LogPath) != "" {
			source = "worker_log"
		}
		if s := strings.TrimSpace(view.LatestWorker.LogPath); s != "" {
			logPath = trimLeft(s, 48)
		}
	}
	if isTail {
		if s := strings.TrimSpace(m.tailPreview.Source); s != "" {
			source = s
		}
		if s := strings.TrimSpace(m.tailPreview.LogPath); s != "" {
			logPath = trimLeft(s, 48)
		}
	}

	metaTime := "-"
	metaAge := "-"
	if !m.tailUpdatedAt.IsZero() {
		metaTime = m.tailUpdatedAt.Format("15:04:05")
		metaAge = shortDuration(time.Since(m.tailUpdatedAt))
	}

	state := badge("就绪", cOk)
	if m.tailInFlight && isTail && !m.tailStartedAt.IsZero() {
		state = badge(fmt.Sprintf("抓取中 %.0fs", time.Since(m.tailStartedAt).Seconds()), cInfo)
	} else if strings.TrimSpace(m.tailErr) != "" && isTail {
		state = badge("抓取失败", cDanger)
	} else if viewOK && view.LatestWorker != nil && strings.TrimSpace(view.LatestWorker.LogPath) != "" && !view.SessionAlive {
		state = badge("已停止", cNeutral)
	}

	head := panelTitle("worker_log观察窗") + "  " + state
	meta := kvLine("更新:", metaTime+"  ("+metaAge+"前)", innerW)
	where := kvLine("来源:", "source="+source+"  log="+logPath, innerW)

	tailLines := []string{}
	if strings.TrimSpace(m.tailErr) != "" && isTail {
		tailLines = []string{"抓取失败: " + m.tailErr}
	} else if isTail && len(m.tailPreview.Lines) > 0 {
		tailLines = m.tailPreview.Lines
	} else if isTail && m.tailInFlight {
		tailLines = []string{"(抓取中...)"}
	} else {
		switch {
		case !viewOK:
			tailLines = []string{"(等待刷新...)"}
		case view.LatestWorker == nil:
			tailLines = []string{"(尚未启动)"}
		case strings.TrimSpace(view.LatestWorker.LogPath) == "":
			tailLines = []string{"(暂无可读日志)"}
		case !view.SessionAlive:
			tailLines = []string{"(进程已停止)"}
		case m.tailUpdatedAt.IsZero():
			tailLines = []string{"(等待抓取...)"}
		default:
			tailLines = []string{"(暂无输出)"}
		}
	}
	tailLines = tailTail(tailLines, ticketTailLines)
	for i := range tailLines {
		line := cutANSI(tailLines[i], innerW)
		if i == len(tailLines)-1 {
			tailLines[i] = lipgloss.NewStyle().Foreground(cTitle).Bold(true).Render(line)
		} else {
			tailLines[i] = lipgloss.NewStyle().Foreground(cFaint).Render(line)
		}
	}

	lines := []string{head, meta, where}
	lines = append(lines, tailLines...)
	lines = padBottom(lines, 4+ticketTailLines)
	return strings.Join(lines, "\n")
}

func (m model) managerInspectorRightView(panelW int) string {
	innerW := max(10, panelW-4) // border(2) + padding(2)

	head := panelTitle("输出尾部") + "  " + badge("已移除", cNeutral)
	meta := kvLine("说明:", "manager 交互入口已移除", innerW)
	where := kvLine("建议:", "如需观测请使用 manager status / manager tick", innerW)
	tailLines := tailTail([]string{
		"manager 交互入口已简化为无 session 模式。",
		"worker 与调度状态请看上方队列与 ticket 行。",
	}, tailShowLines)
	for i := range tailLines {
		line := cutANSI(tailLines[i], innerW)
		switch {
		case i >= len(tailLines)-2:
			tailLines[i] = lipgloss.NewStyle().Foreground(cTitle).Bold(i == len(tailLines)-1).Render(line)
		default:
			tailLines[i] = lipgloss.NewStyle().Foreground(cFaint).Render(line)
		}
	}

	lines := []string{head, meta, where}
	lines = append(lines, tailLines...)
	lines = padBottom(lines, 4+tailShowLines)
	return strings.Join(lines, "\n")
}
