package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const (
	migrateLockOwnerFileName = "owner.json"
)

var sqlIdentifierPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

type migrateLockOwner struct {
	PID       int       `json:"pid"`
	CreatedAt time.Time `json:"created_at"`
}

func Open(path string) (*gorm.DB, error) {
	dsn := fmt.Sprintf("%s?_busy_timeout=%d&_journal_mode=WAL", path, 2000)
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		return nil, err
	}
	sqlDB, err := db.DB()
	if err != nil {
		return nil, err
	}
	sqlDB.SetConnMaxLifetime(30 * time.Minute)
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)
	return db, nil
}

// ApplyMigrations 按版本化迁移清单执行 store schema 迁移。
func ApplyMigrations(db *gorm.DB) error {
	return RunMigrations(db, storeMigrations())
}

func OpenAndMigrate(path string) (*gorm.DB, error) {
	if !isInMemorySQLitePath(path) {
		unlock, err := lockMigrate(path, 10*time.Second)
		if err != nil {
			return nil, err
		}
		defer unlock()
	}

	db, err := Open(path)
	if err != nil {
		return nil, err
	}
	if err := ApplyMigrations(db); err != nil {
		return nil, err
	}
	return db, nil
}

// OpenGatewayDB 打开并迁移全局 gateway 数据库（默认路径建议 ~/.dalek/gateway.db）。
func OpenGatewayDB(path string) (*gorm.DB, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, fmt.Errorf("gateway db path 为空")
	}
	if !isInMemorySQLitePath(path) {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return nil, fmt.Errorf("创建 gateway db 目录失败: %w", err)
		}
	}
	return OpenAndMigrate(path)
}

func isInMemorySQLitePath(path string) bool {
	normalized := strings.ToLower(strings.TrimSpace(path))
	if normalized == "" {
		return false
	}
	if normalized == ":memory:" {
		return true
	}
	if strings.HasPrefix(normalized, "file::memory:") {
		return true
	}
	if strings.HasPrefix(normalized, "file:") && strings.Contains(normalized, "mode=memory") {
		return true
	}
	return false
}

func lockMigrate(dbPath string, timeout time.Duration) (func(), error) {
	dbPath = strings.TrimSpace(dbPath)
	if dbPath == "" {
		return nil, fmt.Errorf("dbPath 为空，无法加迁移锁")
	}
	lockDir := dbPath + ".migrate.lock"
	deadline := time.Now().Add(timeout)
	for {
		err := os.Mkdir(lockDir, 0o755)
		if err == nil {
			if werr := writeMigrateLockOwner(lockDir); werr != nil {
				_ = os.RemoveAll(lockDir)
				return nil, werr
			}
			return func() { _ = os.RemoveAll(lockDir) }, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, err
		}
		recovered, rerr := tryBreakStaleMigrateLock(lockDir, timeout)
		if rerr != nil {
			return nil, rerr
		}
		if recovered {
			continue
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("获取迁移锁超时: %s", filepath.Base(lockDir))
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func writeMigrateLockOwner(lockDir string) error {
	owner := migrateLockOwner{
		PID:       os.Getpid(),
		CreatedAt: time.Now().UTC(),
	}
	raw, err := json.Marshal(owner)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(lockDir, migrateLockOwnerFileName), raw, 0o644)
}

func readMigrateLockOwner(lockDir string) (migrateLockOwner, bool, error) {
	raw, err := os.ReadFile(filepath.Join(lockDir, migrateLockOwnerFileName))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return migrateLockOwner{}, false, nil
		}
		return migrateLockOwner{}, false, err
	}
	var owner migrateLockOwner
	if err := json.Unmarshal(raw, &owner); err != nil {
		// owner 文件损坏视为遗留锁，后续按既有逻辑判定。
		return migrateLockOwner{}, false, nil
	}
	return owner, true, nil
}

