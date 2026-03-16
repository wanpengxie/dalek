package store

import (
	"dalek/internal/contracts"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"gorm.io/gorm"
)

func TestOpenAndMigrate_TracksBaselineMigrations(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "dalek.sqlite3")
	db, err := OpenAndMigrate(dbPath)
	if err != nil {
		t.Fatalf("OpenAndMigrate failed: %v", err)
	}

	rows := loadMigrationRowsForTest(t, db)
	want := len(storeMigrations())
	if len(rows) != want {
		t.Fatalf("expected %d migration records, got=%d", want, len(rows))
	}
	for i, row := range rows {
		if row.Version != i+1 {
			t.Fatalf("expected migration version=%d, got=%d", i+1, row.Version)
		}
		if row.Name == "" {
			t.Fatalf("migration version=%d should have non-empty name", row.Version)
		}
		if row.AppliedAt == "" {
			t.Fatalf("migration version=%d should have applied_at", row.Version)
		}
	}
}

func TestOpenAndMigrate_IdempotentMigrationUpgrade(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "dalek.sqlite3")
	db, err := OpenAndMigrate(dbPath)
	if err != nil {
		t.Fatalf("OpenAndMigrate failed: %v", err)
	}
	before := loadMigrationRowsForTest(t, db)

	db2, err := OpenAndMigrate(dbPath)
	if err != nil {
		t.Fatalf("OpenAndMigrate (2nd) failed: %v", err)
	}
	after := loadMigrationRowsForTest(t, db2)

	if len(before) != len(after) {
		t.Fatalf("expected migration row count stable, before=%d after=%d", len(before), len(after))
	}
	for i := range before {
		if before[i] != after[i] {
			t.Fatalf("migration rows should remain unchanged on idempotent rerun, before=%+v after=%+v", before[i], after[i])
		}
	}
}

func TestRunMigrations_FailureStopsAtVersion(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "dalek.sqlite3")
	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	executed := make([]int, 0, 3)
	err = RunMigrations(db, []Migration{
		{
			Version: 1,
			Name:    "ok_1",
			Up: func(db *gorm.DB) error {
				executed = append(executed, 1)
				return nil
			},
		},
		{
			Version: 2,
			Name:    "boom_2",
			Up: func(db *gorm.DB) error {
				executed = append(executed, 2)
				return errors.New("boom")
			},
		},
		{
			Version: 3,
			Name:    "skip_3",
			Up: func(db *gorm.DB) error {
				executed = append(executed, 3)
				return nil
			},
		},
	})
	if err == nil {
		t.Fatalf("expected migration failure")
	}
	if len(executed) != 2 || executed[0] != 1 || executed[1] != 2 {
		t.Fatalf("expected execution stop at failed version, got=%v", executed)
	}

	rows := loadMigrationRowsForTest(t, db)
	if len(rows) != 1 || rows[0].Version != 1 {
		t.Fatalf("expected only v1 recorded after failure, got=%+v", rows)
	}
}

func TestOpenAndMigrate_WorkerZombieRetryColumnsPresent(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "dalek.sqlite3")
	db, err := OpenAndMigrate(dbPath)
	if err != nil {
		t.Fatalf("OpenAndMigrate failed: %v", err)
	}

	type columnRow struct {
		Name string `gorm:"column:name"`
	}
	var cols []columnRow
	if err := db.Raw("PRAGMA table_info(workers);").Scan(&cols).Error; err != nil {
		t.Fatalf("query workers columns failed: %v", err)
	}
	seen := map[string]bool{}
	for _, col := range cols {
		seen[col.Name] = true
	}
	for _, want := range []string{"retry_count", "last_retry_at", "last_error_hash", "log_path"} {
		if !seen[want] {
			t.Fatalf("workers missing expected column: %s", want)
		}
	}
	if seen["process_pid"] {
		t.Fatalf("workers should not keep old column: process_pid")
	}
	if seen["tmux_socket"] || seen["tmux_session"] {
		t.Fatalf("workers should not keep old tmux columns")
	}
}

