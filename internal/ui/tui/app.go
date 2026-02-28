package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"dalek/internal/app"
	"dalek/internal/contracts"
	"dalek/internal/repo"
)

type appPage int

const (
	pageProjects appPage = iota
	pageMonitor
	pageNotebook
)

type projectsMode int

const (
	projectsModeList projectsMode = iota
	projectsModeAdd
	projectsModeRegisterConfirm
	projectsModeRegisterWarn
)

type appModel struct {
	ctx    context.Context
	cancel context.CancelFunc

	home *app.Home
	wd   string

	page   appPage
	width  int
	height int

	// page: projects
	projectsTable    table.Model
	projectsRowNames []string
	projectsMode     projectsMode
	addPath          textinput.Model
	lastProjectsErr  string
	registerRepoRoot string
	registerWarn     string

	// current project
	currentProjectName string
	p                  *app.Project

	// monitor page（复用现有页面）
	monitor    model
	hasMonitor bool

	// notebook page
	notebook    notebookModel
	hasNotebook bool
}

type projectsLoadedMsg struct {
	Projects []app.RegisteredProject
	Err      error
}

type projectOpenedMsg struct {
	Name string
	P    *app.Project
	Err  error
}

type queueStatusSummary struct {
	Total   int
	Backlog int
	Queued  int
	Blocked int
	Running int
	Done    int
}

type startupProbeMsg struct {
	RepoRoot       string
	RegisteredName string
	IsGit          bool
	Interactive    bool
	Err            error
}

type gotoNotebookMsg struct{}

type notebookClosedMsg struct{}

func newAppModel(h *app.Home, initialProject string) appModel {
	cols := []table.Column{
		{Title: "项目", Width: 26},
		{Title: "Repo", Width: 70},
	}
	t := table.New(
		table.WithColumns(cols),
		table.WithFocused(true),
		table.WithHeight(12),
	)
	t.SetStyles(tableStyles())

	ti := textinput.New()
	ti.Placeholder = "/path/to/git/repo"
	ti.CharLimit = 4096
	ti.Width = 80

	ctx, cancel := context.WithCancel(context.Background())
	m := appModel{
		ctx:                ctx,
		cancel:             cancel,
		home:               h,
		wd:                 detectWorkingDir(),
		page:               pageProjects,
		projectsTable:      t,
		projectsRowNames:   nil,
		projectsMode:       projectsModeList,
		addPath:            ti,
		lastProjectsErr:    "",
		registerRepoRoot:   "",
		registerWarn:       "",
		currentProjectName: strings.TrimSpace(initialProject),
		p:                  nil,
		monitor:            model{},
		hasMonitor:         false,
		notebook:           notebookModel{},
		hasNotebook:        false,
	}
	return m
}

func detectWorkingDir() string {
	wd, err := os.Getwd()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(wd)
}

func (m appModel) Init() tea.Cmd {
	cmds := []tea.Cmd{m.loadProjectsCmd()}
	if strings.TrimSpace(m.currentProjectName) != "" {
		// 直接进入指定项目
		cmds = append(cmds, m.openProjectCmd(m.currentProjectName))
	} else {
		// 未显式指定项目时，启动即探测当前目录（若是未注册 git repo，直接给注册弹窗）。
		cmds = append(cmds, m.probeStartupDirCmd(false))
	}
	return tea.Batch(cmds...)
}

