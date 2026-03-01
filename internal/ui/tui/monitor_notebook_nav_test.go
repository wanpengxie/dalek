package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestUpdateTable_NShortcutGotoNotebook(t *testing.T) {
	m := newModel(nil, nil, "")

	gotModel, cmd := m.updateTable(keyRune('n'))
	if cmd == nil {
		t.Fatalf("n should emit goto notebook cmd")
	}

	got := gotModel.(model)
	if got.mode != modeTable {
		t.Fatalf("mode changed unexpectedly: got=%v want=%v", got.mode, modeTable)
	}

	msg := cmd()
	if _, ok := msg.(gotoNotebookMsg); !ok {
		t.Fatalf("expected gotoNotebookMsg, got=%T", msg)
	}
}

func TestUpdateTable_CShortcutNewTicket(t *testing.T) {
	m := newModel(nil, nil, "")
	m.newLabel.SetValue("should-clear")

	gotModel, cmd := m.updateTable(keyRune('c'))
	if cmd != nil {
		t.Fatalf("c should not emit extra cmd, got=%T", cmd)
	}

	got := gotModel.(model)
	if got.mode != modeNewTicket {
		t.Fatalf("mode=%v, want=%v", got.mode, modeNewTicket)
	}
	if got.newLabel.Value() != "" {
		t.Fatalf("new ticket label should reset on open, got=%q", got.newLabel.Value())
	}
}

func TestOpenNotebookCmdMessageType(t *testing.T) {
	cmd := openNotebookCmd()
	if cmd == nil {
		t.Fatalf("openNotebookCmd should not be nil")
	}
	msg := cmd()
	if _, ok := msg.(tea.Msg); !ok {
		t.Fatalf("expected tea.Msg, got=%T", msg)
	}
	if _, ok := msg.(gotoNotebookMsg); !ok {
		t.Fatalf("expected gotoNotebookMsg, got=%T", msg)
	}
}