func TestOpenAndMigrate_TicketLabelColumnPresent(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "dalek.sqlite3")
	db, err := OpenAndMigrate(dbPath)
	if err != nil {
		t.Fatalf("OpenAndMigrate failed: %v", err)
	}

	type columnRow struct {
		Name string `gorm:"column:name"`
	}
	var cols []columnRow
	if err := db.Raw("PRAGMA table_info(tickets);").Scan(&cols).Error; err != nil {
		t.Fatalf("query tickets columns failed: %v", err)
	}
	seen := map[string]bool{}
	for _, col := range cols {
		seen[col.Name] = true
	}
	if !seen["label"] {
		t.Fatalf("tickets should contain label column after migrations")
	}
}

func TestOpenAndMigrate_TicketIntegrationColumnsPresent(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "dalek.sqlite3")
	db, err := OpenAndMigrate(dbPath)
	if err != nil {
		t.Fatalf("OpenAndMigrate failed: %v", err)
	}

	for _, col := range []string{"integration_status", "merge_anchor_sha", "target_branch", "merged_at", "abandoned_reason"} {
		ok, err := tableHasColumn(db, "tickets", col)
		if err != nil {
			t.Fatalf("tableHasColumn(tickets.%s) failed: %v", col, err)
		}
		if !ok {
			t.Fatalf("tickets should contain integration column: %s", col)
		}
	}

	if err := dropTableColumn(db, "tickets", "abandoned_reason"); err != nil {
		t.Fatalf("drop tickets.abandoned_reason failed: %v", err)
	}
	if err := db.Exec("DELETE FROM schema_migrations WHERE version >= 16;").Error; err != nil {
		t.Fatalf("rollback schema_migrations for v16 failed: %v", err)
	}
	if _, err := OpenAndMigrate(dbPath); err != nil {
		t.Fatalf("OpenAndMigrate (reapply v16) failed: %v", err)
	}

	db2, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	ok, err := tableHasColumn(db2, "tickets", "abandoned_reason")
	if err != nil {
		t.Fatalf("tableHasColumn(tickets.abandoned_reason) after reapply failed: %v", err)
	}
	if !ok {
		t.Fatalf("tickets should restore abandoned_reason after reapply")
	}
}

func TestOpenAndMigrate_TicketLifecycleTablePresent(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "dalek.sqlite3")
	db, err := OpenAndMigrate(dbPath)
	if err != nil {
		t.Fatalf("OpenAndMigrate failed: %v", err)
	}

	for _, col := range []string{"ticket_id", "sequence", "event_type", "actor_type", "idempotency_key", "payload_json"} {
		ok, err := tableHasColumn(db, "ticket_lifecycle_events", col)
		if err != nil {
			t.Fatalf("tableHasColumn(ticket_lifecycle_events.%s) failed: %v", col, err)
		}
		if !ok {
			t.Fatalf("ticket_lifecycle_events should contain column: %s", col)
		}
	}

	if err := db.Exec(`DROP TABLE IF EXISTS ticket_lifecycle_events;`).Error; err != nil {
		t.Fatalf("drop ticket_lifecycle_events failed: %v", err)
	}
	if err := db.Exec("DELETE FROM schema_migrations WHERE version >= 17;").Error; err != nil {
		t.Fatalf("rollback schema_migrations for v17 failed: %v", err)
	}
	if _, err := OpenAndMigrate(dbPath); err != nil {
		t.Fatalf("OpenAndMigrate (reapply v17) failed: %v", err)
	}

	db2, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	ok, err := tableHasColumn(db2, "ticket_lifecycle_events", "idempotency_key")
	if err != nil {
		t.Fatalf("tableHasColumn(ticket_lifecycle_events.idempotency_key) after reapply failed: %v", err)
	}
	if !ok {
		t.Fatalf("ticket_lifecycle_events should be restored after reapply")
	}
}

