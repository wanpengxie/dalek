package store

import (
	"dalek/internal/contracts"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestOpenAndMigrate_AllowsBasicCRUD(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "dalek.sqlite3")
	db, err := OpenAndMigrate(dbPath)
	if err != nil {
		t.Fatalf("OpenAndMigrate failed: %v", err)
	}

	tk := Ticket{
		Title:          "ticket-1",
		WorkflowStatus: contracts.TicketBacklog,
	}
	if err := db.Create(&tk).Error; err != nil {
		t.Fatalf("create ticket failed: %v", err)
	}
	if tk.ID == 0 {
		t.Fatalf("expected generated ticket ID")
	}

	var got Ticket
	if err := db.First(&got, tk.ID).Error; err != nil {
		t.Fatalf("query ticket failed: %v", err)
	}
	if got.Title != "ticket-1" || got.WorkflowStatus != contracts.TicketBacklog {
		t.Fatalf("unexpected ticket: %+v", got)
	}
}

func TestOpenAndMigrate_ConcurrentOpenDoesNotFailOnTaskStatusView(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "dalek.sqlite3")
	const workers = 8

	var wg sync.WaitGroup
	errCh := make(chan error, workers)
	start := make(chan struct{})

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := OpenAndMigrate(dbPath)
			if err != nil {
				errCh <- err
			}
		}()
	}

	close(start)
	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Fatalf("concurrent OpenAndMigrate failed: %v", err)
	}
}

func TestLockMigrate_BreaksStaleOwnerLock(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "dalek.sqlite3")
	lockDir := dbPath + ".migrate.lock"
	if err := os.Mkdir(lockDir, 0o755); err != nil {
		t.Fatalf("create lock dir failed: %v", err)
	}
	owner := migrateLockOwner{
		PID:       0,
		CreatedAt: time.Now().Add(-time.Minute),
	}
	raw, err := json.Marshal(owner)
	if err != nil {
		t.Fatalf("marshal owner failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(lockDir, migrateLockOwnerFileName), raw, 0o644); err != nil {
		t.Fatalf("write owner file failed: %v", err)
	}

	unlock, err := lockMigrate(dbPath, 150*time.Millisecond)
	if err != nil {
		t.Fatalf("lockMigrate should recover stale owner lock: %v", err)
	}
	unlock()
}

func TestLockMigrate_DoesNotBreakLiveOwnerLock(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "dalek.sqlite3")
	lockDir := dbPath + ".migrate.lock"
	if err := os.Mkdir(lockDir, 0o755); err != nil {
		t.Fatalf("create lock dir failed: %v", err)
	}
	owner := migrateLockOwner{
		PID:       os.Getpid(),
		CreatedAt: time.Now(),
	}
	raw, err := json.Marshal(owner)
	if err != nil {
		t.Fatalf("marshal owner failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(lockDir, migrateLockOwnerFileName), raw, 0o644); err != nil {
		t.Fatalf("write owner file failed: %v", err)
	}

	_, err = lockMigrate(dbPath, 120*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "获取迁移锁超时") {
		t.Fatalf("expected timeout for live owner lock, got=%v", err)
	}
}

func TestIsInMemorySQLitePath(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{path: ":memory:", want: true},
		{path: " :memory: ", want: true},
		{path: "file::memory:?cache=shared", want: true},
		{path: "file:test.db?mode=memory&cache=shared", want: true},
		{path: filepath.Join(t.TempDir(), "dalek.sqlite3"), want: false},
		{path: "", want: false},
	}
	for _, tc := range cases {
		got := isInMemorySQLitePath(tc.path)
		if got != tc.want {
			t.Fatalf("isInMemorySQLitePath(%q)=%v want=%v", tc.path, got, tc.want)
		}
	}
}

func TestOpenAndMigrate_InMemorySkipsLockDir(t *testing.T) {
	wd := t.TempDir()
	prev, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd failed: %v", err)
	}
	if err := os.Chdir(wd); err != nil {
		t.Fatalf("chdir failed: %v", err)
	}
	defer func() {
		_ = os.Chdir(prev)
	}()

	db, err := OpenAndMigrate(":memory:")
	if err != nil {
		t.Fatalf("OpenAndMigrate(:memory:) failed: %v", err)
	}
	if db == nil {
		t.Fatalf("expected non-nil db")
	}

	lockDir := filepath.Join(wd, ":memory:.migrate.lock")
	if _, statErr := os.Stat(lockDir); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("in-memory db should not create migrate lock dir, statErr=%v", statErr)
	}
}

