package pm

import (
	"context"
	"errors"
	"runtime"
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

func TestNewCancelOnlyContext_NilParent(t *testing.T) {
	child, childCancel := newCancelOnlyContext(nil)
	defer childCancel()

	select {
	case <-child.Done():
		t.Fatalf("nil parent child should not be done")
	default:
	}
}

func TestNewCancelOnlyContext_ChildCancelDoesNotAffectParent(t *testing.T) {
	parent, parentCancel := context.WithCancel(context.Background())
	defer parentCancel()

	_, childCancel := newCancelOnlyContext(parent)
	childCancel()

	select {
	case <-parent.Done():
		t.Fatalf("cancelling child should not cancel parent")
	case <-time.After(30 * time.Millisecond):
	}
}

func TestNewCancelOnlyContext_NoGoroutineLeak(t *testing.T) {
	// Force GC and stabilize goroutine count.
	runtime.GC()
	time.Sleep(10 * time.Millisecond)
	before := runtime.NumGoroutine()

	const iterations = 50
	for i := 0; i < iterations; i++ {
		parent, parentCancel := context.WithCancel(context.Background())
		_, childCancel := newCancelOnlyContext(parent)
		childCancel()
		parentCancel()
	}

	// Allow any internal cleanup to finish.
	runtime.GC()
	time.Sleep(50 * time.Millisecond)

	after := runtime.NumGoroutine()
	// Allow a small margin for other runtime goroutines, but 50 leaked goroutines would be obvious.
	leaked := after - before
	if leaked > 5 {
		t.Fatalf("possible goroutine leak: before=%d after=%d leaked=%d (created %d contexts)", before, after, leaked, iterations)
	}
}

func TestNewCancelOnlyContext_NoGoroutineLeak_ParentDeadline(t *testing.T) {
	runtime.GC()
	time.Sleep(10 * time.Millisecond)
	before := runtime.NumGoroutine()

	const iterations = 50
	for i := 0; i < iterations; i++ {
		parent, parentCancel := context.WithTimeout(context.Background(), time.Millisecond)
		_, childCancel := newCancelOnlyContext(parent)
		<-parent.Done() // wait for deadline
		childCancel()
		parentCancel()
	}

	runtime.GC()
	time.Sleep(50 * time.Millisecond)

	after := runtime.NumGoroutine()
	leaked := after - before
	if leaked > 5 {
		t.Fatalf("possible goroutine leak on deadline: before=%d after=%d leaked=%d", before, after, leaked)
	}
}