func tryBreakStaleMigrateLock(lockDir string, timeout time.Duration) (bool, error) {
	owner, ok, err := readMigrateLockOwner(lockDir)
	if err != nil {
		return false, err
	}
	if ok {
		if processExists(owner.PID) {
			return false, nil
		}
		if err := os.RemoveAll(lockDir); err != nil && !errors.Is(err, os.ErrNotExist) {
			return false, err
		}
		return true, nil
	}

	info, err := os.Stat(lockDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	staleAfter := timeout
	if staleAfter < 30*time.Second {
		staleAfter = 30 * time.Second
	}
	if time.Since(info.ModTime()) < staleAfter {
		return false, nil
	}
	if err := os.RemoveAll(lockDir); err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	return true, nil
}

func processExists(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

func ensureTaskStatusView(db *gorm.DB) error {
	if err := db.Exec(`DROP VIEW IF EXISTS task_status_view;`).Error; err != nil {
		return err
	}
	sql := `
CREATE VIEW task_status_view AS
SELECT
	tr.id AS run_id,
	tr.owner_type AS owner_type,
	tr.task_type AS task_type,
	tr.project_key AS project_key,
	tr.ticket_id AS ticket_id,
	tr.worker_id AS worker_id,
	tr.subject_type AS subject_type,
	tr.subject_id AS subject_id,
	tr.orchestration_state AS orchestration_state,
	tr.runner_id AS runner_id,
	tr.lease_expires_at AS lease_expires_at,
	tr.attempt AS attempt,
	tr.error_code AS error_code,
	tr.error_message AS error_message,
	tr.started_at AS started_at,
	tr.finished_at AS finished_at,
	tr.created_at AS created_at,
	tr.updated_at AS updated_at,
	COALESCE(rs.runtime_health_state, 'unknown') AS runtime_health_state,
	COALESCE(rs.needs_user, 0) AS runtime_needs_user,
	COALESCE(rs.summary, '') AS runtime_summary,
	rs.observed_at AS runtime_observed_at,
	COALESCE(sr.semantic_phase, '') AS semantic_phase,
	COALESCE(sr.milestone, '') AS semantic_milestone,
	COALESCE(sr.next_action, '') AS semantic_next_action,
	COALESCE(sr.summary, '') AS semantic_summary,
	sr.reported_at AS semantic_reported_at,
	COALESCE(ev.event_type, '') AS last_event_type,
	COALESCE(ev.note, '') AS last_event_note,
	ev.created_at AS last_event_at
FROM task_runs tr
LEFT JOIN task_runtime_samples rs ON rs.id = (
	SELECT x.id
	FROM task_runtime_samples x
	WHERE x.task_run_id = tr.id
	ORDER BY x.observed_at DESC, x.id DESC
	LIMIT 1
)
LEFT JOIN task_semantic_reports sr ON sr.id = (
	SELECT x.id
	FROM task_semantic_reports x
	WHERE x.task_run_id = tr.id
	ORDER BY x.reported_at DESC, x.id DESC
	LIMIT 1
)
LEFT JOIN task_events ev ON ev.id = (
	SELECT x.id
	FROM task_events x
	WHERE x.task_run_id = tr.id
	ORDER BY x.created_at DESC, x.id DESC
	LIMIT 1
);
`
	err := db.Exec(sql).Error
	if err == nil {
		return nil
	}
	// 并发 OpenAndMigrate 时，若另一进程已创建同名视图，视为可用。
	if strings.Contains(strings.ToLower(err.Error()), "already exists") {
		return nil
	}
	return err
}

func migrateTicketWorkflowStatus(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("db 为空")
	}

	// 1) 回填 workflow_status（若历史列仍存在则迁移；不存在则忽略）。
	// 注意：archived=1 是“终态”，必须优先级最高，不能被 status 覆盖。
	if err := db.Exec(`
	UPDATE tickets
	SET workflow_status = CASE TRIM(status)
		WHEN 'backlog' THEN 'backlog'
		WHEN 'queued' THEN 'queued'
		WHEN 'running' THEN 'active'
		WHEN 'active' THEN 'active'
		WHEN 'blocked' THEN 'blocked'
		WHEN 'done' THEN 'done'
		WHEN 'archived' THEN 'archived'
		ELSE workflow_status
	END
	WHERE TRIM(COALESCE(status, '')) != '' AND TRIM(COALESCE(workflow_status, '')) != 'archived';
	`).Error; err != nil {
		msg := strings.ToLower(strings.TrimSpace(err.Error()))
		if !strings.Contains(msg, "no such column") {
			return err
		}
	}

	if err := db.Exec(`
UPDATE tickets
SET workflow_status = 'archived'
WHERE archived = 1;
`).Error; err != nil {
		msg := strings.ToLower(strings.TrimSpace(err.Error()))
		if !strings.Contains(msg, "no such column") {
			return err
		}
	}

	// 1.5) 规范化历史别名，避免旧状态值在新 UI 中被当作未知状态。
	if err := db.Exec(`
	UPDATE tickets
	SET workflow_status = CASE TRIM(LOWER(COALESCE(workflow_status, '')))
		WHEN '' THEN 'backlog'
		WHEN 'queue' THEN 'queued'
		WHEN 'running' THEN 'active'
		WHEN 'in_progress' THEN 'active'
		WHEN 'in-progress' THEN 'active'
		WHEN 'inprogress' THEN 'active'
		WHEN 'wait_user' THEN 'blocked'
		WHEN 'waiting_user' THEN 'blocked'
		WHEN 'wait-user' THEN 'blocked'
		WHEN 'completed' THEN 'done'
		WHEN 'archive' THEN 'archived'
		ELSE TRIM(LOWER(COALESCE(workflow_status, '')))
	END;
	`).Error; err != nil {
		return err
	}

	// 2) 硬切：删除旧列（若不存在则忽略）。
	for _, col := range []string{"archived", "status"} {
		if err := dropTicketIndexesByColumn(db, col); err != nil {
			return err
		}
		if err := db.Exec(fmt.Sprintf(`ALTER TABLE tickets DROP COLUMN %s;`, col)).Error; err != nil {
			msg := strings.ToLower(strings.TrimSpace(err.Error()))
			// “error in index ... no such column” 不是列不存在，而是索引阻塞删列；先清索引再重试一次。
			if strings.Contains(msg, "error in index") && strings.Contains(msg, "no such column") {
				if derr := dropTicketIndexesByColumn(db, col); derr != nil {
					return derr
				}
				if rerr := db.Exec(fmt.Sprintf(`ALTER TABLE tickets DROP COLUMN %s;`, col)).Error; rerr == nil {
					continue
				} else {
					rmsg := strings.ToLower(strings.TrimSpace(rerr.Error()))
					if !strings.Contains(rmsg, "no such column") || strings.Contains(rmsg, "error in index") {
						return rerr
					}
					continue
				}
			}
			if !strings.Contains(msg, "no such column") {
				return err
			}
		}
	}
	return nil
}

func dropTicketIndexesByColumn(db *gorm.DB, col string) error {
	return dropTableIndexesByColumn(db, "tickets", col)
}

func migrateNotebookSchema(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("db 为空")
	}
	if err := backfillNoteItemsPRD(db); err != nil {
		return err
	}
	if err := backfillShapedItemsPRD(db); err != nil {
		return err
	}
	if err := normalizeShapedItemDedupConflicts(db); err != nil {
		return err
	}
	if err := ensureShapedItemProjectDedupIndex(db); err != nil {
		return err
	}
	// 开发态硬切：移除 NoteItem 的历史旧列，避免新逻辑继续依赖。
	for _, col := range []string{"raw_text", "ticket_id", "rejected_reason"} {
		if err := dropTableColumn(db, "note_items", col); err != nil {
			return err
		}
	}
	return nil
}

