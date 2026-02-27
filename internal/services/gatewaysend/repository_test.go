package gatewaysend

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"dalek/internal/contracts"
	"dalek/internal/store"

	"gorm.io/gorm"
)

func openGatewayDBForRepositoryTest(t *testing.T) *gorm.DB {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "gateway.db")
	db, err := store.OpenGatewayDB(dbPath)
	if err != nil {
		t.Fatalf("OpenGatewayDB failed: %v", err)
	}
	return db
}

func createRepositoryTestBinding(t *testing.T, db *gorm.DB, projectName, chatID string) contracts.ChannelBinding {
	t.Helper()
	binding := contracts.ChannelBinding{
		ProjectName:    strings.TrimSpace(projectName),
		ChannelType:    contracts.ChannelTypeIM,
		Adapter:        AdapterFeishu,
		PeerProjectKey: strings.TrimSpace(chatID),
		RolePolicyJSON: contracts.JSONMap{},
		Enabled:        true,
	}
	if err := db.Create(&binding).Error; err != nil {
		t.Fatalf("create binding failed: %v", err)
	}
	return binding
}

func TestRepository_CreatePendingAndMarkSent(t *testing.T) {
	db := openGatewayDBForRepositoryTest(t)
	binding := createRepositoryTestBinding(t, db, "demo", "chat-repo-1")
	repo := NewGormRepository(db)

	state, err := repo.CreatePending(context.Background(), binding, "demo", "deploy done")
	if err != nil {
		t.Fatalf("CreatePending failed: %v", err)
	}
	if state.conversation.ID == 0 || state.message.ID == 0 || state.outbox.ID == 0 {
		t.Fatalf("create pending should return persisted ids: %+v", state)
	}

	var pendingMsg contracts.ChannelMessage
	if err := db.First(&pendingMsg, state.message.ID).Error; err != nil {
		t.Fatalf("query message failed: %v", err)
	}
	if pendingMsg.Status != contracts.ChannelMessageProcessed {
		t.Fatalf("pending message status mismatch: %s", pendingMsg.Status)
	}
	var pendingOutbox contracts.ChannelOutbox
	if err := db.First(&pendingOutbox, state.outbox.ID).Error; err != nil {
		t.Fatalf("query outbox failed: %v", err)
	}
	if pendingOutbox.Status != contracts.ChannelOutboxPending {
		t.Fatalf("pending outbox status mismatch: %s", pendingOutbox.Status)
	}

	if err := repo.MarkSending(context.Background(), state.outbox.ID); err != nil {
		t.Fatalf("MarkSending failed: %v", err)
	}
	var sendingOutbox contracts.ChannelOutbox
	if err := db.First(&sendingOutbox, state.outbox.ID).Error; err != nil {
		t.Fatalf("query sending outbox failed: %v", err)
	}
	if sendingOutbox.Status != contracts.ChannelOutboxSending {
		t.Fatalf("sending outbox status mismatch: %s", sendingOutbox.Status)
	}
	if sendingOutbox.RetryCount != 1 {
		t.Fatalf("sending retry count mismatch: %d", sendingOutbox.RetryCount)
	}

	if err := repo.MarkSent(context.Background(), state); err != nil {
		t.Fatalf("MarkSent failed: %v", err)
	}
	var sentMsg contracts.ChannelMessage
	if err := db.First(&sentMsg, state.message.ID).Error; err != nil {
		t.Fatalf("query sent message failed: %v", err)
	}
	if sentMsg.Status != contracts.ChannelMessageSent {
		t.Fatalf("sent message status mismatch: %s", sentMsg.Status)
	}
	var sentOutbox contracts.ChannelOutbox
	if err := db.First(&sentOutbox, state.outbox.ID).Error; err != nil {
		t.Fatalf("query sent outbox failed: %v", err)
	}
	if sentOutbox.Status != contracts.ChannelOutboxSent {
		t.Fatalf("sent outbox status mismatch: %s", sentOutbox.Status)
	}
	var conv contracts.ChannelConversation
	if err := db.First(&conv, state.conversation.ID).Error; err != nil {
		t.Fatalf("query conversation failed: %v", err)
	}
	if conv.LastMessageAt == nil {
		t.Fatalf("conversation last_message_at should be updated")
	}
}

