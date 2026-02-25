package channel

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestEventDeduplicator_IsDuplicate_Basic(t *testing.T) {
	dedup := NewEventDeduplicator(5*time.Minute, 128)
	if dedup.IsDuplicate("evt-1") {
		t.Fatalf("first event should not be duplicate")
	}
	if !dedup.IsDuplicate("evt-1") {
		t.Fatalf("second same event should be duplicate")
	}
	if dedup.IsDuplicate("   ") {
		t.Fatalf("blank event id should not be duplicate")
	}
}

func TestEventDeduplicator_IsDuplicate_TTLExpire(t *testing.T) {
	dedup := NewEventDeduplicator(2*time.Second, 128)
	now := time.Unix(1_700_000_000, 0)
	dedup.now = func() time.Time { return now }

	if dedup.IsDuplicate("evt-expire") {
		t.Fatalf("first event should not be duplicate")
	}
	now = now.Add(time.Second)
	if !dedup.IsDuplicate("evt-expire") {
		t.Fatalf("event should be duplicate within ttl")
	}
	now = now.Add(2 * time.Second)
	if dedup.IsDuplicate("evt-expire") {
		t.Fatalf("event should expire after ttl")
	}
}

func TestEventDeduplicator_IsDuplicate_CapacityEvict(t *testing.T) {
	dedup := NewEventDeduplicator(time.Minute, 2)

	if dedup.IsDuplicate("evt-1") {
		t.Fatalf("evt-1 first hit should not be duplicate")
	}
	if dedup.IsDuplicate("evt-2") {
		t.Fatalf("evt-2 first hit should not be duplicate")
	}
	if dedup.IsDuplicate("evt-3") {
		t.Fatalf("evt-3 first hit should not be duplicate")
	}

	// 容量为 2，插入第三个后最旧的 evt-1 应被淘汰。
	if dedup.IsDuplicate("evt-1") {
		t.Fatalf("evt-1 should have been evicted")
	}
}

func TestEventDeduplicator_IsDuplicate_Concurrent(t *testing.T) {
	dedup := NewEventDeduplicator(time.Minute, 64)
	var dupCount atomic.Int64

	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if dedup.IsDuplicate("evt-concurrent") {
				dupCount.Add(1)
			}
		}()
	}
	wg.Wait()

	if got := dupCount.Load(); got != 31 {
		t.Fatalf("duplicate count mismatch: got=%d want=31", got)
	}
}