func TestOpenAndMigrate_InboxReplyChainColumnsPresent(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "dalek.sqlite3")
	db, err := OpenAndMigrate(dbPath)
	if err != nil {
		t.Fatalf("OpenAndMigrate failed: %v", err)
	}

	for _, col := range []string{
		"origin_task_run_id",
		"current_task_run_id",
		"wait_round_count",
		"chain_resolved_at",
		"reply_action",
		"reply_markdown",
		"reply_received_at",
		"reply_consumed_at",
	} {
		ok, err := tableHasColumn(db, "inbox_items", col)
		if err != nil {
			t.Fatalf("tableHasColumn(inbox_items.%s) failed: %v", col, err)
		}
		if !ok {
			t.Fatalf("inbox_items should contain column: %s", col)
		}
	}

	if err := dropTableColumn(db, "inbox_items", "reply_markdown"); err != nil {
		t.Fatalf("drop inbox_items.reply_markdown failed: %v", err)
	}
	if err := db.Exec("DELETE FROM schema_migrations WHERE version >= 21;").Error; err != nil {
		t.Fatalf("rollback schema_migrations for v21 failed: %v", err)
	}
	if _, err := OpenAndMigrate(dbPath); err != nil {
		t.Fatalf("OpenAndMigrate (reapply v21) failed: %v", err)
	}

	db2, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	ok, err := tableHasColumn(db2, "inbox_items", "reply_markdown")
	if err != nil {
		t.Fatalf("tableHasColumn(inbox_items.reply_markdown) after reapply failed: %v", err)
	}
	if !ok {
		t.Fatalf("inbox_items should restore reply_markdown after reapply")
	}
}

