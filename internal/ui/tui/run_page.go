package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"dalek/internal/app"
)

type runTickMsg struct{}

type runRefreshedMsg struct {
	Runs     []app.RunView
	Statuses map[uint]app.TaskStatus
	Routes   map[uint]app.TaskRouteInfo
	Err      error
}

type runModel struct {
	p           *app.Project
	projectName string

	width  int
	height int

	table     table.Model
	tableRows []app.RunView
	statuses  map[uint]app.TaskStatus
	routes    map[uint]app.TaskRouteInfo
	detail    viewport.Model

	status          string
	errMsg          string
	refreshInFlight bool
	lastRefresh     time.Time
}

func newRunModel(p *app.Project, projectName string) runModel {
	cols := []table.Column{
		{Title: "Run", Width: 6},
		{Title: "状态", Width: 12},
		{Title: "Target", Width: 12},
		{Title: "Snapshot", Width: 14},
		{Title: "更新", Width: 20},
	}
	t := table.New(
		table.WithColumns(cols),
		table.WithFocused(true),
		table.WithHeight(10),
	)
	t.SetStyles(tableStyles())

	vp := viewport.New(0, 0)

	return runModel{
		p:           p,
		projectName: strings.TrimSpace(projectName),
		table:       t,
		tableRows:   nil,
		statuses:    map[uint]app.TaskStatus{},
		routes:      map[uint]app.TaskRouteInfo{},
		detail:      vp,
		status:      "就绪",
		errMsg:      "",
	}
}

func (m runModel) Init() tea.Cmd {
	return tea.Batch(m.refreshCmd(), m.tickCmd())
}

func (m runModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.table.SetHeight(max(6, msg.Height-12))
		m.detail.Width = max(30, msg.Width-6)
		m.detail.Height = max(6, msg.Height-10-m.table.Height())
		return m, nil

	case runTickMsg:
		cmds := []tea.Cmd{m.tickCmd()}
		if !m.refreshInFlight {
			m.refreshInFlight = true
			cmds = append(cmds, m.refreshCmd())
		}
		return m, tea.Batch(cmds...)

	case runRefreshedMsg:
		m.refreshInFlight = false
		if msg.Err != nil {
			m.errMsg = msg.Err.Error()
			return m, nil
		}
		m.errMsg = ""
		m.lastRefresh = time.Now()
		m.tableRows = msg.Runs
		if msg.Statuses == nil {
			m.statuses = map[uint]app.TaskStatus{}
		} else {
			m.statuses = msg.Statuses
		}
		if msg.Routes == nil {
			m.routes = map[uint]app.TaskRouteInfo{}
		} else {
			m.routes = msg.Routes
		}
		m.table.SetRows(runRows(msg.Runs))
		recoveryCount := 0
		artifactWarnCount := 0
		for _, run := range msg.Runs {
			switch strings.TrimSpace(string(run.RunStatus)) {
			case "node_offline", "reconciling":
				recoveryCount++
			}
			if status, ok := m.statuses[run.RunID]; ok && strings.TrimSpace(status.LastEventType) == "run_artifact_upload_failed" {
				artifactWarnCount++
			}
		}
		if len(msg.Runs) == 0 {
			m.status = "暂无 run"
		} else {
			m.status = fmt.Sprintf("共 %d 个 run  恢复中 %d  artifact 异常 %d", len(msg.Runs), recoveryCount, artifactWarnCount)
		}
		m.detail.SetContent(m.renderDetail())
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "r":
			if !m.refreshInFlight {
				m.refreshInFlight = true
				return m, m.refreshCmd()
			}
			m.status = "刷新已在进行中"
			return m, nil
		}
	}

	var cmd tea.Cmd
	m.table, cmd = m.table.Update(msg)
	m.detail.SetContent(m.renderDetail())
	return m, cmd
}

func (m runModel) View() string {
	title := panelTitle(fmt.Sprintf("Run 视图  %s", strings.TrimSpace(m.projectName)))
	status := strings.TrimSpace(m.status)
	if status == "" {
		status = "就绪"
	}
	bar := headerStyle().Render(fmt.Sprintf("%s  %s", title, faint(status)))
	if m.errMsg != "" {
		bar = headerStyle().Render(fmt.Sprintf("%s  %s", title, badge("ERROR", cDanger)))
	}

	tableView := panelStyle().Width(max(10, m.width-4)).Render(m.table.View())
	detailView := panelStyle().Width(max(10, m.width-4)).Render(m.detail.View())
	return lipgloss.JoinVertical(lipgloss.Top, bar, tableView, detailView)
}