func (m appModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.projectsTable.SetHeight(max(6, msg.Height-8))
		m.addPath.Width = min(110, max(40, msg.Width-10))
		// monitor 子模型也需要 window size
		if m.page == pageMonitor && m.p != nil {
			mon, cmd := m.monitorUpdate(msg)
			m = mon
			return m, cmd
		}
		if m.page == pageNotebook && m.p != nil {
			nb, cmd := m.notebookUpdate(msg)
			m = nb
			return m, cmd
		}
		return m, nil

	case projectsLoadedMsg:
		if msg.Err != nil {
			m.lastProjectsErr = msg.Err.Error()
			return m, nil
		}
		m.lastProjectsErr = ""
		m.setProjectsRows(msg.Projects)
		return m, nil

	case projectOpenedMsg:
		if msg.Err != nil {
			// 失败时回到项目列表页
			m.page = pageProjects
			m.projectsMode = projectsModeList
			m.p = nil
			m.hasMonitor = false
			m.hasNotebook = false
			m.lastProjectsErr = msg.Err.Error()
			return m, m.loadProjectsCmd()
		}

		m.currentProjectName = msg.Name
		m.p = msg.P
		m.lastProjectsErr = ""

		mon := newModel(m.p, m.home, m.currentProjectName)
		return m.setMonitor(mon)

	case gotoNotebookMsg:
		if m.p == nil {
			return m, nil
		}
		nb := newNotebookModel(m.p, m.currentProjectName)
		return m.setNotebook(nb)

	case notebookClosedMsg:
		if !m.hasMonitor {
			return m, nil
		}
		m.page = pageMonitor
		m.hasNotebook = false
		return m, nil

	case startupProbeMsg:
		if msg.Err != nil {
			if !msg.Interactive {
				// 启动时的被动探测：非 git 目录不弹打扰性警告。
				return m, nil
			}
			m.projectsMode = projectsModeRegisterWarn
			m.registerRepoRoot = ""
			m.registerWarn = strings.TrimSpace(msg.Err.Error())
			if m.registerWarn == "" {
				m.registerWarn = "当前目录不可注册"
			}
			return m, nil
		}
		if strings.TrimSpace(msg.RegisteredName) != "" {
			m.currentProjectName = strings.TrimSpace(msg.RegisteredName)
			m.projectsMode = projectsModeList
			m.registerRepoRoot = ""
			m.registerWarn = ""
			return m, m.openProjectCmd(m.currentProjectName)
		}
		if !msg.IsGit {
			if !msg.Interactive {
				return m, nil
			}
			m.projectsMode = projectsModeRegisterWarn
			m.registerRepoRoot = ""
			m.registerWarn = "当前目录不是 git 项目，无法注册"
			return m, nil
		}
		m.projectsMode = projectsModeRegisterConfirm
		m.registerRepoRoot = strings.TrimSpace(msg.RepoRoot)
		m.registerWarn = ""
		return m, nil
	}

	// Key handling
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch m.page {
		case pageProjects:
			return m.updateProjectsKeys(msg)
		case pageMonitor:
			// q 退出（monitor 自己也会 quit，但这里先做一次 cancel/stop）
			if msg.String() == "q" || msg.String() == "ctrl+c" {
				m.cancel()
				return m, tea.Quit
			}

			// Esc：当 monitor 处于“表格主界面”时返回 projects；否则交给 monitor 自己处理（比如退出编辑）。
			if msg.String() == "esc" && m.hasMonitor && m.monitor.mode == modeTable {
				m.page = pageProjects
				m.p = nil
				m.hasMonitor = false
				return m, m.loadProjectsCmd()
			}

			mon, cmd := m.monitorUpdate(msg)
			m = mon
			return m, cmd
		case pageNotebook:
			nb, cmd := m.notebookUpdate(msg)
			m = nb
			return m, cmd
		}
	}

	// monitor 子模型会产出很多自定义消息（refreshedMsg/startedMsg/...），
	// 这里需要在“当前 page==monitor”时把它们透传给子模型，否则列表永远不会更新。
	if m.page == pageMonitor {
		mon, cmd := m.monitorUpdate(msg)
		m = mon
		return m, cmd
	}
	if m.page == pageNotebook {
		nb, cmd := m.notebookUpdate(msg)
		m = nb
		return m, cmd
	}

	return m, nil
}

func (m appModel) View() string {
	switch m.page {
	case pageProjects:
		return m.viewProjects()
	case pageMonitor:
		return m.viewMonitor()
	case pageNotebook:
		return m.viewNotebook()
	default:
		return "unknown page"
	}
}

// ---------- Projects Page ----------

func (m *appModel) setProjectsRows(ps []app.RegisteredProject) {
	rows := make([]table.Row, 0, len(ps))
	names := make([]string, 0, len(ps))
	for _, p := range ps {
		name := strings.TrimSpace(p.Name)
		repo := strings.TrimSpace(p.RepoRoot)
		rows = append(rows, table.Row{name, repo})
		names = append(names, name)
	}
	m.projectsRowNames = names
	m.projectsTable.SetRows(rows)
}

