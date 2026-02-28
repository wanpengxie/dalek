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
