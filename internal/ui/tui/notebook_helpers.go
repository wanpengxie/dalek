package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"dalek/internal/app"
)

type notebookAction string

const (
	notebookActionApprove notebookAction = "approve"
	notebookActionReject  notebookAction = "reject"
	notebookActionDiscard notebookAction = "discard"
)

type notebookListLoadedMsg struct {
	Notes    []app.NoteView
	Err      error
	LoadedAt time.Time
}

type notebookDetailLoadedMsg struct {
	NoteID uint
	Note   *app.NoteView
	Err    error
}

type notebookActionDoneMsg struct {
	Action   notebookAction
	NoteID   uint
	TicketID uint
	Err      error
}

type notebookAddDoneMsg struct {
	Result app.NoteAddResult
	Err    error
}

func openNotebookCmd() tea.Cmd {
	return func() tea.Msg {
		return gotoNotebookMsg{}
	}
}

func closeNotebookCmd() tea.Cmd {
	return func() tea.Msg {
		return notebookClosedMsg{}
	}
}

func (m notebookModel) loadNotesCmd() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		items, err := m.p.ListNotes(ctx, app.ListNoteOptions{Limit: 200})
		return notebookListLoadedMsg{
			Notes:    items,
			Err:      err,
			LoadedAt: time.Now(),
		}
	}
}

func (m notebookModel) loadNoteDetailCmd(noteID uint) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		item, err := m.p.GetNote(ctx, noteID)
		return notebookDetailLoadedMsg{
			NoteID: noteID,
			Note:   item,
			Err:    err,
		}
	}
}

func (m notebookModel) runNoteActionCmd(action notebookAction, noteID uint) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		out := notebookActionDoneMsg{
			Action: action,
			NoteID: noteID,
		}
		switch action {
		case notebookActionApprove:
			tk, err := m.p.ApproveNote(ctx, noteID, "tui")
			out.Err = err
			if tk != nil {
				out.TicketID = tk.ID
			}
		case notebookActionReject:
			out.Err = m.p.RejectNote(ctx, noteID, "rejected from tui")
		case notebookActionDiscard:
			out.Err = m.p.DiscardNote(ctx, noteID)
		default:
			out.Err = fmt.Errorf("unknown action: %s", action)
		}
		return out
	}
}

func (m notebookModel) addNoteCmd(raw string) tea.Cmd {
	raw = strings.TrimSpace(raw)
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		res, err := m.p.AddNote(ctx, raw)
		return notebookAddDoneMsg{
			Result: res,
			Err:    err,
		}
	}
}

func notebookStatusKey(note app.NoteView) string {
	noteStatus := strings.TrimSpace(strings.ToLower(note.Status))
	if noteStatus == "discarded" {
		return "discarded"
	}
	shapedStatus := ""
	if note.Shaped != nil {
		shapedStatus = strings.TrimSpace(strings.ToLower(note.Shaped.Status))
	}
	switch shapedStatus {
	case "approved":
		return "approved"
	case "rejected":
		return "rejected"
	case "needs_info":
		return "needs_info"
	case "pending_review":
		return "pending_review"
	}
	switch noteStatus {
	case "open":
		return "open"
	case "shaping":
		return "shaping"
	case "shaped", "pending_review":
		return "pending_review"
	case "approved":
		return "approved"
	case "rejected":
		return "rejected"
	case "discarded":
		return "discarded"
	default:
		if note.Shaped != nil {
			return "pending_review"
		}
		if noteStatus == "" {
			return "unknown"
		}
		return noteStatus
	}
}

func notebookStatusLabelColor(note app.NoteView) (string, lipgloss.TerminalColor) {
	switch notebookStatusKey(note) {
	case "open":
		return "OPEN", cInfo
	case "shaping":
		return "SHAPING", cInfo
	case "pending_review":
		return "SHAPED", cWarn
	case "approved":
		return "APPROVED", cOk
	case "rejected":
		return "REJECTED", cMuted
	case "discarded":
		return "DISCARDED", cFaint
	case "needs_info":
		return "NEEDS_INFO", cDanger
	default:
		return "UNKNOWN", cNeutral
	}
}

func notebookStatusBadge(note app.NoteView) string {
	label, color := notebookStatusLabelColor(note)
	return badge(label, color)
}

func notebookStatusText(note app.NoteView) string {
	label, _ := notebookStatusLabelColor(note)
	return strings.ToLower(label)
}

func notebookTitle(note app.NoteView) string {
	if note.Shaped != nil {
		if title := strings.TrimSpace(note.Shaped.Title); title != "" {
			return oneLine(title)
		}
	}
	txt := strings.TrimSpace(note.Text)
	if txt == "" {
		return "(empty)"
	}
	return oneLine(txt)
}

func notebookCreatedAt(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.Local().Format("01-02 15:04")
}

func notebookActionName(action notebookAction) string {
	switch action {
	case notebookActionApprove:
		return "approve"
	case notebookActionReject:
		return "reject"
	case notebookActionDiscard:
		return "discard"
	default:
		return string(action)
	}
}

func notebookActionPrompt(action notebookAction, noteID uint) string {
	switch action {
	case notebookActionApprove:
		return fmt.Sprintf("确认 approve note #%d 并创建 ticket？", noteID)
	case notebookActionReject:
		return fmt.Sprintf("确认 reject note #%d？", noteID)
	case notebookActionDiscard:
		return fmt.Sprintf("确认 discard note #%d？", noteID)
	default:
		return fmt.Sprintf("确认执行 %s note #%d？", notebookActionName(action), noteID)
	}
}

func notebookCanAction(note app.NoteView, action notebookAction) bool {
	st := notebookStatusKey(note)
	switch action {
	case notebookActionApprove, notebookActionReject:
		return st == "pending_review" || st == "rejected" || st == "needs_info"
	case notebookActionDiscard:
		return st != "discarded"
	default:
		return false
	}
}

func notebookAcceptanceItems(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "[]" {
		return nil
	}

	var items []string
	if err := json.Unmarshal([]byte(raw), &items); err == nil {
		out := make([]string, 0, len(items))
		for _, item := range items {
			item = strings.TrimSpace(item)
			if item != "" {
				out = append(out, item)
			}
		}
		if len(out) > 0 {
			return out
		}
	}

	var anyItems []any
	if err := json.Unmarshal([]byte(raw), &anyItems); err == nil {
		out := make([]string, 0, len(anyItems))
		for _, item := range anyItems {
			text := strings.TrimSpace(fmt.Sprint(item))
			if text != "" {
				out = append(out, text)
			}
		}
		if len(out) > 0 {
			return out
		}
	}

	fallback := oneLine(raw)
	if fallback == "" {
		return nil
	}
	return []string{fallback}
}