func TestRepository_FindRecentDuplicateDelivery(t *testing.T) {
	db := openGatewayDBForRepositoryTest(t)
	binding := createRepositoryTestBinding(t, db, "demo", "chat-repo-dedup")
	repo := NewGormRepository(db)

	state, err := repo.CreatePending(context.Background(), binding, "demo", "same text")
	if err != nil {
		t.Fatalf("CreatePending failed: %v", err)
	}
	if err := repo.MarkSending(context.Background(), state.outbox.ID); err != nil {
		t.Fatalf("MarkSending failed: %v", err)
	}
	if err := repo.MarkSent(context.Background(), state); err != nil {
		t.Fatalf("MarkSent failed: %v", err)
	}

	delivery, ok, err := repo.FindRecentDuplicateDelivery(context.Background(), binding, "same text")
	if err != nil {
		t.Fatalf("FindRecentDuplicateDelivery failed: %v", err)
	}
	if !ok {
		t.Fatalf("duplicate delivery should be found")
	}
	if delivery.MessageID != state.message.ID || delivery.OutboxID != state.outbox.ID {
		t.Fatalf("duplicate delivery id mismatch: %+v", delivery)
	}
	if delivery.Status != string(contracts.ChannelOutboxSent) {
		t.Fatalf("duplicate delivery status mismatch: %s", delivery.Status)
	}

	_, ok, err = repo.FindRecentDuplicateDelivery(context.Background(), binding, "different text")
	if err != nil {
		t.Fatalf("FindRecentDuplicateDelivery with different text failed: %v", err)
	}
	if ok {
		t.Fatalf("different text should not be deduped")
	}
}

func TestRepository_MarkFailed(t *testing.T) {
	db := openGatewayDBForRepositoryTest(t)
	binding := createRepositoryTestBinding(t, db, "demo", "chat-repo-fail")
	repo := NewGormRepository(db)

	state, err := repo.CreatePending(context.Background(), binding, "demo", "will fail")
	if err != nil {
		t.Fatalf("CreatePending failed: %v", err)
	}
	if err := repo.MarkFailed(context.Background(), state, fmt.Errorf("sender down")); err != nil {
		t.Fatalf("MarkFailed failed: %v", err)
	}

	var msg contracts.ChannelMessage
	if err := db.First(&msg, state.message.ID).Error; err != nil {
		t.Fatalf("query message failed: %v", err)
	}
	if msg.Status != contracts.ChannelMessageFailed {
		t.Fatalf("failed message status mismatch: %s", msg.Status)
	}
	var outbox contracts.ChannelOutbox
	if err := db.First(&outbox, state.outbox.ID).Error; err != nil {
		t.Fatalf("query outbox failed: %v", err)
	}
	if outbox.Status != contracts.ChannelOutboxFailed {
		t.Fatalf("failed outbox status mismatch: %s", outbox.Status)
	}
	if !strings.Contains(outbox.LastError, "sender down") {
		t.Fatalf("failed outbox last_error mismatch: %q", outbox.LastError)
	}
}

