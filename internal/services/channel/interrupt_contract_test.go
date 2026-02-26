package channel

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"dalek/internal/contracts"
	"dalek/internal/repo"
	"dalek/internal/services/core"
	"dalek/internal/store"
)

func TestInterruptContract_MessageReachableThenStopReachable(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "dalek.sqlite3")
	db, err := store.OpenAndMigrate(dbPath)
	if err != nil {
		t.Fatalf("OpenAndMigrate failed: %v", err)
	}

	svc := newChannelServiceWithFakeAgent(t, db)

	inbound, err := svc.ProcessInbound(context.Background(), contracts.InboundEnvelope{
		Schema:             contracts.ChannelInboundSchemaV1,
		ChannelType:        contracts.ChannelTypeIM,
		Adapter:            "im.feishu",
		PeerConversationID: "chat-contract-1",
		PeerMessageID:      "msg-contract-1",
		SenderID:           "user-1",
		Text:               "hello",
		ReceivedAt:         time.Now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		t.Fatalf("ProcessInbound failed: %v", err)
	}
	if inbound.ConversationID == 0 {
		t.Fatalf("expected non-zero conversation id")
	}

	manager := &stubInterruptChatRunnerManager{}
	svc.chatRunners = manager

	canceled := false
	svc.setRunningTurn(inbound.JobID, inbound.ConversationID, "", func() { canceled = true })

	result, err := svc.InterruptPeerConversation(context.Background(), contracts.ChannelTypeIM, "im.feishu", "chat-contract-1")
	if err != nil {
		t.Fatalf("InterruptPeerConversation failed: %v", err)
	}
	if result.Status != InterruptStatusHit {
		t.Fatalf("expected status=hit, got=%s", result.Status)
	}
	if !result.ContextCanceled {
		t.Fatalf("expected context canceled=true")
	}
	if !canceled {
		t.Fatalf("running turn context should be canceled")
	}
	if manager.interruptCalls != 1 {
		t.Fatalf("interrupt calls mismatch: %d", manager.interruptCalls)
	}
}

func TestInterruptPeerConversation_ReturnsMissWhenNoRunningTurn(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "dalek.sqlite3")
	db, err := store.OpenAndMigrate(dbPath)
	if err != nil {
		t.Fatalf("OpenAndMigrate failed: %v", err)
	}
	repoRoot := t.TempDir()
	svc := New(&core.Project{
		Name:     "demo",
		RepoRoot: repoRoot,
		Layout:   repo.NewLayout(repoRoot),
		DB:       db,
	})

	binding := store.ChannelBinding{
		ProjectName:    "demo",
		ChannelType:    contracts.ChannelTypeIM,
		Adapter:        "im.feishu",
		PeerProjectKey: "",
		RolePolicyJSON: "{}",
		Enabled:        true,
	}
	if err := db.Create(&binding).Error; err != nil {
		t.Fatalf("create binding failed: %v", err)
	}
	conv := store.ChannelConversation{
		BindingID:          binding.ID,
		PeerConversationID: "chat-contract-2",
	}
	if err := db.Create(&conv).Error; err != nil {
		t.Fatalf("create conversation failed: %v", err)
	}

	manager := &stubInterruptChatRunnerManager{}
	svc.chatRunners = manager

	result, err := svc.InterruptPeerConversation(context.Background(), contracts.ChannelTypeIM, "im.feishu", "chat-contract-2")
	if err != nil {
		t.Fatalf("InterruptPeerConversation failed: %v", err)
	}
	if result.Status != InterruptStatusMiss {
		t.Fatalf("expected status=miss, got=%s", result.Status)
	}
	if result.RunnerInterrupted {
		t.Fatalf("runner interrupted should be false")
	}
	if result.ContextCanceled {
		t.Fatalf("context canceled should be false")
	}
	if manager.interruptCalls != 1 {
		t.Fatalf("interrupt calls mismatch: %d", manager.interruptCalls)
	}
}

func TestInterruptPeerConversation_ReturnsExecutionFailure(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "dalek.sqlite3")
	db, err := store.OpenAndMigrate(dbPath)
	if err != nil {
		t.Fatalf("OpenAndMigrate failed: %v", err)
	}
	repoRoot := t.TempDir()
	svc := New(&core.Project{
		Name:     "demo",
		RepoRoot: repoRoot,
		Layout:   repo.NewLayout(repoRoot),
		DB:       db,
	})

	binding := store.ChannelBinding{
		ProjectName:    "demo",
		ChannelType:    contracts.ChannelTypeIM,
		Adapter:        "im.feishu",
		PeerProjectKey: "",
		RolePolicyJSON: "{}",
		Enabled:        true,
	}
	if err := db.Create(&binding).Error; err != nil {
		t.Fatalf("create binding failed: %v", err)
	}
	conv := store.ChannelConversation{
		BindingID:          binding.ID,
		PeerConversationID: "chat-contract-3",
	}
	if err := db.Create(&conv).Error; err != nil {
		t.Fatalf("create conversation failed: %v", err)
	}

	manager := &stubInterruptChatRunnerManager{interruptErr: errors.New("interrupt failed")}
	svc.chatRunners = manager

	result, err := svc.InterruptPeerConversation(context.Background(), contracts.ChannelTypeIM, "im.feishu", "chat-contract-3")
	if err != nil {
		t.Fatalf("InterruptPeerConversation should not return hard error, got=%v", err)
	}
	if result.Status != InterruptStatusExecutionFailure {
		t.Fatalf("expected status=execution_failure, got=%s", result.Status)
	}
	if result.RunnerErrorMessage() == "" {
		t.Fatalf("runner error should not be empty")
	}
	if manager.interruptCalls != 1 {
		t.Fatalf("interrupt calls mismatch: %d", manager.interruptCalls)
	}
}