func TestOpenAndMigrate_InboxReplyTimeColumnsUpgradeLegacyTextAndRemainReadable(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "dalek.sqlite3")
	db, err := OpenAndMigrate(dbPath)
	if err != nil {
		t.Fatalf("OpenAndMigrate failed: %v", err)
	}

	ticket := contracts.Ticket{
		Title:          "legacy-inbox-time-columns",
		Description:    "legacy schema regression",
		WorkflowStatus: contracts.TicketBlocked,
	}
	if err := db.Create(&ticket).Error; err != nil {
		t.Fatalf("create ticket failed: %v", err)
	}

	done := contracts.InboxItem{
		Key:              "legacy-chain-done",
		Status:           contracts.InboxDone,
		Severity:         contracts.InboxBlocker,
		Reason:           contracts.InboxNeedsUser,
		Title:            "历史 done inbox",
		Body:             "legacy chain resolved",
		TicketID:         ticket.ID,
		OriginTaskRunID:  11,
		CurrentTaskRunID: 11,
		WaitRoundCount:   1,
		ReplyAction:      contracts.InboxReplyDone,
		ReplyMarkdown:    "按最小收尾结束",
	}
	if err := db.Create(&done).Error; err != nil {
		t.Fatalf("create done inbox failed: %v", err)
	}

	pending := contracts.InboxItem{
		Key:              "legacy-reply-pending",
		Status:           contracts.InboxOpen,
		Severity:         contracts.InboxBlocker,
		Reason:           contracts.InboxNeedsUser,
		Title:            "待消费 reply inbox",
		Body:             "legacy reply pending",
		TicketID:         ticket.ID,
		OriginTaskRunID:  11,
		CurrentTaskRunID: 12,
		WaitRoundCount:   2,
		ReplyAction:      contracts.InboxReplyContinue,
		ReplyMarkdown:    "资料已补充，请继续",
	}
	if err := db.Create(&pending).Error; err != nil {
		t.Fatalf("create pending inbox failed: %v", err)
	}

	for _, col := range []string{"chain_resolved_at", "reply_received_at", "reply_consumed_at"} {
		if err := dropTableColumn(db, "inbox_items", col); err != nil {
			t.Fatalf("drop inbox_items.%s failed: %v", col, err)
		}
		if err := db.Exec("ALTER TABLE inbox_items ADD COLUMN " + col + " TEXT DEFAULT NULL;").Error; err != nil {
			t.Fatalf("re-add inbox_items.%s as TEXT failed: %v", col, err)
		}
	}

	const (
		chainResolvedLegacy = "2026-03-16 09:56:54.787762+08:00"
		replyReceivedLegacy = "2026-03-16 10:01:02.123456+08:00"
		replyConsumedLegacy = "2026-03-16 10:05:33.999999+08:00"
	)
	if err := db.Exec(`
UPDATE inbox_items
SET chain_resolved_at = ?, reply_received_at = ?, reply_consumed_at = ?
WHERE id = ?;
`, chainResolvedLegacy, replyReceivedLegacy, replyConsumedLegacy, done.ID).Error; err != nil {
		t.Fatalf("backfill done inbox legacy timestamps failed: %v", err)
	}
	if err := db.Exec(`
UPDATE inbox_items
SET reply_received_at = ?, reply_consumed_at = NULL
WHERE id = ?;
`, replyReceivedLegacy, pending.ID).Error; err != nil {
		t.Fatalf("backfill pending inbox legacy timestamps failed: %v", err)
	}

	if err := db.Exec("DELETE FROM schema_migrations WHERE version >= 22;").Error; err != nil {
		t.Fatalf("rollback schema_migrations for v22 failed: %v", err)
	}
	if _, err := OpenAndMigrate(dbPath); err != nil {
		t.Fatalf("OpenAndMigrate (reapply v22) failed: %v", err)
	}

	db2, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	for _, col := range []string{"chain_resolved_at", "reply_received_at", "reply_consumed_at"} {
		colType, err := tableColumnType(db2, "inbox_items", col)
		if err != nil {
			t.Fatalf("tableColumnType(inbox_items.%s) failed: %v", col, err)
		}
		switch strings.ToLower(strings.TrimSpace(colType)) {
		case "datetime", "timestamp":
		default:
			t.Fatalf("expected inbox_items.%s upgraded to datetime/timestamp, got=%q", col, colType)
		}
	}

	var gotDone contracts.InboxItem
	if err := db2.First(&gotDone, done.ID).Error; err != nil {
		t.Fatalf("reload done inbox failed: %v", err)
	}
	if gotDone.ChainResolvedAt == nil {
		t.Fatalf("expected chain_resolved_at readable after migration")
	}
	if gotDone.ReplyReceivedAt == nil {
		t.Fatalf("expected done inbox reply_received_at readable after migration")
	}
	if gotDone.ReplyConsumedAt == nil {
		t.Fatalf("expected done inbox reply_consumed_at readable after migration")
	}

	var gotPending contracts.InboxItem
	if err := db2.
		Where("status = ? AND reason = ? AND ticket_id = ?", contracts.InboxOpen, contracts.InboxNeedsUser, ticket.ID).
		Where("COALESCE(reply_action, '') <> ''").
		Where("reply_consumed_at IS NULL").
		Order("id desc").
		First(&gotPending).Error; err != nil {
		t.Fatalf("reload pending reply inbox failed: %v", err)
	}
	if gotPending.ID != pending.ID {
		t.Fatalf("expected pending inbox id=%d, got=%d", pending.ID, gotPending.ID)
	}
	if gotPending.ReplyReceivedAt == nil {
		t.Fatalf("expected pending inbox reply_received_at readable after migration")
	}
	if gotPending.ReplyConsumedAt != nil {
		t.Fatalf("expected pending inbox reply_consumed_at remain nil, got=%v", gotPending.ReplyConsumedAt)
	}
}

