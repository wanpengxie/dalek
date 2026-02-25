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
