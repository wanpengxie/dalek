package pm

import (
	"context"
	"errors"
	"testing"
	"time"
)

type ctxValueKey string

func TestNewCancelOnlyContext_IgnoresParentDeadline(t *testing.T) {
	parent, parentCancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer parentCancel()

	child, childCancel := newCancelOnlyContext(parent)
	defer childCancel()

	<-parent.Done()
	if !errors.Is(parent.Err(), context.DeadlineExceeded) {
		t.Fatalf("expected parent deadline exceeded, got=%v", parent.Err())
	}
	select {
	case <-child.Done():
		t.Fatalf("child context should ignore parent deadline")
	case <-time.After(30 * time.Millisecond):
	}
}

func TestNewCancelOnlyContext_PropagatesParentCancel(t *testing.T) {
	parent, parentCancel := context.WithCancel(context.Background())
	child, childCancel := newCancelOnlyContext(parent)
	defer childCancel()

	parentCancel()

	select {
	case <-child.Done():
	case <-time.After(200 * time.Millisecond):
		t.Fatalf("child context should receive parent cancel")
	}
	if !errors.Is(child.Err(), context.Canceled) {
		t.Fatalf("expected child err=context canceled, got=%v", child.Err())
	}
}

func TestNewCancelOnlyContext_InheritsParentValues(t *testing.T) {
	parent := context.WithValue(context.Background(), ctxValueKey("trace_id"), "trace-123")
	child, childCancel := newCancelOnlyContext(parent)
	defer childCancel()

	got := child.Value(ctxValueKey("trace_id"))
	if got != "trace-123" {
		t.Fatalf("expected inherited value trace-123, got=%v", got)
	}
}
