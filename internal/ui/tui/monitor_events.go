package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"dalek/internal/app"
)

func (m model) updateEvents(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "esc", "q":
		m.mode = modeTable
		m.eventsInFlight = false
		m.eventsErr = ""
		m.status = "已返回列表"
		return m, nil
	case "r":
		if m.eventsInFlight {
			m.status = "事件加载中..."
			return m, nil
		}
		id := m.eventsTicketID
		if id == 0 {
			id = m.selectedTicketID()
		}
		if id == 0 {
			return m, nil
		}
		m.eventsInFlight = true
		m.eventsErr = ""
		m.status = fmt.Sprintf("加载事件 t%d...", id)
		return m, m.loadEventsCmd(id)
	}

	var cmd tea.Cmd
	m.eventsViewport, cmd = m.eventsViewport.Update(msg)
	return m, cmd
}

func (m model) eventsView(width int) string {
	tid := m.eventsTicketID
	if tid == 0 {
		tid = m.selectedTicketID()
	}

	head := panelTitle(fmt.Sprintf("事件  t%d", tid))
	if m.eventsWorkerID != 0 {
		head = head + faint(fmt.Sprintf("  w%d", m.eventsWorkerID))
	}

	state := badge("就绪", cOk)
	if m.eventsInFlight {
		state = badge("加载中", cInfo)
	} else if strings.TrimSpace(m.eventsErr) != "" {
		state = badge("错误", cDanger)
	}

	meta := ""
	if !m.eventsLoadedAt.IsZero() {
		meta = faint("已加载: " + m.eventsLoadedAt.Format("15:04:05"))
	}

	top := head + "  " + faint("↑↓ 滚动 | r 刷新 | Esc 返回") + "  " + state
	if meta != "" {
		top = top + "  " + meta
	}

	body := top + "\n\n" + m.eventsViewport.View()
	return panelStyle().Width(width).Render(body)
}

func (m model) loadEventsCmd(ticketID uint) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()

		a, err := m.p.LatestWorker(ctx, ticketID)
		if err != nil {
			return eventsLoadedMsg{TicketID: ticketID, WorkerID: 0, Events: nil, Err: err}
		}
		workerID := uint(0)
		if a != nil {
			workerID = a.ID
		}

		evs, err := m.p.ListTaskEventsByScope(ctx, ticketID, workerID, 200)
		if err != nil {
			return eventsLoadedMsg{TicketID: ticketID, WorkerID: workerID, Events: nil, Err: err}
		}
		for i, j := 0, len(evs)-1; i < j; i, j = i+1, j-1 {
			evs[i], evs[j] = evs[j], evs[i]
		}
		return eventsLoadedMsg{TicketID: ticketID, WorkerID: workerID, Events: evs, Err: nil}
	}
}

func renderEvents(evs []app.TaskEvent) string {
	if len(evs) == 0 {
		return faint("(暂无事件)\n\n提示：按 r 可手动刷新；按 Esc 返回列表。")
	}

	var b strings.Builder
	for i, e := range evs {
		if i > 0 {
			b.WriteString("\n\n")
		}

		ts := e.CreatedAt.Local().Format("01-02 15:04:05")
		typ := strings.TrimSpace(e.EventType)
		if typ == "" {
			typ = "-"
		}

		// Header
		b.WriteString(fmt.Sprintf("%s  run=%d  %s", ts, e.TaskRunID, typ))

		note := strings.TrimSpace(e.Note)
		if note != "" {
			b.WriteString("\n")
			b.WriteString(faint("note: "))
			b.WriteString(oneLine(note))
		}

		from := strings.TrimSpace(e.FromStateJSON)
		if from != "" {
			b.WriteString("\n")
			b.WriteString(faint("from: "))
			b.WriteString(oneLine(from))
		}

		to := strings.TrimSpace(e.ToStateJSON)
		if to != "" {
			b.WriteString("\n")
			b.WriteString(faint("to: "))
			b.WriteString(oneLine(to))
		}

		payload := strings.TrimSpace(e.PayloadJSON)
		if payload != "" {
			b.WriteString("\n")
			b.WriteString(faint("payload: "))
			b.WriteString(oneLine(payload))
		}
	}
	return b.String()
}
