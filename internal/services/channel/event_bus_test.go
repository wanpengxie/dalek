package channel

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"dalek/internal/store"
)

func TestEventBus_FilterByProjectAndConversation(t *testing.T) {
	bus := NewEventBus()
	subExact, unsubExact := bus.Subscribe("alpha", "conv-1", 4)
	defer unsubExact()
	subAll, unsubAll := bus.Subscribe("*", "*", 4)
	defer unsubAll()

	bus.Publish(GatewayEvent{ProjectName: "alpha", ConversationID: "conv-1", Type: "assistant_message", JobStatus: store.ChannelTurnSucceeded})
	mustReceiveEvent(t, subExact, "exact should receive alpha/conv-1")
	mustReceiveEvent(t, subAll, "wildcard should receive alpha/conv-1")

	bus.Publish(GatewayEvent{ProjectName: "alpha", ConversationID: "conv-2", Type: "assistant_message", JobStatus: store.ChannelTurnSucceeded})
	mustNotReceiveEvent(t, subExact, "exact should ignore conv-2")
	mustReceiveEvent(t, subAll, "wildcard should receive conv-2")

	bus.Publish(GatewayEvent{ProjectName: "beta", ConversationID: "conv-1", Type: "assistant_message", JobStatus: store.ChannelTurnSucceeded})
	mustNotReceiveEvent(t, subExact, "exact should ignore beta")
	mustReceiveEvent(t, subAll, "wildcard should receive beta")
}

func TestEventBus_Unsubscribe(t *testing.T) {
	bus := NewEventBus()
	sub, unsubscribe := bus.Subscribe("alpha", "conv-1", 1)
	unsubscribe()

	_, ok := <-sub
	if ok {
		t.Fatalf("channel should be closed after unsubscribe")
	}
}

func TestEventBus_Close_IdempotentAndRejectsSubscribe(t *testing.T) {
	bus := NewEventBus()
	sub, unsubscribe := bus.Subscribe("alpha", "conv-1", 1)
	defer unsubscribe()

	bus.Close()
	bus.Close()

	select {
	case _, ok := <-sub:
		if ok {
			t.Fatalf("subscriber channel should be closed after bus close")
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("wait subscriber close timeout")
	}

	afterClose, afterUnsubscribe := bus.Subscribe("alpha", "conv-1", 1)
	defer afterUnsubscribe()
	select {
	case _, ok := <-afterClose:
		if ok {
			t.Fatalf("subscribe after close should return closed channel")
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("subscribe after close should close channel immediately")
	}

	// 关闭后 publish 应直接丢弃且不 panic。
	bus.Publish(GatewayEvent{ProjectName: "alpha", ConversationID: "conv-1", Type: "assistant_message"})
}

func TestEventBus_ConcurrentPublishAndUnsubscribe_NoPanic(t *testing.T) {
	bus := NewEventBus()
	_, unsubscribe := bus.Subscribe("alpha", "conv-1", 1)

	panicCh := make(chan any, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer func() {
			if r := recover(); r != nil {
				panicCh <- r
			}
		}()
		for i := 0; i < 2000; i++ {
			bus.Publish(GatewayEvent{
				ProjectName:    "alpha",
				ConversationID: "conv-1",
				Type:           "assistant_event",
				At:             time.Now(),
			})
		}
	}()

	time.Sleep(1 * time.Millisecond)
	unsubscribe()

	<-done
	select {
	case r := <-panicCh:
		t.Fatalf("publish should not panic after unsubscribe, panic=%v", r)
	default:
	}
}

func TestEventBus_AuditWriteError_LogsAndDoesNotBlockPublish(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "event-bus.db")
	db, err := store.OpenAndMigrate(dbPath)
	if err != nil {
		t.Fatalf("open db failed: %v", err)
	}
	if err := db.Exec("DROP TABLE event_bus_logs").Error; err != nil {
		t.Fatalf("drop event_bus_logs failed: %v", err)
	}

	var auditErr bytes.Buffer
	bus := newEventBusWithAuditAndErrWriter(db, &auditErr)
	sub, unsubscribe := bus.Subscribe("alpha", "conv-1", 2)
	defer unsubscribe()

	bus.Publish(GatewayEvent{
		ProjectName:    "alpha",
		ConversationID: "conv-1",
		PeerMessageID:  "msg-1",
		Type:           "assistant_message",
		JobStatus:      store.ChannelTurnSucceeded,
	})
	_ = mustReceiveEvent(t, sub, "publish should continue even if audit write failed")

	got := auditErr.String()
	if !strings.Contains(got, "event bus audit write failed") {
		t.Fatalf("missing audit write error log, got=%q", got)
	}
	if !strings.Contains(got, "project=alpha") || !strings.Contains(got, "conversation=conv-1") {
		t.Fatalf("audit write error log should include event context, got=%q", got)
	}
}

func mustReceiveEvent(t *testing.T, ch <-chan GatewayEvent, msg string) GatewayEvent {
	t.Helper()
	select {
	case ev := <-ch:
		return ev
	case <-time.After(500 * time.Millisecond):
		t.Fatal(msg)
		return GatewayEvent{}
	}
}

func mustNotReceiveEvent(t *testing.T, ch <-chan GatewayEvent, msg string) {
	t.Helper()
	select {
	case ev := <-ch:
		t.Fatalf("%s, unexpected=%+v", msg, ev)
	case <-time.After(120 * time.Millisecond):
	}
}
