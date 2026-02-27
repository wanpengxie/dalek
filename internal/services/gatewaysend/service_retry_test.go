package gatewaysend

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"dalek/internal/contracts"
)

type flakySender struct {
	mu sync.Mutex

	cardErr  error
	textErr  error
	cardCall int
	textCall int
}

func (s *flakySender) SendCard(ctx context.Context, chatID, title, markdown string) error {
	_ = ctx
	_ = chatID
	_ = title
	_ = markdown
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cardCall++
	return s.cardErr
}

func (s *flakySender) SendText(ctx context.Context, chatID, text string) error {
	_ = ctx
	_ = chatID
	_ = text
	s.mu.Lock()
	defer s.mu.Unlock()
	s.textCall++
	return s.textErr
}

func (s *flakySender) snapshot() (card, text int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cardCall, s.textCall
}

func TestSend_TextFallback(t *testing.T) {
	db := openGatewayDBForRepositoryTest(t)
	_ = createRepositoryTestBinding(t, db, "demo", "chat-fallback")

	sender := &flakySender{
		cardErr: fmt.Errorf("card failed"),
	}
	svc := NewServiceWithDB(db, nil, sender, nil)
	resp, err := svc.Send(context.Background(), "demo", "fallback content")
	if err != nil {
		t.Fatalf("Send failed: %v", err)
	}
	if resp.Delivered != 1 || resp.Failed != 0 || len(resp.Results) != 1 {
		t.Fatalf("unexpected response: %+v", resp)
	}
	cardCalls, textCalls := sender.snapshot()
	if cardCalls != 1 || textCalls != 1 {
		t.Fatalf("unexpected sender calls: card=%d text=%d", cardCalls, textCalls)
	}

	var outbox contracts.ChannelOutbox
	if err := db.First(&outbox, resp.Results[0].OutboxID).Error; err != nil {
		t.Fatalf("query outbox failed: %v", err)
	}
	if outbox.Status != contracts.ChannelOutboxSent {
		t.Fatalf("outbox status should be sent, got=%s", outbox.Status)
	}
	if outbox.NextRetryAt != nil {
		t.Fatalf("fallback success should clear next_retry_at, got=%v", outbox.NextRetryAt)
	}
}

func TestSend_RetryableFailureSetsNextRetryAt(t *testing.T) {
	db := openGatewayDBForRepositoryTest(t)
	_ = createRepositoryTestBinding(t, db, "demo", "chat-retryable")

	base := time.Date(2026, 2, 27, 15, 0, 0, 0, time.UTC)
	svc := NewServiceWithDB(db, nil, &flakySender{
		cardErr: fmt.Errorf("card down"),
		textErr: fmt.Errorf("text down"),
	}, nil)
	svc.policy = RetryPolicy{
		MaxRetries:     3,
		InitialBackoff: 2 * time.Second,
		MaxBackoff:     time.Minute,
		BackoffFactor:  2,
	}
	svc.now = func() time.Time { return base }

	resp, err := svc.Send(context.Background(), "demo", "need retry")
	if err != nil {
		t.Fatalf("Send failed: %v", err)
	}
	if resp.Delivered != 0 || resp.Failed != 1 || len(resp.Results) != 1 {
		t.Fatalf("unexpected response: %+v", resp)
	}

	var outbox contracts.ChannelOutbox
	if err := db.First(&outbox, resp.Results[0].OutboxID).Error; err != nil {
		t.Fatalf("query outbox failed: %v", err)
	}
	if outbox.Status != contracts.ChannelOutboxFailed {
		t.Fatalf("outbox status should be failed, got=%s", outbox.Status)
	}
	if outbox.NextRetryAt == nil {
		t.Fatalf("retryable failure should set next_retry_at")
	}
	expected := base.Add(2 * time.Second)
	if !outbox.NextRetryAt.Equal(expected) {
		t.Fatalf("next_retry_at mismatch: got=%s want=%s", outbox.NextRetryAt.Format(time.RFC3339Nano), expected.Format(time.RFC3339Nano))
	}
	if outbox.RetryCount != 1 {
		t.Fatalf("retry_count should be 1 after first attempt, got=%d", outbox.RetryCount)
	}
	if !strings.Contains(outbox.LastError, "card down") || !strings.Contains(outbox.LastError, "text down") {
		t.Fatalf("last_error should include card/text errors, got=%q", outbox.LastError)
	}
}

func TestSend_ExhaustedRetryMarksDead(t *testing.T) {
	db := openGatewayDBForRepositoryTest(t)
	_ = createRepositoryTestBinding(t, db, "demo", "chat-dead")

	svc := NewServiceWithDB(db, nil, &flakySender{
		cardErr: fmt.Errorf("card failed"),
		textErr: fmt.Errorf("text failed"),
	}, nil)
	svc.policy = RetryPolicy{
		MaxRetries:     1,
		InitialBackoff: time.Second,
		MaxBackoff:     time.Second,
		BackoffFactor:  2,
	}

	resp, err := svc.Send(context.Background(), "demo", "dead letter")
	if err != nil {
		t.Fatalf("Send failed: %v", err)
	}
	if resp.Delivered != 0 || resp.Failed != 1 || len(resp.Results) != 1 {
		t.Fatalf("unexpected response: %+v", resp)
	}
	var outbox contracts.ChannelOutbox
	if err := db.First(&outbox, resp.Results[0].OutboxID).Error; err != nil {
		t.Fatalf("query outbox failed: %v", err)
	}
	if outbox.Status != contracts.ChannelOutboxDead {
		t.Fatalf("outbox status should be dead, got=%s", outbox.Status)
	}
	if outbox.NextRetryAt != nil {
		t.Fatalf("dead outbox should clear next_retry_at, got=%v", outbox.NextRetryAt)
	}
}