func TestRepository_MarkFailedRetryable(t *testing.T) {
	db := openGatewayDBForRepositoryTest(t)
	binding := createRepositoryTestBinding(t, db, "demo", "chat-repo-retryable")
	repo := NewGormRepository(db)

	state, err := repo.CreatePending(context.Background(), binding, "demo", "will retry")
	if err != nil {
		t.Fatalf("CreatePending failed: %v", err)
	}
	nextRetryAt := time.Now().Add(2 * time.Minute).Round(time.Second)
	if err := repo.MarkFailedRetryable(context.Background(), state, fmt.Errorf("temporary dns error"), nextRetryAt); err != nil {
		t.Fatalf("MarkFailedRetryable failed: %v", err)
	}

	var msg contracts.ChannelMessage
	if err := db.First(&msg, state.message.ID).Error; err != nil {
		t.Fatalf("query message failed: %v", err)
	}
	if msg.Status != contracts.ChannelMessageFailed {
		t.Fatalf("message status should be failed, got=%s", msg.Status)
	}
	var outbox contracts.ChannelOutbox
	if err := db.First(&outbox, state.outbox.ID).Error; err != nil {
		t.Fatalf("query outbox failed: %v", err)
	}
	if outbox.Status != contracts.ChannelOutboxFailed {
		t.Fatalf("outbox status should be failed, got=%s", outbox.Status)
	}
	if outbox.NextRetryAt == nil || !outbox.NextRetryAt.Round(time.Second).Equal(nextRetryAt) {
		t.Fatalf("next_retry_at mismatch: got=%v want=%v", outbox.NextRetryAt, nextRetryAt)
	}
	if !strings.Contains(outbox.LastError, "temporary dns error") {
		t.Fatalf("outbox last_error mismatch: %q", outbox.LastError)
	}
}

func TestRepository_MarkDead(t *testing.T) {
	db := openGatewayDBForRepositoryTest(t)
	binding := createRepositoryTestBinding(t, db, "demo", "chat-repo-dead")
	repo := NewGormRepository(db)

	state, err := repo.CreatePending(context.Background(), binding, "demo", "dead letter")
	if err != nil {
		t.Fatalf("CreatePending failed: %v", err)
	}
	if err := repo.MarkDead(context.Background(), state, fmt.Errorf("retry exhausted")); err != nil {
		t.Fatalf("MarkDead failed: %v", err)
	}

	var msg contracts.ChannelMessage
	if err := db.First(&msg, state.message.ID).Error; err != nil {
		t.Fatalf("query message failed: %v", err)
	}
	if msg.Status != contracts.ChannelMessageFailed {
		t.Fatalf("message status should be failed, got=%s", msg.Status)
	}
	var outbox contracts.ChannelOutbox
	if err := db.First(&outbox, state.outbox.ID).Error; err != nil {
		t.Fatalf("query outbox failed: %v", err)
	}
	if outbox.Status != contracts.ChannelOutboxDead {
		t.Fatalf("outbox status should be dead, got=%s", outbox.Status)
	}
	if outbox.NextRetryAt != nil {
		t.Fatalf("dead outbox should clear next_retry_at, got=%v", outbox.NextRetryAt)
	}
	if !strings.Contains(outbox.LastError, "retry exhausted") {
		t.Fatalf("outbox last_error mismatch: %q", outbox.LastError)
	}
}

func TestRepository_FindRetryableOutbox(t *testing.T) {
	db := openGatewayDBForRepositoryTest(t)
	repo := NewGormRepository(db)
	binding := createRepositoryTestBinding(t, db, "demo", "chat-repo-find-retryable")

	due, err := repo.CreatePending(context.Background(), binding, "demo", "due message")
	if err != nil {
		t.Fatalf("CreatePending due failed: %v", err)
	}
	if err := repo.MarkSending(context.Background(), due.outbox.ID); err != nil {
		t.Fatalf("MarkSending due failed: %v", err)
	}
	if err := repo.MarkFailedRetryable(context.Background(), due, fmt.Errorf("due"), time.Now().Add(-time.Minute)); err != nil {
		t.Fatalf("MarkFailedRetryable due failed: %v", err)
	}

	future, err := repo.CreatePending(context.Background(), binding, "demo", "future message")
	if err != nil {
		t.Fatalf("CreatePending future failed: %v", err)
	}
	if err := repo.MarkSending(context.Background(), future.outbox.ID); err != nil {
		t.Fatalf("MarkSending future failed: %v", err)
	}
	if err := repo.MarkFailedRetryable(context.Background(), future, fmt.Errorf("future"), time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("MarkFailedRetryable future failed: %v", err)
	}

	items, err := repo.FindRetryableOutbox(context.Background(), time.Now(), 10)
	if err != nil {
		t.Fatalf("FindRetryableOutbox failed: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 retryable outbox, got=%d", len(items))
	}
	got := items[0]
	if got.state.outbox.ID != due.outbox.ID {
		t.Fatalf("retryable outbox id mismatch: got=%d want=%d", got.state.outbox.ID, due.outbox.ID)
	}
	if got.binding.ID != binding.ID {
		t.Fatalf("retryable binding id mismatch: got=%d want=%d", got.binding.ID, binding.ID)
	}
	if got.project != "demo" {
		t.Fatalf("retryable project mismatch: %q", got.project)
	}
	if got.text != "due message" {
		t.Fatalf("retryable text mismatch: %q", got.text)
	}
}