func backfillNoteItemsPRD(db *gorm.DB) error {
	hasText, err := tableHasColumn(db, "note_items", "text")
	if err != nil {
		return err
	}
	hasRawText, err := tableHasColumn(db, "note_items", "raw_text")
	if err != nil {
		return err
	}
	if hasText && hasRawText {
		if err := db.Exec(`
UPDATE note_items
SET text = raw_text
WHERE TRIM(COALESCE(text, '')) = '' AND TRIM(COALESCE(raw_text, '')) != '';
`).Error; err != nil {
			return err
		}
	}

	if err := db.Exec(`
UPDATE note_items
SET status = 'shaped'
WHERE TRIM(LOWER(COALESCE(status, ''))) IN ('pending_review', 'approved', 'rejected');
`).Error; err != nil {
		return err
	}

	hasProjectKey, err := tableHasColumn(db, "note_items", "project_key")
	if err != nil {
		return err
	}
	if hasProjectKey {
		if err := db.Exec(`
UPDATE note_items
SET project_key = ''
WHERE project_key IS NULL;
`).Error; err != nil {
			return err
		}
	}

	hasContextJSON, err := tableHasColumn(db, "note_items", "context_json")
	if err != nil {
		return err
	}
	if hasContextJSON {
		if err := db.Exec(`
UPDATE note_items
SET context_json = ''
WHERE context_json IS NULL;
`).Error; err != nil {
			return err
		}
	}

	if err := db.Exec(`
UPDATE note_items
SET source = 'cli'
WHERE TRIM(COALESCE(source, '')) = '';
`).Error; err != nil {
		return err
	}
	return nil
}