func TestSweeper_RunOnce_PickupAndRetry(t *testing.T) {
	db := openGatewayDBForRepositoryTest(t)
	binding := createRepositoryTestBinding(t, db, "demo", "chat-sweeper-success")
	repo := NewGormRepository(db)
	state, err := repo.CreatePending(context.Background(), binding, "demo", "retry once")
	if err != nil {
		t.Fatalf("CreatePending failed: %v", err)
	}
	if err := repo.MarkSending(context.Background(), state.outbox.ID); err != nil {
		t.Fatalf("MarkSending failed: %v", err)
	}
	if err := repo.MarkFailedRetryable(context.Background(), state, fmt.Errorf("temporary error"), time.Now().Add(-time.Minute)); err != nil {
		t.Fatalf("MarkFailedRetryable failed: %v", err)
	}

	sender := &flakySender{}
	sw := NewSweeper(repo, sender, nil, nil, SweeperOptions{
		RetryPolicy: RetryPolicy{
			MaxRetries:     5,
			InitialBackoff: time.Second,
			MaxBackoff:     time.Minute,
			BackoffFactor:  2,
		},
	})
	n, err := sw.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce failed: %v", err)
	}
	if n != 1 {
		t.Fatalf("RunOnce should process 1 item, got=%d", n)
	}
	cardCalls, textCalls := sender.snapshot()
	if cardCalls != 1 || textCalls != 0 {
		t.Fatalf("unexpected sender calls: card=%d text=%d", cardCalls, textCalls)
	}

	var outbox contracts.ChannelOutbox
	if err := db.First(&outbox, state.outbox.ID).Error; err != nil {
		t.Fatalf("query outbox failed: %v", err)
	}
	if outbox.Status != contracts.ChannelOutboxSent {
		t.Fatalf("outbox status should be sent, got=%s", outbox.Status)
	}
	if outbox.RetryCount != 2 {
		t.Fatalf("retry_count should be incremented on retry, got=%d", outbox.RetryCount)
	}
}

func TestSweeper_RunOnce_PickupPending(t *testing.T) {
	db := openGatewayDBForRepositoryTest(t)
	binding := createRepositoryTestBinding(t, db, "demo", "chat-sweeper-pending")
	repo := NewGormRepository(db)
	state, err := repo.CreatePending(context.Background(), binding, "demo", "pending once")
	if err != nil {
		t.Fatalf("CreatePending failed: %v", err)
	}

	sender := &flakySender{}
	sw := NewSweeper(repo, sender, nil, nil, SweeperOptions{
		RetryPolicy: RetryPolicy{
			MaxRetries:     5,
			InitialBackoff: time.Second,
			MaxBackoff:     time.Minute,
			BackoffFactor:  2,
		},
	})
	n, err := sw.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce failed: %v", err)
	}
	if n != 1 {
		t.Fatalf("RunOnce should process 1 item, got=%d", n)
	}
	cardCalls, textCalls := sender.snapshot()
	if cardCalls != 1 || textCalls != 0 {
		t.Fatalf("unexpected sender calls: card=%d text=%d", cardCalls, textCalls)
	}

	var outbox contracts.ChannelOutbox
	if err := db.First(&outbox, state.outbox.ID).Error; err != nil {
		t.Fatalf("query outbox failed: %v", err)
	}
	if outbox.Status != contracts.ChannelOutboxSent {
		t.Fatalf("outbox status should be sent, got=%s", outbox.Status)
	}
	if outbox.RetryCount != 1 {
		t.Fatalf("retry_count should be 1 after first delivery, got=%d", outbox.RetryCount)
	}
}

func TestSweeper_RunOnce_ExhaustedToDead(t *testing.T) {
	db := openGatewayDBForRepositoryTest(t)
	binding := createRepositoryTestBinding(t, db, "demo", "chat-sweeper-dead")
	repo := NewGormRepository(db)
	state, err := repo.CreatePending(context.Background(), binding, "demo", "retry dead")
	if err != nil {
		t.Fatalf("CreatePending failed: %v", err)
	}
	if err := repo.MarkSending(context.Background(), state.outbox.ID); err != nil {
		t.Fatalf("MarkSending failed: %v", err)
	}
	if err := repo.MarkFailedRetryable(context.Background(), state, fmt.Errorf("temporary error"), time.Now().Add(-time.Minute)); err != nil {
		t.Fatalf("MarkFailedRetryable failed: %v", err)
	}

	sw := NewSweeper(repo, &flakySender{
		cardErr: fmt.Errorf("card failed"),
		textErr: fmt.Errorf("text failed"),
	}, nil, nil, SweeperOptions{
		RetryPolicy: RetryPolicy{
			MaxRetries:     2,
			InitialBackoff: time.Second,
			MaxBackoff:     time.Minute,
			BackoffFactor:  2,
		},
	})
	n, err := sw.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce failed: %v", err)
	}
	if n != 1 {
		t.Fatalf("RunOnce should process 1 item, got=%d", n)
	}

	var outbox contracts.ChannelOutbox
	if err := db.First(&outbox, state.outbox.ID).Error; err != nil {
		t.Fatalf("query outbox failed: %v", err)
	}
	if outbox.Status != contracts.ChannelOutboxDead {
		t.Fatalf("outbox status should be dead, got=%s", outbox.Status)
	}
	if outbox.RetryCount != 2 {
		t.Fatalf("retry_count should be 2 after exhausted retry, got=%d", outbox.RetryCount)
	}
}