func TestOpenAndMigrate_TicketArchivedTakesPrecedenceOverStatus(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "dalek.sqlite3")
	db, err := OpenAndMigrate(dbPath)
	if err != nil {
		t.Fatalf("OpenAndMigrate failed: %v", err)
	}

	// 模拟历史 schema：tickets.status + tickets.archived
	if err := db.Exec("ALTER TABLE tickets ADD COLUMN status TEXT;").Error; err != nil {
		t.Fatalf("add legacy status column failed: %v", err)
	}
	if err := db.Exec("ALTER TABLE tickets ADD COLUMN archived INTEGER;").Error; err != nil {
		t.Fatalf("add legacy archived column failed: %v", err)
	}
	if err := db.Exec("CREATE INDEX IF NOT EXISTS idx_tickets_status ON tickets(status);").Error; err != nil {
		t.Fatalf("add legacy status index failed: %v", err)
	}
	if err := db.Exec("CREATE INDEX IF NOT EXISTS idx_tickets_archived ON tickets(archived);").Error; err != nil {
		t.Fatalf("add legacy archived index failed: %v", err)
	}

	now := time.Now()
	if err := db.Exec(
		"INSERT INTO tickets (created_at, updated_at, title, description, priority, workflow_status, status, archived) VALUES (?, ?, 'legacy-1', 'd', 0, 'backlog', 'done', 1);",
		now,
		now,
	).Error; err != nil {
		t.Fatalf("insert legacy ticket failed: %v", err)
	}

	// 回退版本标记，模拟“老库停留在 v3，待执行 v4+”。
	if err := db.Exec("DELETE FROM schema_migrations WHERE version >= 4;").Error; err != nil {
		t.Fatalf("rollback schema_migrations failed: %v", err)
	}

	// 再次迁移：应把 archived=1 映射为 workflow_status=archived（不能被 status='done' 覆盖）。
	if _, err := OpenAndMigrate(dbPath); err != nil {
		t.Fatalf("OpenAndMigrate (2nd) failed: %v", err)
	}

	db2, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	var got Ticket
	if err := db2.Order("id asc").First(&got).Error; err != nil {
		t.Fatalf("query ticket failed: %v", err)
	}
	if got.WorkflowStatus != contracts.TicketArchived {
		t.Fatalf("expected workflow_status=archived, got=%s", got.WorkflowStatus)
	}

	type ticketCol struct {
		Name string `gorm:"column:name"`
	}
	var cols []ticketCol
	if err := db2.Raw("PRAGMA table_info(tickets);").Scan(&cols).Error; err != nil {
		t.Fatalf("query ticket columns failed: %v", err)
	}
	for _, c := range cols {
		if c.Name == "status" || c.Name == "archived" {
			t.Fatalf("legacy ticket column should be removed after migrate, got=%s", c.Name)
		}
	}
}

func TestOpenAndMigrate_NormalizesLegacyWorkflowAliases(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "dalek.sqlite3")
	db, err := OpenAndMigrate(dbPath)
	if err != nil {
		t.Fatalf("OpenAndMigrate failed: %v", err)
	}
	now := time.Now()
	if err := db.Exec(
		"INSERT INTO tickets (created_at, updated_at, title, description, priority, workflow_status) VALUES (?, ?, 'legacy-alias', '', 0, 'in_progress');",
		now,
		now,
	).Error; err != nil {
		t.Fatalf("insert legacy ticket failed: %v", err)
	}

	// 回退版本标记，模拟“老库停留在 v3，待执行 v4+”。
	if err := db.Exec("DELETE FROM schema_migrations WHERE version >= 4;").Error; err != nil {
		t.Fatalf("rollback schema_migrations failed: %v", err)
	}

	if _, err := OpenAndMigrate(dbPath); err != nil {
		t.Fatalf("OpenAndMigrate (2nd) failed: %v", err)
	}

	db2, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	var got Ticket
	if err := db2.Where("title = ?", "legacy-alias").First(&got).Error; err != nil {
		t.Fatalf("query ticket failed: %v", err)
	}
	if got.WorkflowStatus != contracts.TicketActive {
		t.Fatalf("expected workflow_status=active after normalize, got=%s", got.WorkflowStatus)
	}
}

