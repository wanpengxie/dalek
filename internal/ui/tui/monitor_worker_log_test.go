package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"dalek/internal/contracts"
)

func TestRenderWorkerLog_Empty(t *testing.T) {
	got := renderWorkerLog(contracts.TailPreview{})
	if !strings.Contains(got, "暂无日志输出") {
		t.Fatalf("unexpected empty render: %q", got)
	}
}

func TestUpdateWorkerLog_EscBackToTable(t *testing.T) {
	m := newModel(nil, nil, "")
	m.mode = modeWorkerLog
	m.workerLogInFlight = true
	m.workerLogErr = "boom"

	gotModel, cmd := m.updateWorkerLog(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd != nil {
		t.Fatalf("esc should not schedule cmd")
	}
	got := gotModel.(model)
	if got.mode != modeTable {
		t.Fatalf("mode=%v, want=%v", got.mode, modeTable)
	}
	if got.workerLogInFlight {
		t.Fatalf("workerLogInFlight should be false after esc")
	}
	if got.workerLogErr != "" {
		t.Fatalf("workerLogErr should be cleared, got=%q", got.workerLogErr)
	}
}

func TestUpdateWorkerLog_RStartsLoad(t *testing.T) {
	m := newModel(nil, nil, "")
	m.mode = modeWorkerLog
	m.workerLogTicketID = 9

	gotModel, cmd := m.updateWorkerLog(keyRune('r'))
	if cmd == nil {
		t.Fatalf("r should schedule load cmd")
	}
	got := gotModel.(model)
	if !got.workerLogInFlight {
		t.Fatalf("worker log should mark in flight")
	}
	if !strings.Contains(got.status, "加载日志 t9") {
		t.Fatalf("unexpected status: %q", got.status)
	}
}