func (m appModel) loadProjectsCmd() tea.Cmd {
	return func() tea.Msg {
		ps, err := m.home.ListProjects()
		return projectsLoadedMsg{Projects: ps, Err: err}
	}
}

func (m appModel) openProjectCmd(name string) tea.Cmd {
	name = strings.TrimSpace(name)
	return func() tea.Msg {
		p, err := m.home.OpenProjectByName(name)
		return projectOpenedMsg{Name: name, P: p, Err: err}
	}
}

func (m appModel) addProjectCmd(path string) tea.Cmd {
	path = strings.TrimSpace(path)
	return func() tea.Msg {
		cfg := app.ProjectConfig{RefreshIntervalMS: 1000}
		p, err := m.home.InitProjectFromDir(path, "", cfg)
		if err != nil {
			return projectOpenedMsg{Name: "", P: nil, Err: err}
		}
		// 添加成功后仍停留在 projects 页，只刷新列表
		_ = p
		return projectsLoadedMsg{Projects: mustListProjects(m.home), Err: nil}
	}
}

func (m appModel) probeStartupDirCmd(interactive bool) tea.Cmd {
	startDir := strings.TrimSpace(m.wd)
	return func() tea.Msg {
		if startDir == "" {
			return startupProbeMsg{IsGit: false, Interactive: interactive, Err: fmt.Errorf("无法获取当前目录")}
		}
		repoRoot, err := repo.FindRepoRoot(startDir)
		if err != nil {
			return startupProbeMsg{IsGit: false, Interactive: interactive, Err: fmt.Errorf("当前目录不是 git 项目，无法注册")}
		}
		rp, err := m.home.FindProjectByRepoRoot(repoRoot)
		if err == nil {
			return startupProbeMsg{
				RepoRoot:       strings.TrimSpace(repoRoot),
				RegisteredName: strings.TrimSpace(rp.Name),
				IsGit:          true,
				Interactive:    interactive,
			}
		}
		if errors.Is(err, app.ErrNotInitialized) {
			return startupProbeMsg{
				RepoRoot:    strings.TrimSpace(repoRoot),
				IsGit:       true,
				Interactive: interactive,
			}
		}
		return startupProbeMsg{
			RepoRoot:    strings.TrimSpace(repoRoot),
			IsGit:       true,
			Interactive: interactive,
			Err:         err,
		}
	}
}

func (m appModel) registerAndOpenCmd(repoRoot string) tea.Cmd {
	repoRoot = strings.TrimSpace(repoRoot)
	return func() tea.Msg {
		cfg := app.ProjectConfig{RefreshIntervalMS: 1000}
		p, err := m.home.InitProjectFromDir(repoRoot, "", cfg)
		if err != nil {
			return projectOpenedMsg{Name: "", P: nil, Err: err}
		}
		return projectOpenedMsg{Name: strings.TrimSpace(p.Name()), P: p, Err: nil}
	}
}

func mustListProjects(h *app.Home) []app.RegisteredProject {
	ps, err := h.ListProjects()
	if err != nil {
		return []app.RegisteredProject{}
	}
	return ps
}

func summarizeQueueStatus(views []app.TicketView) queueStatusSummary {
	out := queueStatusSummary{Total: len(views)}
	for _, v := range views {
		switch v.DerivedStatus {
		case contracts.TicketQueued:
			out.Queued++
		case contracts.TicketActive:
			out.Running++
		case contracts.TicketBlocked:
			out.Blocked++
		case contracts.TicketDone:
			out.Done++
		default:
			out.Backlog++
		}
	}
	return out
}

