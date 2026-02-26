package gatewaysend

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

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
		RolePolicyJSON: "{}",
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
