package ticket

import (
	"context"
	"dalek/internal/contracts"
	"errors"
	"path/filepath"
	"testing"
	"time"

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

func TestService_List_SortsByPriorityThenCreatedAtThenID(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.sqlite3")
	db, err := store.OpenAndMigrate(dbPath)
	if err != nil {
		t.Fatalf("open db failed: %v", err)
	}

	svc := New(db)
	t1, err := svc.CreateWithDescription(context.Background(), "t1", "d1")
	if err != nil {
		t.Fatalf("create t1 failed: %v", err)
	}
	t2, err := svc.CreateWithDescription(context.Background(), "t2", "d2")
	if err != nil {
		t.Fatalf("create t2 failed: %v", err)
	}
	t3, err := svc.CreateWithDescription(context.Background(), "t3", "d3")
	if err != nil {
		t.Fatalf("create t3 failed: %v", err)
	}

	if err := svc.SetPriority(context.Background(), t1.ID, 1); err != nil {
		t.Fatalf("set t1 priority failed: %v", err)
	}
	if err := svc.SetPriority(context.Background(), t2.ID, 1); err != nil {
		t.Fatalf("set t2 priority failed: %v", err)
	}
	if err := svc.SetPriority(context.Background(), t3.ID, 2); err != nil {
		t.Fatalf("set t3 priority failed: %v", err)
	}

	base := time.Date(2026, 2, 27, 10, 0, 0, 0, time.UTC)
	if err := db.Model(&contracts.Ticket{}).Where("id = ?", t1.ID).Updates(map[string]any{
		"created_at": base.Add(2 * time.Hour),
		"updated_at": base.Add(8 * time.Hour),
	}).Error; err != nil {
		t.Fatalf("update t1 timestamps failed: %v", err)
	}
	if err := db.Model(&contracts.Ticket{}).Where("id = ?", t2.ID).Updates(map[string]any{
		"created_at": base.Add(1 * time.Hour),
		"updated_at": base.Add(9 * time.Hour),
	}).Error; err != nil {
		t.Fatalf("update t2 timestamps failed: %v", err)
	}
	if err := db.Model(&contracts.Ticket{}).Where("id = ?", t3.ID).Updates(map[string]any{
		"created_at": base.Add(3 * time.Hour),
		"updated_at": base.Add(1 * time.Hour),
	}).Error; err != nil {
		t.Fatalf("update t3 timestamps failed: %v", err)
	}

	items, err := svc.List(context.Background(), false)
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}
	if len(items) != 3 {
		t.Fatalf("expected 3 tickets, got %d", len(items))
	}

	got := []uint{items[0].ID, items[1].ID, items[2].ID}
	want := []uint{t3.ID, t2.ID, t1.ID}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("unexpected order: got=%v want=%v", got, want)
		}
	}
}

func TestService_SetPriority_UpdatesTicket(t *testing.T) {
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
	if err := svc.SetPriority(context.Background(), tk.ID, contracts.TicketPriorityHigh); err != nil {
		t.Fatalf("set priority failed: %v", err)
	}

	got, err := svc.GetByID(context.Background(), tk.ID)
	if err != nil {
		t.Fatalf("get failed: %v", err)
	}
	if got.Priority != contracts.TicketPriorityHigh {
		t.Fatalf("unexpected priority: got=%d want=%d", got.Priority, contracts.TicketPriorityHigh)
	}

	if err := svc.SetPriority(context.Background(), 99999, contracts.TicketPriorityLow); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("set priority on missing ticket should return record not found, got=%v", err)
	}
}