func TestOpenAndMigrate_ChannelTablesCRUD(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "dalek.sqlite3")
	db, err := OpenAndMigrate(dbPath)
	if err != nil {
		t.Fatalf("OpenAndMigrate failed: %v", err)
	}

	binding := ChannelBinding{
		ProjectName: "demo",
		ChannelType: contracts.ChannelTypeCLI,
		Adapter:     "cli.local",
		Enabled:     true,
	}
	if err := db.Create(&binding).Error; err != nil {
		t.Fatalf("create binding failed: %v", err)
	}

	conv := ChannelConversation{
		BindingID:          binding.ID,
		PeerConversationID: "conv-1",
		Title:              "demo",
		Summary:            "",
	}
	if err := db.Create(&conv).Error; err != nil {
		t.Fatalf("create conversation failed: %v", err)
	}

	peerID := "msg-1"
	inMsg := ChannelMessage{
		ConversationID: conv.ID,
		Direction:      contracts.ChannelMessageIn,
		Adapter:        "cli.local",
		PeerMessageID:  &peerID,
		SenderID:       "user-1",
		ContentText:    "list tickets",
		PayloadJSON:    contracts.JSONMap{},
		Status:         contracts.ChannelMessageAccepted,
	}
	if err := db.Create(&inMsg).Error; err != nil {
		t.Fatalf("create inbound message failed: %v", err)
	}

	job := ChannelTurnJob{
		ConversationID:   conv.ID,
		InboundMessageID: inMsg.ID,
		Status:           contracts.ChannelTurnPending,
	}
	if err := db.Create(&job).Error; err != nil {
		t.Fatalf("create turn job failed: %v", err)
	}

	outMsg := ChannelMessage{
		ConversationID: conv.ID,
		Direction:      contracts.ChannelMessageOut,
		Adapter:        "cli.local",
		SenderID:       "pm",
		ContentText:    "ok",
		PayloadJSON:    contracts.JSONMap{},
		Status:         contracts.ChannelMessageProcessed,
	}
	if err := db.Create(&outMsg).Error; err != nil {
		t.Fatalf("create outbound message failed: %v", err)
	}

	outbox := ChannelOutbox{
		MessageID:   outMsg.ID,
		Adapter:     "cli.local",
		PayloadJSON: contracts.JSONMap{},
		Status:      contracts.ChannelOutboxPending,
	}
	if err := db.Create(&outbox).Error; err != nil {
		t.Fatalf("create outbox failed: %v", err)
	}
}

func TestOpenAndMigrate_ChannelMessageDedupScopedByConversation(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "dalek.sqlite3")
	db, err := OpenAndMigrate(dbPath)
	if err != nil {
		t.Fatalf("OpenAndMigrate failed: %v", err)
	}

	binding := ChannelBinding{
		ProjectName: "demo",
		ChannelType: contracts.ChannelTypeWeb,
		Adapter:     "web.ws",
		Enabled:     true,
	}
	if err := db.Create(&binding).Error; err != nil {
		t.Fatalf("create binding failed: %v", err)
	}
	conv1 := ChannelConversation{BindingID: binding.ID, PeerConversationID: "conv-1"}
	if err := db.Create(&conv1).Error; err != nil {
		t.Fatalf("create conv1 failed: %v", err)
	}
	conv2 := ChannelConversation{BindingID: binding.ID, PeerConversationID: "conv-2"}
	if err := db.Create(&conv2).Error; err != nil {
		t.Fatalf("create conv2 failed: %v", err)
	}

	peerID := "dup-msg"
	in1 := ChannelMessage{
		ConversationID: conv1.ID,
		Direction:      contracts.ChannelMessageIn,
		Adapter:        "web.ws",
		PeerMessageID:  &peerID,
		SenderID:       "u1",
		ContentText:    "hello-1",
		PayloadJSON:    contracts.JSONMap{},
		Status:         contracts.ChannelMessageAccepted,
	}
	if err := db.Create(&in1).Error; err != nil {
		t.Fatalf("create inbound message conv1 failed: %v", err)
	}
	in2 := ChannelMessage{
		ConversationID: conv2.ID,
		Direction:      contracts.ChannelMessageIn,
		Adapter:        "web.ws",
		PeerMessageID:  &peerID,
		SenderID:       "u2",
		ContentText:    "hello-2",
		PayloadJSON:    contracts.JSONMap{},
		Status:         contracts.ChannelMessageAccepted,
	}
	if err := db.Create(&in2).Error; err != nil {
		t.Fatalf("create inbound message conv2 with same peer_message_id should succeed: %v", err)
	}
}