func TestOpenAndMigrate_InboxReplyTimeColumnsUpgradeLegacyTextRows(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "dalek.sqlite3")
	db, err := OpenAndMigrate(dbPath)
	if err != nil {
		t.Fatalf("OpenAndMigrate failed: %v", err)
	}

	for _, col := range []string{"chain_resolved_at", "reply_received_at", "reply_consumed_at"} {
		if err := dropTableColumn(db, "inbox_items", col); err != nil {
			t.Fatalf("drop inbox_items.%s failed: %v", col, err)
		}
	}
	if err := db.Exec("DELETE FROM schema_migrations WHERE version >= 21;").Error; err != nil {
		t.Fatalf("rollback schema_migrations for v21/v22 failed: %v", err)
	}
	if err := migrateAddInboxReplyChainColumns(db); err != nil {
		t.Fatalf("migrateAddInboxReplyChainColumns failed: %v", err)
	}

	for _, col := range []string{"chain_resolved_at", "reply_received_at", "reply_consumed_at"} {
		gotType, err := tableColumnType(db, "inbox_items", col)
		if err != nil {
			t.Fatalf("tableColumnType(inbox_items.%s) failed: %v", col, err)
		}
		if !strings.EqualFold(strings.TrimSpace(gotType), "text") {
			t.Fatalf("legacy inbox_items.%s should be TEXT before v22, got=%q", col, gotType)
		}
	}

	const resolvedAt = "2026-03-16 07:50:23.612544+08:00"
	const receivedAt = "2026-03-16 09:56:54.787762+08:00"

	if err := db.Exec(`
INSERT INTO inbox_items (
	created_at, updated_at, key, status, severity, reason, title, body,
	ticket_id, worker_id, merge_item_id,
	origin_task_run_id, current_task_run_id, wait_round_count,
	chain_resolved_at, reply_action, reply_markdown, reply_received_at, reply_consumed_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);
`, resolvedAt, resolvedAt, "legacy-done", contracts.InboxDone, contracts.InboxBlocker, contracts.InboxNeedsUser, "历史 done", "legacy done", 1, 1, 0, 11, 11, 1, resolvedAt, contracts.InboxReplyNone, "", nil, nil).Error; err != nil {
		t.Fatalf("insert legacy done inbox failed: %v", err)
	}
	if err := db.Exec(`
INSERT INTO inbox_items (
	created_at, updated_at, key, status, severity, reason, title, body,
	ticket_id, worker_id, merge_item_id,
	origin_task_run_id, current_task_run_id, wait_round_count,
	chain_resolved_at, reply_action, reply_markdown, reply_received_at, reply_consumed_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);
`, receivedAt, receivedAt, "legacy-pending", contracts.InboxOpen, contracts.InboxBlocker, contracts.InboxNeedsUser, "历史 pending", "legacy pending", 1, 1, 0, 11, 12, 2, nil, contracts.InboxReplyContinue, "已补充说明", receivedAt, nil).Error; err != nil {
		t.Fatalf("insert legacy pending inbox failed: %v", err)
	}

	var doneBefore contracts.InboxItem
	if err := db.Where("key = ?", "legacy-done").First(&doneBefore).Error; err == nil || !strings.Contains(err.Error(), "unsupported Scan") {
		t.Fatalf("legacy done inbox should fail scan before v22, err=%v", err)
	}
	var pendingBefore contracts.InboxItem
	if err := db.Where("key = ?", "legacy-pending").First(&pendingBefore).Error; err == nil || !strings.Contains(err.Error(), "unsupported Scan") {
		t.Fatalf("legacy pending inbox should fail scan before v22, err=%v", err)
	}

	db2, err := OpenAndMigrate(dbPath)
	if err != nil {
		t.Fatalf("OpenAndMigrate failed: %v", err)
	}

	for _, col := range []string{"chain_resolved_at", "reply_received_at", "reply_consumed_at"} {
		gotType, err := tableColumnType(db2, "inbox_items", col)
		if err != nil {
			t.Fatalf("tableColumnType(inbox_items.%s) after v22 failed: %v", col, err)
		}
		gotType = strings.ToLower(strings.TrimSpace(gotType))
		if gotType != "datetime" && gotType != "timestamp" {
			t.Fatalf("inbox_items.%s should upgrade to datetime-compatible type, got=%q", col, gotType)
		}
	}

	var done contracts.InboxItem
	if err := db2.Where("key = ?", "legacy-done").First(&done).Error; err != nil {
		t.Fatalf("load legacy done inbox after v22 failed: %v", err)
	}
	if done.ChainResolvedAt == nil {
		t.Fatalf("expected chain_resolved_at restored after v22")
	}

	var pending contracts.InboxItem
	if err := db2.Where("key = ?", "legacy-pending").First(&pending).Error; err != nil {
		t.Fatalf("load legacy pending inbox after v22 failed: %v", err)
	}
	if pending.ReplyReceivedAt == nil {
		t.Fatalf("expected reply_received_at restored after v22")
	}
	if pending.ReplyConsumedAt != nil {
		t.Fatalf("expected reply_consumed_at remain nil after v22, got=%v", pending.ReplyConsumedAt)
	}
}