func backfillShapedItemsPRD(db *gorm.DB) error {
	hasReviewComment, err := tableHasColumn(db, "shaped_items", "review_comment")
	if err != nil {
		return err
	}
	hasRejectedReason, err := tableHasColumn(db, "shaped_items", "rejected_reason")
	if err != nil {
		return err
	}
	if hasReviewComment && hasRejectedReason {
		if err := db.Exec(`
UPDATE shaped_items
SET review_comment = rejected_reason
WHERE TRIM(COALESCE(review_comment, '')) = '' AND TRIM(COALESCE(rejected_reason, '')) != '';
`).Error; err != nil {
			return err
		}
	}

	hasProjectKey, err := tableHasColumn(db, "shaped_items", "project_key")
	if err != nil {
		return err
	}
	if hasProjectKey {
		if err := db.Exec(`
UPDATE shaped_items
SET project_key = ''
WHERE project_key IS NULL;
`).Error; err != nil {
			return err
		}
	}
	if err := db.Exec(`
UPDATE shaped_items
SET source_note_ids = '[]'
WHERE TRIM(COALESCE(source_note_ids, '')) = '';
`).Error; err != nil {
		return err
	}
	if err := db.Exec(`
UPDATE shaped_items
SET acceptance_json = '[]'
WHERE TRIM(COALESCE(acceptance_json, '')) = '';
`).Error; err != nil {
		return err
	}
	return nil
}

func normalizeShapedItemDedupConflicts(db *gorm.DB) error {
	type row struct {
		ID            uint   `gorm:"column:id"`
		ProjectKey    string `gorm:"column:project_key"`
		DedupKey      string `gorm:"column:dedup_key"`
		SourceNoteIDs string `gorm:"column:source_note_ids"`
	}
	var rows []row
	if err := db.Raw(`
SELECT id, project_key, dedup_key, source_note_ids
FROM shaped_items
WHERE TRIM(COALESCE(dedup_key, '')) != ''
ORDER BY project_key ASC, dedup_key ASC, id DESC;
`).Scan(&rows).Error; err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}

	groups := map[string][]row{}
	for _, it := range rows {
		key := strings.TrimSpace(it.ProjectKey) + "\n" + strings.TrimSpace(it.DedupKey)
		groups[key] = append(groups[key], it)
	}
	now := time.Now()
	for _, items := range groups {
		if len(items) <= 1 {
			continue
		}
		keeper := items[0]
		merged := strings.TrimSpace(keeper.SourceNoteIDs)
		dupIDs := make([]uint, 0, len(items)-1)
		for i := 1; i < len(items); i++ {
			merged = mergeJSONUintArrays(merged, items[i].SourceNoteIDs)
			dupIDs = append(dupIDs, items[i].ID)
		}
		if strings.TrimSpace(merged) == "" {
			merged = "[]"
		}
		if err := db.Model(&ShapedItem{}).
			Where("id = ?", keeper.ID).
			Updates(map[string]any{
				"source_note_ids": merged,
				"updated_at":      now,
			}).Error; err != nil {
			return err
		}
		if len(dupIDs) > 0 {
			if err := db.Model(&ShapedItem{}).
				Where("id IN ?", dupIDs).
				Updates(map[string]any{
					"dedup_key":  "",
					"updated_at": now,
				}).Error; err != nil {
				return err
			}
		}
	}
	return nil
}

