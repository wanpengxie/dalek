package pm

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"dalek/internal/contracts"
	"dalek/internal/store"

	"gorm.io/gorm"
)

func TestInboxQueries_ReadLegacyReplyTimeColumnsAfterMigration(t *testing.T) {
	ctx := context.Background()
	svc, p, _ := newServiceForTest(t)
	tk := createTicket(t, p.DB, "legacy-inbox-time-columns")

	downgradeInboxReplyTimeColumnsToLegacyText(t, p.DB)

	doneItem := contracts.InboxItem{
		Key:              "legacy-done",
		Status:           contracts.InboxDone,
		Severity:         contracts.InboxBlocker,
		Reason:           contracts.InboxNeedsUser,
		Title:            "历史 done",
		Body:             "legacy done",
		TicketID:         tk.ID,
		WorkerID:         1,
		OriginTaskRunID:  11,
		CurrentTaskRunID: 11,
		WaitRoundCount:   1,
	}
	if err := p.DB.Create(&doneItem).Error; err != nil {
		t.Fatalf("create legacy done inbox failed: %v", err)
	}

	pendingItem := contracts.InboxItem{
		Key:              "legacy-pending",
		Status:           contracts.InboxOpen,
		Severity:         contracts.InboxBlocker,
		Reason:           contracts.InboxNeedsUser,
		Title:            "历史 pending",
		Body:             "legacy pending",
		TicketID:         tk.ID,
		WorkerID:         1,
		OriginTaskRunID:  11,
		CurrentTaskRunID: 12,
		WaitRoundCount:   2,
		ReplyAction:      contracts.InboxReplyContinue,
		ReplyMarkdown:    "已补充说明",
	}
	if err := p.DB.Create(&pendingItem).Error; err != nil {
		t.Fatalf("create legacy pending inbox failed: %v", err)
	}

	const resolvedAt = "2026-03-16 07:50:23.612544+08:00"
	const receivedAt = "2026-03-16 09:56:54.787762+08:00"

	if err := p.DB.Exec(`
UPDATE inbox_items
SET chain_resolved_at = ?, updated_at = ?
WHERE id = ?;
`, resolvedAt, resolvedAt, doneItem.ID).Error; err != nil {
		t.Fatalf("set legacy chain_resolved_at failed: %v", err)
	}
	if err := p.DB.Exec(`
UPDATE inbox_items
SET reply_received_at = ?, reply_consumed_at = NULL, updated_at = ?
WHERE id = ?;
`, receivedAt, receivedAt, pendingItem.ID).Error; err != nil {
		t.Fatalf("set legacy reply_received_at failed: %v", err)
	}

	if _, err := svc.GetInboxItem(ctx, doneItem.ID); err == nil || !strings.Contains(err.Error(), "unsupported Scan") {
		t.Fatalf("legacy done inbox should fail before migration, err=%v", err)
	}
	if _, err := loadPendingNeedsUserInboxWithDB(ctx, p.DB, tk.ID, 0); err == nil || !strings.Contains(err.Error(), "unsupported Scan") {
		t.Fatalf("legacy pending inbox should fail before migration, err=%v", err)
	}

	p.DB = reopenDBAfterInboxTimeMigration(t, p.DB, p.Layout.DBPath)

	doneRows, err := svc.ListInbox(ctx, ListInboxOptions{Status: contracts.InboxDone, Limit: 10})
	if err != nil {
		t.Fatalf("ListInbox(done) after migration failed: %v", err)
	}
	done := findInboxByKey(doneRows, doneItem.Key)
	if done == nil {
		t.Fatalf("expected legacy done inbox in ListInbox result")
	}
	if done.ChainResolvedAt == nil {
		t.Fatalf("expected chain_resolved_at readable in ListInbox after migration")
	}

	gotDone, err := svc.GetInboxItem(ctx, doneItem.ID)
	if err != nil {
		t.Fatalf("GetInboxItem after migration failed: %v", err)
	}
	if gotDone.ChainResolvedAt == nil {
		t.Fatalf("expected chain_resolved_at readable in GetInboxItem after migration")
	}

	pending, err := loadPendingNeedsUserInboxWithDB(ctx, p.DB, tk.ID, 0)
	if err != nil {
		t.Fatalf("loadPendingNeedsUserInboxWithDB after migration failed: %v", err)
	}
	if pending == nil || pending.ID != pendingItem.ID {
		t.Fatalf("expected pending inbox#%d after migration, got=%+v", pendingItem.ID, pending)
	}
	if pending.ReplyReceivedAt == nil {
		t.Fatalf("expected reply_received_at readable after migration")
	}
	if pending.ReplyConsumedAt != nil {
		t.Fatalf("expected reply_consumed_at remain nil after migration, got=%v", pending.ReplyConsumedAt)
	}
}

func downgradeInboxReplyTimeColumnsToLegacyText(t *testing.T, db *gorm.DB) {
	t.Helper()
	for _, col := range []string{"chain_resolved_at", "reply_received_at", "reply_consumed_at"} {
		if err := db.Exec(fmt.Sprintf(`ALTER TABLE inbox_items DROP COLUMN %s;`, col)).Error; err != nil {
			t.Fatalf("drop inbox_items.%s failed: %v", col, err)
		}
		if err := db.Exec(fmt.Sprintf(`ALTER TABLE inbox_items ADD COLUMN %s TEXT DEFAULT NULL;`, col)).Error; err != nil {
			t.Fatalf("add legacy TEXT inbox_items.%s failed: %v", col, err)
		}
	}
}

func reopenDBAfterInboxTimeMigration(t *testing.T, db *gorm.DB, dbPath string) *gorm.DB {
	t.Helper()
	if err := db.Exec("DELETE FROM schema_migrations WHERE version >= 22;").Error; err != nil {
		t.Fatalf("rollback schema_migrations for v22 failed: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("db.DB failed: %v", err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatalf("close legacy db failed: %v", err)
	}
	reopened, err := store.OpenAndMigrate(dbPath)
	if err != nil {
		t.Fatalf("OpenAndMigrate after legacy downgrade failed: %v", err)
	}
	return reopened
}

func findInboxByKey(items []contracts.InboxItem, key string) *contracts.InboxItem {
	for i := range items {
		if strings.TrimSpace(items[i].Key) == strings.TrimSpace(key) {
			return &items[i]
		}
	}
	return nil
}
