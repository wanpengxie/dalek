package channel

import (
	"errors"
	"testing"
	"time"

	"dalek/internal/contracts"
)

func TestInboundQueue_ProjectPartitionAndFull(t *testing.T) {
	q := NewInboundQueue(1)
	item := InboundItem{
		ProjectName: "alpha",
		Envelope: contracts.InboundEnvelope{
			Schema:             contracts.ChannelInboundSchemaV1,
			ChannelType:        contracts.ChannelTypeCLI,
			Adapter:            "cli.local",
			PeerMessageID:      "msg-1",
			PeerConversationID: "conv-1",
			SenderID:           "u1",
			Text:               "hello",
			ReceivedAt:         "2026-02-19T13:00:00Z",
		},
	}

	if err := q.Enqueue("alpha", item); err != nil {
		t.Fatalf("first enqueue failed: %v", err)
	}
	if err := q.Enqueue("alpha", item); !errors.Is(err, ErrInboundQueueFull) {
		t.Fatalf("second enqueue should be queue full, got: %v", err)
	}
	if err := q.Enqueue("beta", item); err != nil {
		t.Fatalf("beta enqueue should not be blocked by alpha: %v", err)
	}
}

func TestInboundQueue_GetOrCreateValidation(t *testing.T) {
	q := NewInboundQueue(2)
	if _, _, err := q.GetOrCreate(""); err == nil {
		t.Fatalf("empty project should fail")
	}
	ch1, created1, err := q.GetOrCreate("demo")
	if err != nil {
		t.Fatalf("first get/create failed: %v", err)
	}
	if !created1 || ch1 == nil {
		t.Fatalf("first get/create should create channel")
	}
	ch2, created2, err := q.GetOrCreate("demo")
	if err != nil {
		t.Fatalf("second get/create failed: %v", err)
	}
	if created2 {
		t.Fatalf("second get/create should not create new channel")
	}
	if ch1 != ch2 {
		t.Fatalf("same project should return same channel")
	}
}

func TestInboundQueue_Close_IdempotentAndRejectsNewQueue(t *testing.T) {
	q := NewInboundQueue(2)
	ch, created, err := q.GetOrCreate("alpha")
	if err != nil {
		t.Fatalf("get/create failed: %v", err)
	}
	if !created || ch == nil {
		t.Fatalf("expected created queue channel")
	}

	q.Close()
	q.Close()

	select {
	case _, ok := <-ch:
		if ok {
			t.Fatalf("queue channel should be closed after Close")
		}
	case <-time.After(300 * time.Millisecond):
		t.Fatalf("wait queue close timeout")
	}

	if _, _, err := q.GetOrCreate("beta"); !errors.Is(err, ErrInboundQueueClosed) {
		t.Fatalf("GetOrCreate after close should return ErrInboundQueueClosed, got=%v", err)
	}
	if err := q.Enqueue("beta", InboundItem{ProjectName: "beta"}); !errors.Is(err, ErrInboundQueueClosed) {
		t.Fatalf("Enqueue after close should return ErrInboundQueueClosed, got=%v", err)
	}
}

func TestInboundQueue_ReplaceProjectQueue(t *testing.T) {
	q := NewInboundQueue(2)
	orig, created, err := q.GetOrCreate("alpha")
	if err != nil {
		t.Fatalf("GetOrCreate failed: %v", err)
	}
	if !created || orig == nil {
		t.Fatalf("expected initial queue channel created")
	}

	oldCh, newCh, err := q.Replace("alpha")
	if err != nil {
		t.Fatalf("Replace failed: %v", err)
	}
	if oldCh != orig {
		t.Fatalf("replace should return old queue channel")
	}
	if newCh == nil || newCh == oldCh {
		t.Fatalf("replace should create a new queue channel")
	}

	got, createdAgain, err := q.GetOrCreate("alpha")
	if err != nil {
		t.Fatalf("GetOrCreate after replace failed: %v", err)
	}
	if createdAgain {
		t.Fatalf("queue should already exist after replace")
	}
	if got != newCh {
		t.Fatalf("expected replaced queue channel")
	}
}