func TestOpenAndMigrate_ChannelConversationAgentProviderPresent(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "dalek.sqlite3")
	db, err := OpenAndMigrate(dbPath)
	if err != nil {
		t.Fatalf("OpenAndMigrate failed: %v", err)
	}

	ok, err := tableHasColumn(db, "channel_conversations", "agent_provider")
	if err != nil {
		t.Fatalf("tableHasColumn(channel_conversations.agent_provider) failed: %v", err)
	}
	if !ok {
		t.Fatalf("channel_conversations should contain agent_provider column")
	}

	if err := dropTableColumn(db, "channel_conversations", "agent_provider"); err != nil {
		t.Fatalf("drop channel_conversations.agent_provider failed: %v", err)
	}
	if err := db.Exec("DELETE FROM schema_migrations WHERE version >= 18;").Error; err != nil {
		t.Fatalf("rollback schema_migrations for v18 failed: %v", err)
	}
	if _, err := OpenAndMigrate(dbPath); err != nil {
		t.Fatalf("OpenAndMigrate (reapply v18) failed: %v", err)
	}

	db2, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	ok, err = tableHasColumn(db2, "channel_conversations", "agent_provider")
	if err != nil {
		t.Fatalf("tableHasColumn(channel_conversations.agent_provider) after reapply failed: %v", err)
	}
	if !ok {
		t.Fatalf("channel_conversations should restore agent_provider after reapply")
	}
}

func TestOpenAndMigrate_PMStatePlannerColumnsNoop(t *testing.T) {
	// Migration v14 is now a noop (planner columns removed).
	// Verify the migration runs without error.
	dbPath := filepath.Join(t.TempDir(), "dalek.sqlite3")
	if _, err := OpenAndMigrate(dbPath); err != nil {
		t.Fatalf("OpenAndMigrate failed: %v", err)
	}
}

func TestOpenAndMigrate_PMOpsJournalCheckpointTablesNoop(t *testing.T) {
	// Migration v15 is now a noop (PMOps journal/checkpoint tables removed).
	// Verify the migration runs without error.
	dbPath := filepath.Join(t.TempDir(), "dalek.sqlite3")
	if _, err := OpenAndMigrate(dbPath); err != nil {
		t.Fatalf("OpenAndMigrate failed: %v", err)
	}
}