func TestRepository_FindPendingOutbox(t *testing.T) {
	db := openGatewayDBForRepositoryTest(t)
	repo := NewGormRepository(db)
	binding := createRepositoryTestBinding(t, db, "demo", "chat-repo-find-pending")

	pending, err := repo.CreatePending(context.Background(), binding, "demo", "pending message")
	if err != nil {
		t.Fatalf("CreatePending pending failed: %v", err)
	}
	sent, err := repo.CreatePending(context.Background(), binding, "demo", "sent message")
	if err != nil {
		t.Fatalf("CreatePending sent failed: %v", err)
	}
	if err := repo.MarkSending(context.Background(), sent.outbox.ID); err != nil {
		t.Fatalf("MarkSending sent failed: %v", err)
	}
	if err := repo.MarkSent(context.Background(), sent); err != nil {
		t.Fatalf("MarkSent sent failed: %v", err)
	}

	items, err := repo.FindPendingOutbox(context.Background(), 10)
	if err != nil {
		t.Fatalf("FindPendingOutbox failed: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 pending outbox, got=%d", len(items))
	}
	got := items[0]
	if got.state.outbox.ID != pending.outbox.ID {
		t.Fatalf("pending outbox id mismatch: got=%d want=%d", got.state.outbox.ID, pending.outbox.ID)
	}
	if got.binding.ID != binding.ID {
		t.Fatalf("pending binding id mismatch: got=%d want=%d", got.binding.ID, binding.ID)
	}
	if got.project != "demo" {
		t.Fatalf("pending project mismatch: %q", got.project)
	}
	if got.text != "pending message" {
		t.Fatalf("pending text mismatch: %q", got.text)
	}
}

func TestRepository_CreatePendingWithPayload_PreservesCardJSON(t *testing.T) {
	db := openGatewayDBForRepositoryTest(t)
	repo := NewGormRepository(db)
	binding := createRepositoryTestBinding(t, db, "demo", "chat-repo-card-json")

	cardJSON := `{"type":"template","data":{"foo":"bar"}}`
	_, err := repo.CreatePendingWithPayload(context.Background(), binding, "demo", "fallback text", contracts.JSONMap{
		payloadKeyCardJSON: cardJSON,
		payloadKeySendMode: payloadSendModeInteractive,
	})
	if err != nil {
		t.Fatalf("CreatePendingWithPayload failed: %v", err)
	}

	items, err := repo.FindPendingOutbox(context.Background(), 10)
	if err != nil {
		t.Fatalf("FindPendingOutbox failed: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 pending outbox, got=%d", len(items))
	}
	got := items[0]
	if got.cardJSON != cardJSON {
		t.Fatalf("cardJSON mismatch: got=%q want=%q", got.cardJSON, cardJSON)
	}
	if got.text != "fallback text" {
		t.Fatalf("text mismatch: got=%q", got.text)
	}
}
