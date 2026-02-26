package app

import (
	"testing"
	"time"
)

func TestDeriveRunStatus(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name          string
		orchestration string
		health        string
		needsUser     bool
		want          string
	}{
		{name: "pending", orchestration: "pending", want: "pending"},
		{name: "succeeded", orchestration: "succeeded", want: "done"},
		{name: "failed", orchestration: "failed", want: "failed"},
		{name: "canceled", orchestration: "canceled", want: "canceled"},
		{name: "running waiting user by flag", orchestration: "running", needsUser: true, want: "waiting_user"},
		{name: "running waiting user by runtime", orchestration: "running", health: "waiting_user", want: "waiting_user"},
		{name: "running stalled", orchestration: "running", health: "stalled", want: "stalled"},
		{name: "running dead", orchestration: "running", health: "dead", want: "dead"},
		{name: "running alive", orchestration: "running", health: "alive", want: "running"},
		{name: "running unknown", orchestration: "running", health: "unknown", want: "running"},
		{name: "unknown orch waiting user", orchestration: "mystery", health: "alive", needsUser: true, want: "waiting_user"},
		{name: "unknown orch stalled", orchestration: "mystery", health: "stalled", want: "stalled"},
		{name: "unknown orch dead", orchestration: "mystery", health: "dead", want: "dead"},
		{name: "unknown orch busy", orchestration: "mystery", health: "busy", want: "running"},
		{name: "unknown orch unknown", orchestration: "mystery", health: "unknown", want: "unknown"},
		{name: "trim and case", orchestration: "  RUNNING ", health: "  DEAD ", want: "dead"},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := DeriveRunStatus(tc.orchestration, tc.health, tc.needsUser); got != tc.want {
				t.Fatalf("DeriveRunStatus()=%q want=%q", got, tc.want)
			}
		})
	}
}

func TestTaskStatusUpdatedAt(t *testing.T) {
	t.Parallel()
	base := time.Date(2026, 2, 18, 10, 0, 0, 0, time.UTC)
	runtimeAt := base.Add(1 * time.Minute)
	semanticAt := base.Add(2 * time.Minute)
	eventAt := base.Add(3 * time.Minute)

	got := TaskStatusUpdatedAt(TaskStatus{
		UpdatedAt:          base,
		RuntimeObservedAt:  &runtimeAt,
		SemanticReportedAt: &semanticAt,
		LastEventAt:        &eventAt,
	})
	if !got.Equal(eventAt) {
		t.Fatalf("expected latest event time, got=%s", got.Format(time.RFC3339))
	}
}