func TestOpenAndMigrate_RepairWorkerLogPathWhenOldV9Occupied(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "dalek.sqlite3")
	db, err := OpenAndMigrate(dbPath)
	if err != nil {
		t.Fatalf("OpenAndMigrate failed: %v", err)
	}

	if err := dropTableColumn(db, "workers", "log_path"); err != nil {
		t.Fatalf("drop workers.log_path failed: %v", err)
	}
	if err := db.Exec("DELETE FROM schema_migrations WHERE version >= 10;").Error; err != nil {
		t.Fatalf("rollback schema_migrations failed: %v", err)
	}
	if err := db.Exec("UPDATE schema_migrations SET name = ? WHERE version = 9;", "migrate_dag_plans_schema").Error; err != nil {
		t.Fatalf("set occupied v9 name failed: %v", err)
	}

	db2, err := OpenAndMigrate(dbPath)
	if err != nil {
		t.Fatalf("OpenAndMigrate (repair) failed: %v", err)
	}

	type columnRow struct {
		Name string `gorm:"column:name"`
	}
	var cols []columnRow
	if err := db2.Raw("PRAGMA table_info(workers);").Scan(&cols).Error; err != nil {
		t.Fatalf("query workers columns failed: %v", err)
	}
	seen := map[string]bool{}
	for _, col := range cols {
		seen[col.Name] = true
	}
	if !seen["log_path"] {
		t.Fatalf("workers should restore log_path when v9 was occupied by old branch")
	}
	if seen["process_pid"] {
		t.Fatalf("workers should not keep old column: process_pid")
	}
}

func TestOpenAndMigrate_DropsLegacyWorkerTmuxColumnsAndDagPlans(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "dalek.sqlite3")
	db, err := OpenAndMigrate(dbPath)
	if err != nil {
		t.Fatalf("OpenAndMigrate failed: %v", err)
	}

	if err := db.Exec("ALTER TABLE workers ADD COLUMN tmux_socket TEXT NOT NULL DEFAULT '';").Error; err != nil {
		t.Fatalf("add legacy workers.tmux_socket failed: %v", err)
	}
	if err := db.Exec("ALTER TABLE workers ADD COLUMN tmux_session TEXT NOT NULL DEFAULT '';").Error; err != nil {
		t.Fatalf("add legacy workers.tmux_session failed: %v", err)
	}
	if err := db.Exec(`
CREATE TABLE IF NOT EXISTS dag_plans (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	name TEXT NOT NULL DEFAULT '',
	status TEXT NOT NULL DEFAULT 'active',
	config_json TEXT NOT NULL DEFAULT '{}',
	created_at DATETIME NOT NULL,
	updated_at DATETIME NOT NULL
);
`).Error; err != nil {
		t.Fatalf("create legacy dag_plans failed: %v", err)
	}
	if err := db.Exec("CREATE INDEX IF NOT EXISTS idx_dag_plans_status ON dag_plans(status);").Error; err != nil {
		t.Fatalf("create legacy idx_dag_plans_status failed: %v", err)
	}

	if err := db.Exec("DELETE FROM schema_migrations WHERE version >= 11;").Error; err != nil {
		t.Fatalf("rollback schema_migrations failed: %v", err)
	}
	if _, err := OpenAndMigrate(dbPath); err != nil {
		t.Fatalf("OpenAndMigrate (cleanup) failed: %v", err)
	}

	db2, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	hasSocket, err := tableHasColumn(db2, "workers", "tmux_socket")
	if err != nil {
		t.Fatalf("query workers.tmux_socket failed: %v", err)
	}
	if hasSocket {
		t.Fatalf("workers should drop legacy column tmux_socket")
	}
	hasSession, err := tableHasColumn(db2, "workers", "tmux_session")
	if err != nil {
		t.Fatalf("query workers.tmux_session failed: %v", err)
	}
	if hasSession {
		t.Fatalf("workers should drop legacy column tmux_session")
	}

	type countRow struct {
		N int `gorm:"column:n"`
	}
	var row countRow
	if err := db2.Raw("SELECT COUNT(1) AS n FROM sqlite_master WHERE type = 'table' AND name = 'dag_plans';").Scan(&row).Error; err != nil {
		t.Fatalf("query dag_plans table failed: %v", err)
	}
	if row.N != 0 {
		t.Fatalf("dag_plans should be dropped after migrate")
	}
}