func collectPendingIssues(views []app.TicketView, limit int) []string {
	if limit <= 0 {
		limit = 6
	}
	issues := make([]string, 0, limit)
	seen := make(map[uint]struct{}, limit)
	addIssue := func(v app.TicketView, reason string) bool {
		if len(issues) >= limit {
			return false
		}
		if _, ok := seen[v.Ticket.ID]; ok {
			return true
		}
		seen[v.Ticket.ID] = struct{}{}
		detail := strings.TrimSpace(v.RuntimeSummary)
		if detail == "" {
			detail = strings.TrimSpace(v.Ticket.Title)
		}
		if detail != "" {
			issues = append(issues, fmt.Sprintf("t%d %s：%s", v.Ticket.ID, reason, oneLine(detail)))
		} else {
			issues = append(issues, fmt.Sprintf("t%d %s", v.Ticket.ID, reason))
		}
		return true
	}

	for _, v := range views {
		if !v.RuntimeNeedsUser {
			continue
		}
		if !addIssue(v, "等待输入") {
			return issues
		}
	}
	for _, v := range views {
		if v.RuntimeHealthState != contracts.TaskHealthStalled {
			continue
		}
		if !addIssue(v, "运行错误") {
			return issues
		}
	}
	for _, v := range views {
		if v.DerivedStatus != contracts.TicketBlocked {
			continue
		}
		if !addIssue(v, "状态阻塞") {
			return issues
		}
	}
	return issues
}

func (m appModel) updateProjectsKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch m.projectsMode {
	case projectsModeList:
		switch msg.String() {
		case "q", "ctrl+c":
			m.cancel()
			return m, tea.Quit
		case "r":
			return m, m.loadProjectsCmd()
		case "n":
			m.projectsMode = projectsModeAdd
			m.addPath.SetValue("")
			m.addPath.CursorEnd()
			m.addPath.Focus()
			return m, nil
		case "enter":
			idx := m.projectsTable.Cursor()
			if idx < 0 || idx >= len(m.projectsRowNames) || len(m.projectsRowNames) == 0 {
				// 空列表/无选择：探测当前目录是否可注册。
				return m, m.probeStartupDirCmd(true)
			}
			name := strings.TrimSpace(m.projectsRowNames[idx])
			if name == "" {
				return m, m.probeStartupDirCmd(true)
			}
			m.currentProjectName = name
			return m, m.openProjectCmd(name)
		}
		// 其他按键交给 table（上下移动等）
		var cmd tea.Cmd
		m.projectsTable, cmd = m.projectsTable.Update(msg)
		return m, cmd
	case projectsModeAdd:
		switch msg.String() {
		case "esc":
			m.projectsMode = projectsModeList
			m.addPath.Blur()
			return m, nil
		case "enter":
			path := strings.TrimSpace(m.addPath.Value())
			if path == "" {
				m.projectsMode = projectsModeList
				m.addPath.Blur()
				return m, nil
			}
			m.projectsMode = projectsModeList
			m.addPath.Blur()
			return m, tea.Batch(m.addProjectCmd(path), m.loadProjectsCmd())
		}
		// 其他按键交给 input（输入路径）
		var cmd tea.Cmd
		m.addPath, cmd = m.addPath.Update(msg)
		return m, cmd
	case projectsModeRegisterConfirm:
		switch msg.String() {
		case "q", "ctrl+c":
			m.cancel()
			return m, tea.Quit
		case "esc", "n":
			m.projectsMode = projectsModeList
			m.registerRepoRoot = ""
			m.registerWarn = ""
			return m, nil
		case "enter", "r", "y":
			if strings.TrimSpace(m.registerRepoRoot) == "" {
				m.projectsMode = projectsModeRegisterWarn
				m.registerWarn = "未找到可注册的 repo 路径"
				return m, nil
			}
			return m, m.registerAndOpenCmd(m.registerRepoRoot)
		}
		return m, nil
	case projectsModeRegisterWarn:
		switch msg.String() {
		case "q", "ctrl+c":
			m.cancel()
			return m, tea.Quit
		case "enter", "esc":
			m.projectsMode = projectsModeList
			m.registerWarn = ""
			m.registerRepoRoot = ""
			return m, nil
		}
		return m, nil
	}
	return m, nil
}

