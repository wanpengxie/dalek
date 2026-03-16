package run

import (
	"context"
	"testing"
	"time"
)

func TestRunScheduler_SerializesWorkspace(t *testing.T) {
	s := NewRunScheduler(SchedulerOptions{NodeCapacity: map[string]int{"node-a": 2}})
	if err := s.Enqueue("node-a", 1, "demo-a", "ws-1"); err != nil {
		t.Fatalf("enqueue 1 failed: %v", err)
	}
	if err := s.Enqueue("node-a", 2, "demo-b", "ws-1"); err != nil {
		t.Fatalf("enqueue 2 failed: %v", err)
	}
	runID, ok := s.ScheduleNext("node-a")
	if !ok || runID != 1 {
		t.Fatalf("expected run 1 scheduled, got=%d ok=%v", runID, ok)
	}
	if _, ok := s.ScheduleNext("node-a"); ok {
		t.Fatalf("expected workspace serialized")
	}
	s.Finish(1)
	runID, ok = s.ScheduleNext("node-a")
	if !ok || runID != 2 {
		t.Fatalf("expected run 2 scheduled after finish, got=%d ok=%v", runID, ok)
	}
}

func TestRunScheduler_RespectsNodeCapacity(t *testing.T) {
	s := NewRunScheduler(SchedulerOptions{NodeCapacity: map[string]int{"node-a": 1}})
	if err := s.Enqueue("node-a", 10, "demo-a", "ws-10"); err != nil {
		t.Fatalf("enqueue 10 failed: %v", err)
	}
	if err := s.Enqueue("node-a", 11, "demo-b", "ws-11"); err != nil {
		t.Fatalf("enqueue 11 failed: %v", err)
	}
	runID, ok := s.ScheduleNext("node-a")
	if !ok || runID != 10 {
		t.Fatalf("expected first scheduled run=10, got=%d ok=%v", runID, ok)
	}
	if _, ok := s.ScheduleNext("node-a"); ok {
		t.Fatalf("expected capacity limit to block second run")
	}
	s.Finish(10)
	runID, ok = s.ScheduleNext("node-a")
	if !ok || runID != 11 {
		t.Fatalf("expected second run scheduled after finish, got=%d ok=%v", runID, ok)
	}
}

func TestRunScheduler_AllowsParallelWorkspaces(t *testing.T) {
	s := NewRunScheduler(SchedulerOptions{NodeCapacity: map[string]int{"node-a": 2}})
	if err := s.Enqueue("node-a", 20, "demo-a", "ws-20"); err != nil {
		t.Fatalf("enqueue 20 failed: %v", err)
	}
	if err := s.Enqueue("node-a", 21, "demo-b", "ws-21"); err != nil {
		t.Fatalf("enqueue 21 failed: %v", err)
	}
	runID1, ok := s.ScheduleNext("node-a")
	if !ok {
		t.Fatalf("expected first schedule")
	}
	runID2, ok := s.ScheduleNext("node-a")
	if !ok {
		t.Fatalf("expected second schedule")
	}
	if runID1 == runID2 {
		t.Fatalf("expected distinct runs, got=%d", runID1)
	}
}

func TestRunScheduler_Acquire_BlocksOnNodeCapacityUntilFinish(t *testing.T) {
	s := NewRunScheduler(SchedulerOptions{NodeCapacity: map[string]int{"node-a": 1}})

	if err := s.Acquire(context.Background(), "node-a", 31, "demo-a", "ws-31"); err != nil {
		t.Fatalf("Acquire first run failed: %v", err)
	}

	secondAcquired := make(chan error, 1)
	go func() {
		secondAcquired <- s.Acquire(context.Background(), "node-a", 32, "demo-b", "ws-32")
	}()

	select {
	case err := <-secondAcquired:
		t.Fatalf("second acquire should block before finish, err=%v", err)
	case <-time.After(30 * time.Millisecond):
	}

	s.Finish(31)

	select {
	case err := <-secondAcquired:
		if err != nil {
			t.Fatalf("second acquire should succeed after finish: %v", err)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatalf("second acquire did not unblock after finish")
	}
}

func TestRunScheduler_Acquire_BlocksSameWorkspaceEvenWithCapacity(t *testing.T) {
	s := NewRunScheduler(SchedulerOptions{NodeCapacity: map[string]int{"node-a": 2}})

	if err := s.Acquire(context.Background(), "node-a", 41, "demo-a", "ws-shared"); err != nil {
		t.Fatalf("Acquire first run failed: %v", err)
	}

	secondAcquired := make(chan error, 1)
	go func() {
		secondAcquired <- s.Acquire(context.Background(), "node-a", 42, "demo-b", "ws-shared")
	}()

	select {
	case err := <-secondAcquired:
		t.Fatalf("second acquire should block on shared workspace before finish, err=%v", err)
	case <-time.After(30 * time.Millisecond):
	}

	s.Finish(41)

	select {
	case err := <-secondAcquired:
		if err != nil {
			t.Fatalf("second acquire should succeed after workspace released: %v", err)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatalf("second acquire did not unblock after workspace release")
	}
}

func TestRunScheduler_Acquire_RemovesQueuedRunOnContextCancel(t *testing.T) {
	s := NewRunScheduler(SchedulerOptions{NodeCapacity: map[string]int{"node-a": 1}})

	if err := s.Acquire(context.Background(), "node-a", 51, "demo-a", "ws-51"); err != nil {
		t.Fatalf("Acquire first run failed: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()

	secondAcquired := make(chan error, 1)
	go func() {
		secondAcquired <- s.Acquire(ctx, "node-a", 52, "demo-b", "ws-52")
	}()

	select {
	case err := <-secondAcquired:
		if err == nil {
			t.Fatalf("expected context cancellation error")
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatalf("second acquire did not return after context cancellation")
	}

	s.Finish(51)

	if err := s.Acquire(context.Background(), "node-a", 53, "demo-c", "ws-53"); err != nil {
		t.Fatalf("Acquire third run failed after canceled queue removed: %v", err)
	}
}

func TestRunScheduler_DefaultProjectLimitLeavesOneSlotPerNode(t *testing.T) {
	s := NewRunScheduler(SchedulerOptions{NodeCapacity: map[string]int{"node-a": 3}})

	if err := s.Acquire(context.Background(), "node-a", 61, "demo-a", "ws-61"); err != nil {
		t.Fatalf("Acquire first run failed: %v", err)
	}
	if err := s.Acquire(context.Background(), "node-a", 62, "demo-a", "ws-62"); err != nil {
		t.Fatalf("Acquire second run failed: %v", err)
	}

	thirdAcquired := make(chan error, 1)
	go func() {
		thirdAcquired <- s.Acquire(context.Background(), "node-a", 63, "demo-a", "ws-63")
	}()

	select {
	case err := <-thirdAcquired:
		t.Fatalf("third acquire should block on default project limit before finish, err=%v", err)
	case <-time.After(30 * time.Millisecond):
	}

	s.Finish(61)

	select {
	case err := <-thirdAcquired:
		if err != nil {
			t.Fatalf("third acquire should succeed after same-project slot released: %v", err)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatalf("third acquire did not unblock after same-project slot released")
	}
}