func TestOpenAndMigrate_LeanFocusControlPlanePresent(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "dalek.sqlite3")
	db, err := OpenAndMigrate(dbPath)
	if err != nil {
		t.Fatalf("OpenAndMigrate failed: %v", err)
	}

	for _, tc := range []struct {
		table  string
		column string
	}{
		{table: "focus_runs", column: "desired_state"},
		{table: "focus_runs", column: "request_id"},
		{table: "focus_run_items", column: "handoff_ticket_id"},
		{table: "focus_events", column: "payload_json"},
		{table: "tickets", column: "superseded_by_ticket_id"},
	} {
		ok, err := tableHasColumn(db, tc.table, tc.column)
		if err != nil {
			t.Fatalf("tableHasColumn(%s.%s) failed: %v", tc.table, tc.column, err)
		}
		if !ok {
			t.Fatalf("%s should contain column %s", tc.table, tc.column)
		}
	}

	var idx struct {
		SQL string `gorm:"column:sql"`
	}
	if err := db.Raw("SELECT sql FROM sqlite_master WHERE type = 'index' AND name = ?", "idx_focus_runs_active_project").Scan(&idx).Error; err != nil {
		t.Fatalf("query idx_focus_runs_active_project failed: %v", err)
	}
	if !strings.Contains(strings.ToLower(idx.SQL), "where status in ('queued','running','blocked')") {
		t.Fatalf("unexpected focus active index sql: %q", idx.SQL)
	}
}

func TestOpenAndMigrate_LeanFocusControlPlaneReapply(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "dalek.sqlite3")
	db, err := OpenAndMigrate(dbPath)
	if err != nil {
		t.Fatalf("OpenAndMigrate failed: %v", err)
	}

	if err := db.Exec(`DROP TABLE IF EXISTS focus_run_items;`).Error; err != nil {
		t.Fatalf("drop focus_run_items failed: %v", err)
	}
	if err := db.Exec(`DROP TABLE IF EXISTS focus_events;`).Error; err != nil {
		t.Fatalf("drop focus_events failed: %v", err)
	}
	if err := dropTableColumn(db, "tickets", "superseded_by_ticket_id"); err != nil {
		t.Fatalf("drop tickets.superseded_by_ticket_id failed: %v", err)
	}
	if err := db.Exec("DELETE FROM schema_migrations WHERE version >= 20;").Error; err != nil {
		t.Fatalf("rollback schema_migrations for v20 failed: %v", err)
	}
	if _, err := OpenAndMigrate(dbPath); err != nil {
		t.Fatalf("OpenAndMigrate (reapply v20) failed: %v", err)
	}

	db2, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	for _, tc := range []struct {
		table  string
		column string
	}{
		{table: "focus_run_items", column: "handoff_ticket_id"},
		{table: "focus_events", column: "payload_json"},
		{table: "tickets", column: "superseded_by_ticket_id"},
	} {
		ok, err := tableHasColumn(db2, tc.table, tc.column)
		if err != nil {
			t.Fatalf("tableHasColumn(%s.%s) after reapply failed: %v", tc.table, tc.column, err)
		}
		if !ok {
			t.Fatalf("%s should restore column %s after reapply", tc.table, tc.column)
		}
	}
}

type migrationRow struct {
	Version   int    `gorm:"column:version"`
	Name      string `gorm:"column:name"`
	AppliedAt string `gorm:"column:applied_at"`
}

func loadMigrationRowsForTest(t *testing.T, db *gorm.DB) []migrationRow {
	t.Helper()
	var rows []migrationRow
	if err := db.Raw(`
SELECT version, name, applied_at
FROM schema_migrations
ORDER BY version ASC;
`).Scan(&rows).Error; err != nil {
		t.Fatalf("query schema_migrations failed: %v", err)
	}
	return rows
}
