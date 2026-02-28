package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"dalek/internal/contracts"
)

func (m model) updateWorkerLog(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "esc", "q":
		m.mode = modeTable
		m.workerLogInFlight = false
		m.workerLogErr = ""
		m.status = "已返回列表"
		return m, nil
	case "r":
		if m.workerLogInFlight {
			m.status = "日志加载中..."
			return m, nil
		}
		id := m.workerLogTicketID
		if id == 0 {
			id = m.selectedTicketID()
		}
		if id == 0 {
			return m, nil
		}
		m.workerLogInFlight = true
		m.workerLogErr = ""
		m.status = fmt.Sprintf("加载日志 t%d...", id)
		return m, m.loadWorkerLogCmd(id)
	}

	var cmd tea.Cmd
	m.workerLogViewport, cmd = m.workerLogViewport.Update(msg)
	return m, cmd
}

func (m model) workerLogView(width int) string {
	tid := m.workerLogTicketID
	if tid == 0 {
		tid = m.selectedTicketID()
	}

	head := panelTitle(fmt.Sprintf("日志  t%d", tid))
	if m.workerLogWorkerID != 0 {
		head = head + faint(fmt.Sprintf("  w%d", m.workerLogWorkerID))
	}

	state := badge("流式刷新", cOk)
	if m.workerLogInFlight {
		state = badge("加载中", cInfo)
	} else if strings.TrimSpace(m.workerLogErr) != "" {
		state = badge("错误", cDanger)
	}

	meta := ""
	if !m.workerLogLoadedAt.IsZero() {
		meta = faint("已加载: " + m.workerLogLoadedAt.Format("15:04:05"))
	}

	source := strings.TrimSpace(m.workerLogSource)
	if source == "" {
		source = "-"
	}
	logPath := strings.TrimSpace(m.workerLogLogPath)
	if logPath == "" {
		logPath = "-"
	}

	top := head + "  " + faint("↑↓ 滚动 | r 刷新 | Esc 返回") + "  " + state
	if meta != "" {
		top = top + "  " + meta
	}

	body := top +
		"\n" +
		kvLine("source:", source, max(20, width-6)) +
		"\n" +
		kvLine("log:", trimLeft(logPath, max(20, width-20)), max(20, width-6)) +
		"\n\n" +
		m.workerLogViewport.View()
	return panelStyle().Width(width).Render(body)
}

func (m model) loadWorkerLogCmd(ticketID uint) tea.Cmd {
	return func() tea.Msg {
		if m.p == nil {
			return workerLogLoadedMsg{TicketID: ticketID, WorkerID: 0, Err: fmt.Errorf("project 不可用")}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()

		a, err := m.p.LatestWorker(ctx, ticketID)
		if err != nil {
			return workerLogLoadedMsg{TicketID: ticketID, WorkerID: 0, Err: err}
		}
		if a == nil {
			return workerLogLoadedMsg{TicketID: ticketID, WorkerID: 0, Err: fmt.Errorf("该 ticket 尚未启动 worker，请先按 s 启动")}
		}
		if strings.TrimSpace(a.LogPath) == "" {
			return workerLogLoadedMsg{TicketID: ticketID, WorkerID: a.ID, Err: fmt.Errorf("worker 尚无日志锚点，请先 dispatch 或重新跑")}
		}

		pv, err := m.p.CaptureTicketTail(ctx, ticketID, workerLogCaptureLines)
		if err != nil {
			return workerLogLoadedMsg{TicketID: ticketID, WorkerID: a.ID, Err: err}
		}
		if strings.TrimSpace(pv.LogPath) == "" {
			pv.LogPath = strings.TrimSpace(a.LogPath)
		}
		if strings.TrimSpace(pv.Source) == "" {
			pv.Source = "worker_log"
		}
		return workerLogLoadedMsg{TicketID: ticketID, WorkerID: a.ID, Preview: pv, Err: nil}
	}
}

func renderWorkerLog(pv contracts.TailPreview) string {
	lines := pv.Lines
	if len(lines) == 0 {
		return faint("(暂无日志输出)\n\n提示：按 r 手动刷新；按 Esc 返回列表。")
	}
	return strings.Join(lines, "\n")
}