func ensureShapedItemProjectDedupIndex(db *gorm.DB) error {
	type indexDefRow struct {
		SQL string `gorm:"column:sql"`
	}
	var row indexDefRow
	if err := db.Raw("SELECT sql FROM sqlite_master WHERE type = 'index' AND name = ?", "idx_shaped_items_project_dedup").Scan(&row).Error; err != nil {
		return err
	}
	def := strings.ToLower(strings.TrimSpace(row.SQL))
	if def != "" &&
		strings.Contains(def, "unique") &&
		strings.Contains(def, "project_key") &&
		strings.Contains(def, "dedup_key") {
		return nil
	}
	if def != "" {
		if err := db.Exec("DROP INDEX IF EXISTS idx_shaped_items_project_dedup").Error; err != nil {
			return err
		}
	}
	return db.Exec(`
CREATE UNIQUE INDEX IF NOT EXISTS idx_shaped_items_project_dedup
ON shaped_items(project_key, dedup_key)
WHERE TRIM(COALESCE(dedup_key, '')) != '';
`).Error
}

func dropTableColumn(db *gorm.DB, table string, col string) error {
	if db == nil {
		return nil
	}
	var err error
	table, err = normalizeSQLIdentifier(table)
	if err != nil {
		return err
	}
	col, err = normalizeSQLIdentifier(col)
	if err != nil {
		return err
	}
	if table == "" || col == "" {
		return nil
	}
	if err := dropTableIndexesByColumn(db, table, col); err != nil {
		return err
	}
	if err := db.Exec(fmt.Sprintf(`ALTER TABLE %s DROP COLUMN %s;`, table, col)).Error; err != nil {
		msg := strings.ToLower(strings.TrimSpace(err.Error()))
		if strings.Contains(msg, "error in index") && strings.Contains(msg, "no such column") {
			if derr := dropTableIndexesByColumn(db, table, col); derr != nil {
				return derr
			}
			if rerr := db.Exec(fmt.Sprintf(`ALTER TABLE %s DROP COLUMN %s;`, table, col)).Error; rerr == nil {
				return nil
			} else {
				rmsg := strings.ToLower(strings.TrimSpace(rerr.Error()))
				if !strings.Contains(rmsg, "no such column") || strings.Contains(rmsg, "error in index") {
					return rerr
				}
				return nil
			}
		}
		if !strings.Contains(msg, "no such column") {
			return err
		}
	}
	return nil
}

func dropTableIndexesByColumn(db *gorm.DB, table string, col string) error {
	if db == nil {
		return nil
	}
	var err error
	table, err = normalizeSQLIdentifier(table)
	if err != nil {
		return err
	}
	col, err = normalizeSQLIdentifier(col)
	if err != nil {
		return err
	}
	if table == "" || col == "" {
		return nil
	}
	type idxRow struct {
		Name string `gorm:"column:name"`
	}
	var indexes []idxRow
	if err := db.Raw(fmt.Sprintf("PRAGMA index_list(%s);", table)).Scan(&indexes).Error; err != nil {
		return err
	}
	type colRow struct {
		Name string `gorm:"column:name"`
	}
	for _, idx := range indexes {
		name := strings.TrimSpace(idx.Name)
		if name == "" {
			continue
		}
		if err := validateSQLIdentifier(name); err != nil {
			return err
		}
		var cols []colRow
		if err := db.Raw(fmt.Sprintf("PRAGMA index_info(%s);", name)).Scan(&cols).Error; err != nil {
			return err
		}
		matched := false
		for _, c := range cols {
			if strings.EqualFold(strings.TrimSpace(c.Name), col) {
				matched = true
				break
			}
		}
		if !matched {
			continue
		}
		if err := db.Exec(fmt.Sprintf(`DROP INDEX IF EXISTS "%s";`, name)).Error; err != nil {
			return err
		}
	}
	return nil
}

