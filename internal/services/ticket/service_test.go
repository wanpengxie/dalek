package ticket

import (
	"context"
	"dalek/internal/contracts"
	"errors"
	"path/filepath"
	"testing"

	"dalek/internal/store"

	"gorm.io/gorm"
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

func TestService_Create_AllowsEmptyDescription(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.sqlite3")
	db, err := store.OpenAndMigrate(dbPath)
	if err != nil {
		t.Fatalf("open db failed: %v", err)
	}

	svc := New(db)
	tk, err := svc.Create(context.Background(), "hello")
	if err != nil {
		t.Fatalf("create failed: %v", err)
	}
	if tk.Description != "" {
		t.Fatalf("expected empty description, got %q", tk.Description)
	}

	tk2, err := svc.CreateWithDescription(context.Background(), "world", "   ")
	if err != nil {
		t.Fatalf("create with empty description failed: %v", err)
	}
	if tk2.Description != "" {
		t.Fatalf("expected empty description after trim, got %q", tk2.Description)
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

func TestService_GetByID(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.sqlite3")
	db, err := store.OpenAndMigrate(dbPath)
	if err != nil {
		t.Fatalf("open db failed: %v", err)
	}

	svc := New(db)
	created, err := svc.CreateWithDescription(context.Background(), "hello", "desc")
	if err != nil {
		t.Fatalf("create failed: %v", err)
	}

	got, err := svc.GetByID(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("GetByID failed: %v", err)
	}
	if got.ID != created.ID {
		t.Fatalf("id mismatch: got=%d want=%d", got.ID, created.ID)
	}
	if got.Title != created.Title {
		t.Fatalf("title mismatch: got=%q want=%q", got.Title, created.Title)
	}

	if _, err := svc.GetByID(context.Background(), created.ID+1000); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("expected gorm.ErrRecordNotFound, got=%v", err)
	}
}