func TestEnsureChannelMessageDedupIndex_RebuildLegacyDefinition(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "dalek.sqlite3")
	db, err := OpenAndMigrate(dbPath)
	if err != nil {
		t.Fatalf("OpenAndMigrate failed: %v", err)
	}

	if err := db.Exec("DROP INDEX IF EXISTS idx_channel_message_dedup").Error; err != nil {
		t.Fatalf("drop index failed: %v", err)
	}
	if err := db.Exec("CREATE UNIQUE INDEX idx_channel_message_dedup ON channel_messages(direction, adapter, peer_message_id)").Error; err != nil {
		t.Fatalf("create legacy index failed: %v", err)
	}

	if err := ensureChannelMessageDedupIndex(db); err != nil {
		t.Fatalf("ensureChannelMessageDedupIndex failed: %v", err)
	}

	type row struct {
		SQL string `gorm:"column:sql"`
	}
	var got row
	if err := db.Raw("SELECT sql FROM sqlite_master WHERE type = 'index' AND name = ?", "idx_channel_message_dedup").Scan(&got).Error; err != nil {
		t.Fatalf("query index sql failed: %v", err)
	}
	sql := strings.ToLower(strings.TrimSpace(got.SQL))
	if sql == "" || !strings.Contains(sql, "conversation_id") {
		t.Fatalf("dedup index should include conversation_id, got sql=%q", got.SQL)
	}
}

func TestOpenGatewayDB_CreatesParentDirAndMigrates(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(root, "nested", "gateway.db")
	db, err := OpenGatewayDB(dbPath)
	if err != nil {
		t.Fatalf("OpenGatewayDB failed: %v", err)
	}
	if db == nil {
		t.Fatalf("OpenGatewayDB should return non-nil db")
	}
	if _, err := os.Stat(filepath.Dir(dbPath)); err != nil {
		t.Fatalf("parent dir should exist: %v", err)
	}
	var n int64
	if err := db.Model(&ChannelBinding{}).Count(&n).Error; err != nil {
		t.Fatalf("gateway db should be migrated: %v", err)
	}
}

func TestOpenGatewayDB_EmptyPathFails(t *testing.T) {
	if _, err := OpenGatewayDB(""); err == nil {
		t.Fatalf("empty path should fail")
	}
}

func TestOpenAndMigrate_NotebookSchemaHasRequiredColumns(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "dalek.sqlite3")
	db, err := OpenAndMigrate(dbPath)
	if err != nil {
		t.Fatalf("OpenAndMigrate failed: %v", err)
	}

	requiredNoteCols := []string{"project_key", "text", "context_json", "normalized_hash", "status", "shaped_item_id"}
	for _, col := range requiredNoteCols {
		ok, err := tableHasColumn(db, "note_items", col)
		if err != nil {
			t.Fatalf("tableHasColumn(note_items.%s) failed: %v", col, err)
		}
		if !ok {
			t.Fatalf("note_items should contain column: %s", col)
		}
	}
	legacyNoteCols := []string{"raw_text", "ticket_id", "rejected_reason"}
	for _, col := range legacyNoteCols {
		ok, err := tableHasColumn(db, "note_items", col)
		if err != nil {
			t.Fatalf("tableHasColumn(note_items.%s) failed: %v", col, err)
		}
		if ok {
			t.Fatalf("note_items legacy column should be removed: %s", col)
		}
	}

	requiredShapedCols := []string{"project_key", "dedup_key", "review_comment", "status"}
	for _, col := range requiredShapedCols {
		ok, err := tableHasColumn(db, "shaped_items", col)
		if err != nil {
			t.Fatalf("tableHasColumn(shaped_items.%s) failed: %v", col, err)
		}
		if !ok {
			t.Fatalf("shaped_items should contain column: %s", col)
		}
	}
}

