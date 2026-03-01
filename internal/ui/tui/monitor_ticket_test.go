package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func keyTab() tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyTab}
}

func TestUpdateNewTicket_TabCyclesThreeFields(t *testing.T) {
	m := newModel(nil, nil, "")

	gotModel, _ := m.updateNewTicket(keyTab())
	got := gotModel.(model)
	if got.newFocus != 1 {
		t.Fatalf("newFocus after first tab = %d, want 1", got.newFocus)
	}

	gotModel, _ = got.updateNewTicket(keyTab())
	got = gotModel.(model)
	if got.newFocus != 2 {
		t.Fatalf("newFocus after second tab = %d, want 2", got.newFocus)
	}

	gotModel, _ = got.updateNewTicket(keyTab())
	got = gotModel.(model)
	if got.newFocus != 0 {
		t.Fatalf("newFocus after third tab = %d, want 0", got.newFocus)
	}
}

func TestUpdateEditTicket_TabCyclesThreeFields(t *testing.T) {
	m := newModel(nil, nil, "")
	m.editTicketID = 1

	gotModel, _ := m.updateEditTicket(keyTab())
	got := gotModel.(model)
	if got.editFocus != 1 {
		t.Fatalf("editFocus after first tab = %d, want 1", got.editFocus)
	}

	gotModel, _ = got.updateEditTicket(keyTab())
	got = gotModel.(model)
	if got.editFocus != 2 {
		t.Fatalf("editFocus after second tab = %d, want 2", got.editFocus)
	}

	gotModel, _ = got.updateEditTicket(keyTab())
	got = gotModel.(model)
	if got.editFocus != 0 {
		t.Fatalf("editFocus after third tab = %d, want 0", got.editFocus)
	}
}

func TestNewAndEditTicketViews_RenderLabelField(t *testing.T) {
	m := newModel(nil, nil, "")
	m.editTicketID = 7

	if got := m.newTicketView(120); !strings.Contains(got, "标签:") {
		t.Fatalf("new ticket view should contain label field, got=%q", got)
	}
	if got := m.editTicketView(120); !strings.Contains(got, "标签:") {
		t.Fatalf("edit ticket view should contain label field, got=%q", got)
	}
}