func tableHasColumn(db *gorm.DB, table string, col string) (bool, error) {
	if db == nil {
		return false, nil
	}
	var err error
	table, err = normalizeSQLIdentifier(table)
	if err != nil {
		return false, err
	}
	col, err = normalizeSQLIdentifier(col)
	if err != nil {
		return false, err
	}
	if table == "" || col == "" {
		return false, nil
	}
	type colRow struct {
		Name string `gorm:"column:name"`
	}
	var cols []colRow
	if err := db.Raw(fmt.Sprintf("PRAGMA table_info(%s);", table)).Scan(&cols).Error; err != nil {
		return false, err
	}
	for _, item := range cols {
		if strings.EqualFold(strings.TrimSpace(item.Name), col) {
			return true, nil
		}
	}
	return false, nil
}

func normalizeSQLIdentifier(raw string) (string, error) {
	name := strings.TrimSpace(strings.ToLower(raw))
	if name == "" {
		return "", nil
	}
	if !sqlIdentifierPattern.MatchString(name) {
		return "", fmt.Errorf("非法 SQL 标识符: %q", raw)
	}
	return name, nil
}

func validateSQLIdentifier(raw string) error {
	name := strings.TrimSpace(raw)
	if name == "" {
		return fmt.Errorf("SQL 标识符为空")
	}
	if !sqlIdentifierPattern.MatchString(name) {
		return fmt.Errorf("非法 SQL 标识符: %q", raw)
	}
	return nil
}

func mergeJSONUintArrays(a string, b string) string {
	aa := parseUintJSON(a)
	bb := parseUintJSON(b)
	mergedSet := map[uint]bool{}
	for _, id := range aa {
		if id != 0 {
			mergedSet[id] = true
		}
	}
	for _, id := range bb {
		if id != 0 {
			mergedSet[id] = true
		}
	}
	if len(mergedSet) == 0 {
		return "[]"
	}
	merged := make([]uint, 0, len(mergedSet))
	for id := range mergedSet {
		merged = append(merged, id)
	}
	sort.Slice(merged, func(i, j int) bool { return merged[i] < merged[j] })
	raw, err := json.Marshal(merged)
	if err != nil {
		return "[]"
	}
	return string(raw)
}

func parseUintJSON(raw string) []uint {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var out []uint
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil
	}
	return out
}

func ensureChannelMessageDedupIndex(db *gorm.DB) error {
	// 历史版本的去重键缺少 conversation_id；仅在定义不正确时重建索引。
	type indexDefRow struct {
		SQL string `gorm:"column:sql"`
	}
	var row indexDefRow
	if err := db.Raw("SELECT sql FROM sqlite_master WHERE type = 'index' AND name = ?", "idx_channel_message_dedup").Scan(&row).Error; err != nil {
		return err
	}
	def := strings.ToLower(strings.TrimSpace(row.SQL))
	if def != "" && strings.Contains(def, "conversation_id") {
		return nil
	}
	if def != "" {
		if err := db.Exec("DROP INDEX IF EXISTS idx_channel_message_dedup").Error; err != nil {
			return err
		}
	}
	return db.Exec("CREATE UNIQUE INDEX IF NOT EXISTS idx_channel_message_dedup ON channel_messages(direction, conversation_id, adapter, peer_message_id)").Error
}