func (m appModel) viewProjects() string {
	header := headerStyle().Render(fmt.Sprintf("Projects  |  n 添加  Enter 打开/探测当前目录  r 刷新  q 退出   (%s)", strings.TrimSpace(m.home.Root)))
	body := ""
	if m.projectsMode == projectsModeAdd {
		body = panelStyle().Render(panelTitle("添加项目（输入 repo 路径）") + "\n\n" + m.addPath.View() + "\n\n" + faint("Enter 确认  Esc 取消"))
	} else {
		body = panelStyle().Render(panelTitle(fmt.Sprintf("Projects (%d)", len(m.projectsRowNames))) + "\n" + m.projectsTable.View())
		if strings.TrimSpace(m.lastProjectsErr) != "" {
			body += "\n" + lipgloss.NewStyle().Foreground(cDanger).Render("错误: "+strings.TrimSpace(m.lastProjectsErr))
		}
	}
	switch m.projectsMode {
	case projectsModeRegisterConfirm:
		repoRoot := strings.TrimSpace(m.registerRepoRoot)
		if repoRoot == "" {
			repoRoot = "(unknown)"
		}
		modal := panelStyle().Render(
			panelTitle("注册当前目录项目") +
				"\n\n检测到当前目录是未注册的 git 项目：" +
				"\n" + lipgloss.NewStyle().Foreground(cInfo).Render(repoRoot) +
				"\n\n按 Enter/r 立即注册并打开项目；按 Esc 取消；按 q 退出。",
		)
		body += "\n\n" + modal
	case projectsModeRegisterWarn:
		msg := strings.TrimSpace(m.registerWarn)
		if msg == "" {
			msg = "当前目录不可注册"
		}
		modal := panelStyle().Render(
			panelTitle("无法注册当前目录") +
				"\n\n" + lipgloss.NewStyle().Foreground(cDanger).Render(msg) +
				"\n\n按 Enter 或 Esc 返回；按 q 退出。",
		)
		body += "\n\n" + modal
	}
	footer := footerStyle().Render("提示：也可以用 `dalek project add -path ...` 注册项目")
	return appStyle().Render(lipgloss.JoinVertical(lipgloss.Left, header, body, footer))
}

// ---------- Monitor Page (Existing) ----------

func (m appModel) setMonitor(mon model) (tea.Model, tea.Cmd) {
	// 进入 monitor 前先注入一次 window size，否则 monitor 容易用默认宽度渲染（看起来“变差”）。
	if m.width > 0 && m.height > 0 {
		if nm, _ := mon.Update(tea.WindowSizeMsg{Width: m.width, Height: m.height}); nm != nil {
			if mm, ok := nm.(model); ok {
				mon = mm
			}
		}
	}
	m.monitor = mon
	m.hasMonitor = true
	m.hasNotebook = false
	m.page = pageMonitor
	return m, mon.Init()
}

func (m appModel) monitorUpdate(msg tea.Msg) (appModel, tea.Cmd) {
	if !m.hasMonitor {
		return m, nil
	}
	nm, cmd := m.monitor.Update(msg)
	if mm, ok := nm.(model); ok {
		m.monitor = mm
	}
	return m, cmd
}

func (m appModel) viewMonitor() string {
	if !m.hasMonitor {
		return appStyle().Render(panelStyle().Render("monitor 未初始化"))
	}
	return m.monitor.View()
}

func (m appModel) setNotebook(nb notebookModel) (tea.Model, tea.Cmd) {
	// 进入 notebook 前先注入一次窗口尺寸，避免初次渲染过窄。
	if m.width > 0 && m.height > 0 {
		if nn, _ := nb.Update(tea.WindowSizeMsg{Width: m.width, Height: m.height}); nn != nil {
			if nm, ok := nn.(notebookModel); ok {
				nb = nm
			}
		}
	}
	m.notebook = nb
	m.hasNotebook = true
	m.page = pageNotebook
	return m, nb.Init()
}

func (m appModel) notebookUpdate(msg tea.Msg) (appModel, tea.Cmd) {
	if !m.hasNotebook {
		return m, nil
	}
	nm, cmd := m.notebook.Update(msg)
	if nn, ok := nm.(notebookModel); ok {
		m.notebook = nn
	}
	return m, cmd
}

func (m appModel) viewNotebook() string {
	if !m.hasNotebook {
		return appStyle().Render(panelStyle().Render("notebook 未初始化"))
	}
	return m.notebook.View()
}
