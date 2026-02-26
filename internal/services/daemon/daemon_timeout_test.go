package daemon

import (
	"context"
	"sync"
	"testing"
	"time"
)

type testStopComponent struct {
	name   string
	stopFn func(ctx context.Context) error
}

func (c *testStopComponent) Name() string {
	if c == nil {
		return ""
	}
	return c.name
}

func (c *testStopComponent) Start(ctx context.Context) error {
	_ = ctx
	return nil
}

func (c *testStopComponent) Stop(ctx context.Context) error {
	if c == nil || c.stopFn == nil {
		return nil
	}
	return c.stopFn(ctx)
}

func TestStopComponentsWithTimeout_PerComponentBudgetPreventsStarvation(t *testing.T) {
	var followerBudget time.Duration
	slow := &testStopComponent{
		name: "slow",
		stopFn: func(ctx context.Context) error {
			<-ctx.Done()
			return ctx.Err()
		},
	}
	follower := &testStopComponent{
		name: "follower",
		stopFn: func(ctx context.Context) error {
			deadline, ok := ctx.Deadline()
			if !ok {
				t.Fatalf("follower stop context should have deadline")
			}
			followerBudget = time.Until(deadline)
			return nil
		},
	}

	stopComponentsWithTimeout(nil, []Component{follower, slow}, 240*time.Millisecond)

	if followerBudget <= 0 {
		t.Fatalf("follower should get positive stop window, got=%s", followerBudget)
	}
	if followerBudget < 60*time.Millisecond {
		t.Fatalf("follower stop window too small, got=%s", followerBudget)
	}
	if followerBudget > 170*time.Millisecond {
		t.Fatalf("follower stop window should be capped, got=%s", followerBudget)
	}
}

func TestStopComponentsWithTimeout_AssignsIndependentCaps(t *testing.T) {
	var (
		mu      sync.Mutex
		budgets []time.Duration
	)
	makeBlocking := func(name string) Component {
		return &testStopComponent{
			name: name,
			stopFn: func(ctx context.Context) error {
				deadline, ok := ctx.Deadline()
				if !ok {
					t.Fatalf("component %s stop context should have deadline", name)
				}
				mu.Lock()
				budgets = append(budgets, time.Until(deadline))
				mu.Unlock()

				<-ctx.Done()
				return ctx.Err()
			},
		}
	}

	stopComponentsWithTimeout(nil, []Component{
		makeBlocking("c1"),
		makeBlocking("c2"),
		makeBlocking("c3"),
	}, 300*time.Millisecond)

	mu.Lock()
	got := append([]time.Duration(nil), budgets...)
	mu.Unlock()
	if len(got) != 3 {
		t.Fatalf("expected 3 component budgets, got=%d", len(got))
	}
	for i, budget := range got {
		if budget <= 0 {
			t.Fatalf("component #%d budget should be positive, got=%s", i+1, budget)
		}
		if budget > 160*time.Millisecond {
			t.Fatalf("component #%d budget should be capped, got=%s", i+1, budget)
		}
	}
}

func TestStopComponentsWithTimeout_UncooperativeComponentDoesNotBlockFollower(t *testing.T) {
	blockCh := make(chan struct{})
	slowStarted := make(chan struct{})
	followerCalled := make(chan struct{}, 1)
	var slowWG sync.WaitGroup
	slowWG.Add(1)

	slow := &testStopComponent{
		name: "slow_blocking",
		stopFn: func(ctx context.Context) error {
			_ = ctx
			defer slowWG.Done()
			close(slowStarted)
			<-blockCh
			return nil
		},
	}
	follower := &testStopComponent{
		name: "follower",
		stopFn: func(ctx context.Context) error {
			_ = ctx
			followerCalled <- struct{}{}
			return nil
		},
	}

	startedAt := time.Now()
	stopComponentsWithTimeout(nil, []Component{follower, slow}, 200*time.Millisecond)
	elapsed := time.Since(startedAt)

	select {
	case <-slowStarted:
	default:
		t.Fatalf("slow component should be invoked")
	}
	select {
	case <-followerCalled:
	default:
		t.Fatalf("follower should still be invoked when previous component blocks")
	}
	if elapsed > 450*time.Millisecond {
		t.Fatalf("stopComponentsWithTimeout should not be blocked by uncooperative component, elapsed=%s", elapsed)
	}

	close(blockCh)
	done := make(chan struct{})
	go func() {
		defer close(done)
		slowWG.Wait()
	}()
	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("slow component goroutine should exit after unblock")
	}
}
