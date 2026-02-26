package ticket

import (
	"context"
	"dalek/internal/contracts"
	"path/filepath"
	"testing"

	"dalek/internal/store"
)

func TestService_CreateAndList(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.sqlite3")
	db, err := store.OpenAndMigrate(dbPath)
	if err != nil {
		t.Fatalf("open db failed: %v", err)
	}

	svc := New(db)
	if _, err := svc.CreateWithDescription(context.Background(), "hello", "desc"); err != nil {
		t.Fatalf("create failed: %v", err)
	}
	items, err := svc.List(context.Background(), false)
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 ticket, got %d", len(items))
	}
	if items[0].WorkflowStatus != contracts.TicketBacklog {
		t.Fatalf("unexpected status: %s", items[0].WorkflowStatus)
	}
}

func TestService_CreateWithDescription_RequiresDescription(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.sqlite3")
	db, err := store.OpenAndMigrate(dbPath)
	if err != nil {
		t.Fatalf("open db failed: %v", err)
	}

	svc := New(db)
	if _, err := svc.CreateWithDescription(context.Background(), "hello", "   "); err == nil {
		t.Fatalf("expected error when description is empty")
	}
}

func TestService_CreateWithDescription(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.sqlite3")
	db, err := store.OpenAndMigrate(dbPath)
	if err != nil {
		t.Fatalf("open db failed: %v", err)
	}

	svc := New(db)
	desc := "实现 gateway\n参考 docs/CHANNEL_GATEWAY_PM_DESIGN.md"
	tk, err := svc.CreateWithDescription(context.Background(), "gateway", desc)
	if err != nil {
		t.Fatalf("create with description failed: %v", err)
	}
	if tk == nil {
		t.Fatalf("ticket is nil")
	}
	if tk.Description != desc {
		t.Fatalf("description not saved, got=%q want=%q", tk.Description, desc)
	}
}

func TestService_UpdateText_RequiresDescription(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.sqlite3")
	db, err := store.OpenAndMigrate(dbPath)
	if err != nil {
		t.Fatalf("open db failed: %v", err)
	}

	svc := New(db)
	tk, err := svc.CreateWithDescription(context.Background(), "hello", "desc")
	if err != nil {
		t.Fatalf("create failed: %v", err)
	}

	if err := svc.UpdateText(context.Background(), tk.ID, "new title", "   "); err == nil {
		t.Fatalf("expected error when description is empty")
	}
}
