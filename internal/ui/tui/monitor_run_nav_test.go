package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestUpdateTable_RShortcutGotoRuns(t *testing.T) {
	m := newModel(nil, nil, "")

	gotModel, cmd := m.updateTable(keyRune('R'))
	if cmd == nil {
		t.Fatalf("R should emit goto runs cmd")
	}

	got := gotModel.(model)
	if got.mode != modeTable {
		t.Fatalf("mode changed unexpectedly: got=%v want=%v", got.mode, modeTable)
	}

	msg := cmd()
	if _, ok := msg.(gotoRunsMsg); !ok {
		t.Fatalf("expected gotoRunsMsg, got=%T", msg)
	}
}

func TestOpenRunsCmdMessageType(t *testing.T) {
	cmd := openRunsCmd()
	if cmd == nil {
		t.Fatalf("openRunsCmd should not be nil")
	}
	msg := cmd()
	if _, ok := msg.(tea.Msg); !ok {
		t.Fatalf("expected tea.Msg, got=%T", msg)
	}
	if _, ok := msg.(gotoRunsMsg); !ok {
		t.Fatalf("expected gotoRunsMsg, got=%T", msg)
	}
}