func (m runModel) refreshCmd() tea.Cmd {
	return func() tea.Msg {
		if m.p == nil {
			return runRefreshedMsg{Runs: nil, Err: fmt.Errorf("project 未初始化")}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		runs, err := m.p.ListRuns(ctx, 50)
		if err != nil {
			return runRefreshedMsg{Runs: nil, Err: err}
		}
		statusList, err := m.p.ListTaskStatus(ctx, app.ListTaskOptions{
			OwnerType:       "",
			TaskType:        "run_verify",
			IncludeTerminal: true,
			Limit:           50,
		})
		if err != nil {
			return runRefreshedMsg{Runs: runs, Err: err}
		}
		statuses := make(map[uint]app.TaskStatus, len(statusList))
		for _, item := range statusList {
			statuses[item.RunID] = item
		}
		routes := make(map[uint]app.TaskRouteInfo, len(runs))
		for _, run := range runs {
			route, ok, err := m.p.GetTaskRouteInfo(ctx, run.RunID)
			if err != nil {
				return runRefreshedMsg{Runs: runs, Statuses: statuses, Err: err}
			}
			if ok {
				routes[run.RunID] = route
			}
		}
		return runRefreshedMsg{Runs: runs, Statuses: statuses, Routes: routes, Err: nil}
	}
}

func (m runModel) tickCmd() tea.Cmd {
	return tea.Tick(2*time.Second, func(time.Time) tea.Msg { return runTickMsg{} })
}

func (m runModel) renderDetail() string {
	if len(m.tableRows) == 0 {
		return faint("暂无 run")
	}
	idx := m.table.Cursor()
	if idx < 0 || idx >= len(m.tableRows) {
		return faint("未选择 run")
	}
	v := m.tableRows[idx]
	status, hasStatus := m.statuses[v.RunID]
	lines := []string{
		kv("run:", fmt.Sprintf("%d", v.RunID)),
		kv("status:", string(v.RunStatus)),
		kv("request:", strings.TrimSpace(v.RequestID)),
		kv("ticket:", fmt.Sprintf("t%d", v.TicketID)),
		kv("worker:", fmt.Sprintf("w%d", v.WorkerID)),
		kv("target:", strings.TrimSpace(v.VerifyTarget)),
		kv("snapshot:", strings.TrimSpace(v.SnapshotID)),
		kv("base:", strings.TrimSpace(v.BaseCommit)),
		kv("workspace:", strings.TrimSpace(v.SourceWorkspaceGeneration)),
	}
	if hasStatus {
		lines = append(lines,
			kv("summary:", strings.TrimSpace(status.RuntimeSummary)),
			kv("milestone:", strings.TrimSpace(status.SemanticMilestone)),
			kv("last_event:", strings.TrimSpace(status.LastEventType)),
		)
		if note := strings.TrimSpace(status.LastEventNote); note != "" {
			lines = append(lines, kv("last_note:", note))
		}
	}
	if route, ok := m.routes[v.RunID]; ok {
		lines = append(lines,
			kv("role:", strings.TrimSpace(route.Role)),
			kv("role_source:", strings.TrimSpace(route.RoleSource)),
			kv("route:", firstNonEmpty(strings.TrimSpace(route.RouteTarget), strings.TrimSpace(route.RouteMode))),
		)
		if reason := strings.TrimSpace(route.RouteReason); reason != "" {
			lines = append(lines, kv("route_reason:", reason))
		}
		if route.RemoteRunID != 0 {
			lines = append(lines, kv("remote_run_id:", fmt.Sprintf("%d", route.RemoteRunID)))
		}
	}
	return strings.Join(lines, "\n")
}

func runRows(runs []app.RunView) []table.Row {
	rows := make([]table.Row, 0, len(runs))
	for _, v := range runs {
		rows = append(rows, table.Row{
			fmt.Sprintf("%d", v.RunID),
			string(v.RunStatus),
			strings.TrimSpace(v.VerifyTarget),
			trimLeft(strings.TrimSpace(v.SnapshotID), 12),
			formatShortTime(v.UpdatedAt),
		})
	}
	return rows
}

func formatShortTime(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.Local().Format("01-02 15:04:05")
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return "-"
}
