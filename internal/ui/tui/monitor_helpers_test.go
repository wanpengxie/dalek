package tui

import "testing"

func TestTailTail(t *testing.T) {
	got := tailTail([]string{"1", "2", "3", "4"}, 2)
	if len(got) != 2 || got[0] != "3" || got[1] != "4" {
		t.Fatalf("unexpected tail: %#v", got)
	}

	got = tailTail([]string{"x"}, 3)
	if len(got) != 3 || got[2] != "x" {
		t.Fatalf("unexpected padded tail: %#v", got)
	}
}

func TestTrimCell(t *testing.T) {
	if got := trimCell("abcdef", 4); got != "a..." {
		t.Fatalf("unexpected trimCell result: %q", got)
	}
	if got := trimCell("abc", 5); got != "abc" {
		t.Fatalf("unexpected trimCell short result: %q", got)
	}
}

func TestLabelOrDash(t *testing.T) {
	if got := labelOrDash(""); got != "-" {
		t.Fatalf("empty label should fallback to dash, got=%q", got)
	}
	if got := labelOrDash("   "); got != "-" {
		t.Fatalf("blank label should fallback to dash, got=%q", got)
	}
	if got := labelOrDash(" feature "); got != "feature" {
		t.Fatalf("label should be trimmed, got=%q", got)
	}
}

func TestTableColumnsIncludeLabel(t *testing.T) {
	layout := defaultTableLayout()
	if layout.label != 8 {
		t.Fatalf("default label width mismatch: got=%d want=8", layout.label)
	}
	if layout.integ != 8 {
		t.Fatalf("default integration width mismatch: got=%d want=8", layout.integ)
	}
	if layout.title != 28 {
		t.Fatalf("default title width mismatch: got=%d want=28", layout.title)
	}
	cols := tableColumns(layout)
	if len(cols) != 9 {
		t.Fatalf("columns count mismatch: got=%d want=9", len(cols))
	}
	if cols[3].Title != "标签" {
		t.Fatalf("label column title mismatch: got=%q", cols[3].Title)
	}
	if cols[5].Title != "集成" {
		t.Fatalf("integration column title mismatch: got=%q", cols[5].Title)
	}
}

func TestTicketPriorityShort(t *testing.T) {
	cases := []struct {
		priority int
		want     string
	}{
		{priority: 0, want: "N"},
		{priority: 1, want: "L"},
		{priority: 2, want: "M"},
		{priority: 3, want: "H"},
		{priority: 9, want: "9"},
	}
	for _, tc := range cases {
		if got := ticketPriorityShort(tc.priority); got != tc.want {
			t.Fatalf("ticketPriorityShort(%d)=%q, want=%q", tc.priority, got, tc.want)
		}
	}
}

func TestTicketPriorityDisplay(t *testing.T) {
	cases := []struct {
		priority int
		want     string
	}{
		{priority: 0, want: "none(0)"},
		{priority: 1, want: "low(1)"},
		{priority: 2, want: "medium(2)"},
		{priority: 3, want: "high(3)"},
		{priority: 9, want: "9"},
	}
	for _, tc := range cases {
		if got := ticketPriorityDisplay(tc.priority); got != tc.want {
			t.Fatalf("ticketPriorityDisplay(%d)=%q, want=%q", tc.priority, got, tc.want)
		}
	}
}

func TestTmuxSessionForWorktree_Deterministic(t *testing.T) {
	path := "/tmp/ticket-t71-demo-abcdef01"
	a := tmuxSessionForWorktree(path)
	b := tmuxSessionForWorktree(path)
	if a != b {
		t.Fatalf("same path should map to same session, a=%q b=%q", a, b)
	}
	if a == "" {
		t.Fatalf("session name should not be empty")
	}
}

func TestTmuxSessionForWorktree_DifferentPathDifferentSession(t *testing.T) {
	a := tmuxSessionForWorktree("/tmp/ticket-t71-demo-abcdef01")
	b := tmuxSessionForWorktree("/tmp/ticket-t71-demo-fedcba10")
	if a == b {
		t.Fatalf("different path should map to different session, got=%q", a)
	}
}
