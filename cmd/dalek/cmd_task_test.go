package main

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"dalek/internal/app"
)

func TestRequirePositiveDuration(t *testing.T) {
	if err := requirePositiveDuration("timeout", time.Second); err != nil {
		t.Fatalf("expected nil for positive duration, got=%v", err)
	}
	if err := requirePositiveDuration("timeout", 0); err == nil {
		t.Fatalf("expected error for zero duration")
	}
	if err := requirePositiveDuration("timeout", -time.Second); err == nil {
		t.Fatalf("expected error for negative duration")
	}
}

func TestRequirePositiveInt(t *testing.T) {
	if err := requirePositiveInt("limit", 1); err != nil {
		t.Fatalf("expected nil for positive int, got=%v", err)
	}
	if err := requirePositiveInt("limit", 0); err == nil {
		t.Fatalf("expected error for zero int")
	}
	if err := requirePositiveInt("limit", -1); err == nil {
		t.Fatalf("expected error for negative int")
	}
}

func TestStatusUpdatedAtPrefersLatestObservationTime(t *testing.T) {
	base := time.Date(2026, 2, 18, 10, 0, 0, 0, time.UTC)
	runtimeAt := base.Add(1 * time.Minute)
	semanticAt := base.Add(2 * time.Minute)
	eventAt := base.Add(3 * time.Minute)

	got := app.TaskStatusUpdatedAt(app.TaskStatus{
		UpdatedAt:          base,
		RuntimeObservedAt:  &runtimeAt,
		SemanticReportedAt: &semanticAt,
		LastEventAt:        &eventAt,
	})
	if !got.Equal(eventAt) {
		t.Fatalf("expected latest event time, got=%s", got.Format(time.RFC3339))
	}
}

func TestTrimField_UTF8Safe(t *testing.T) {
	in := "这是一个很长的中文总结"
	got := trimField(in, 8)
	if !utf8.ValidString(got) {
		t.Fatalf("trimField produced invalid utf8: %q", got)
	}
	if !strings.HasSuffix(got, "...") {
		t.Fatalf("expected ellipsis suffix, got=%q", got)
	}
}

func TestNormalizeTaskRunIDArgs_RewriteAlias(t *testing.T) {
	got := normalizeTaskRunIDArgs([]string{
		"--run", "11",
		"--run=12",
		"--id", "13",
		"--other=ok",
	})
	want := []string{
		"--id", "11",
		"--id=12",
		"--id", "13",
		"--other=ok",
	}
	if len(got) != len(want) {
		t.Fatalf("len mismatch: got=%d want=%d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("arg[%d] mismatch: got=%q want=%q", i, got[i], want[i])
		}
	}
}

func TestNormalizeTaskRunIDArgs_EmptyInput(t *testing.T) {
	if got := normalizeTaskRunIDArgs(nil); got != nil {
		t.Fatalf("expected nil for nil input")
	}
	got := normalizeTaskRunIDArgs([]string{})
	if got != nil {
		t.Fatalf("expected nil for empty input")
	}
}