func TestDropTableColumn_RejectsUnsafeIdentifier(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "dalek.sqlite3")
	db, err := OpenAndMigrate(dbPath)
	if err != nil {
		t.Fatalf("OpenAndMigrate failed: %v", err)
	}
	err = dropTableColumn(db, "tickets;drop table workers;--", "status")
	if err == nil || !strings.Contains(err.Error(), "非法 SQL 标识符") {
		t.Fatalf("expected invalid identifier error, got=%v", err)
	}
}

func TestOpenAndMigrate_ShapedItemUniqueConstraintByProjectAndDedupKey(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "dalek.sqlite3")
	db, err := OpenAndMigrate(dbPath)
	if err != nil {
		t.Fatalf("OpenAndMigrate failed: %v", err)
	}

	now := time.Now()
	first := ShapedItem{
		ProjectKey:    "p1",
		Status:        ShapedPendingReview,
		Title:         "A",
		Description:   "A",
		DedupKey:      "dup-k",
		SourceNoteIDs: contracts.JSONUintSlice{1},
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := db.Create(&first).Error; err != nil {
		t.Fatalf("create first shaped item failed: %v", err)
	}

	second := ShapedItem{
		ProjectKey:    "p1",
		Status:        ShapedPendingReview,
		Title:         "B",
		Description:   "B",
		DedupKey:      "dup-k",
		SourceNoteIDs: contracts.JSONUintSlice{2},
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := db.Create(&second).Error; err == nil {
		t.Fatalf("duplicate (project_key, dedup_key) should fail")
	}

	otherProject := ShapedItem{
		ProjectKey:    "p2",
		Status:        ShapedPendingReview,
		Title:         "C",
		Description:   "C",
		DedupKey:      "dup-k",
		SourceNoteIDs: contracts.JSONUintSlice{3},
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := db.Create(&otherProject).Error; err != nil {
		t.Fatalf("same dedup_key in different project should succeed: %v", err)
	}
}

func TestOpenAndMigrate_NoteStatusMigrationToShaped(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "dalek.sqlite3")
	db, err := OpenAndMigrate(dbPath)
	if err != nil {
		t.Fatalf("OpenAndMigrate failed: %v", err)
	}

	now := time.Now()
	statuses := []string{"pending_review", "approved", "rejected"}
	for i, st := range statuses {
		if err := db.Exec(
			`INSERT INTO note_items (created_at, updated_at, project_key, status, source, text, context_json, normalized_hash, shaped_item_id, last_error)
			 VALUES (?, ?, ?, ?, 'cli', ?, '', ?, 0, '');`,
			now,
			now,
			"demo",
			st,
			"legacy note",
			st+strings.Repeat("x", i+1),
		).Error; err != nil {
			t.Fatalf("insert legacy note status=%s failed: %v", st, err)
		}
	}

	// 回退版本标记，模拟“老库停留在 v4，待执行 v5+”。
	if err := db.Exec("DELETE FROM schema_migrations WHERE version >= 5;").Error; err != nil {
		t.Fatalf("rollback schema_migrations failed: %v", err)
	}

	if _, err := OpenAndMigrate(dbPath); err != nil {
		t.Fatalf("OpenAndMigrate (2nd) failed: %v", err)
	}

	db2, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	var count int64
	if err := db2.Model(&NoteItem{}).Where("status = ?", NoteShaped).Count(&count).Error; err != nil {
		t.Fatalf("count shaped notes failed: %v", err)
	}
	if count < int64(len(statuses)) {
		t.Fatalf("expected migrated shaped notes >= %d, got=%d", len(statuses), count)
	}
}
