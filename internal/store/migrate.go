package store

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"
)

// Migration 定义单个可版本化迁移步骤。
type Migration struct {
	Version int
	Name    string
	Up      func(db *gorm.DB) error
}

func storeMigrations() []Migration {
	return []Migration{
		{
			Version: 1,
			Name:    "baseline_schema",
			Up:      migrateBaselineSchema,
		},
		{
			Version: 2,
			Name:    "drop_worker_events_table",
			Up:      migrateDropWorkerEventsTable,
		},
		{
			Version: 3,
			Name:    "drop_workers_legacy_runtime_columns",
			Up:      migrateDropWorkersLegacyRuntimeColumns,
		},
		{
			Version: 4,
			Name:    "migrate_ticket_workflow_status",
			Up:      migrateTicketWorkflowStatus,
		},
		{
			Version: 5,
			Name:    "migrate_notebook_schema",
			Up:      migrateNotebookSchema,
		},
		{
			Version: 6,
			Name:    "ensure_task_status_view",
			Up:      ensureTaskStatusView,
		},
		{
			Version: 7,
			Name:    "ensure_channel_message_dedup_index",
			Up:      ensureChannelMessageDedupIndex,
		},
		{
			Version: 8,
			Name:    "add_worker_zombie_retry_fields",
			Up:      migrateAddWorkerZombieRetryFields,
		},
		{
			Version: 9,
			Name:    "add_worker_process_fields",
			Up:      migrateAddWorkerProcessFields,
		},
	}
}

// RunMigrations 按 version 顺序执行未应用的迁移，并写入 schema_migrations。
func RunMigrations(db *gorm.DB, migrations []Migration) error {
	if db == nil {
		return fmt.Errorf("db 为空")
	}
	ordered, err := normalizeMigrations(migrations)
	if err != nil {
		return err
	}
	if err := ensureSchemaMigrationsTable(db); err != nil {
		return err
	}
	applied, err := loadAppliedMigrationVersions(db)
	if err != nil {
		return err
	}
	for _, migration := range ordered {
		if applied[migration.Version] {
			continue
		}
		if err := migration.Up(db); err != nil {
			return fmt.Errorf("执行迁移 v%d(%s) 失败: %w", migration.Version, migration.Name, err)
		}
		if err := recordAppliedMigration(db, migration.Version, migration.Name); err != nil {
			return fmt.Errorf("记录迁移版本 v%d(%s) 失败: %w", migration.Version, migration.Name, err)
		}
	}
	return nil
}

func migrateBaselineSchema(db *gorm.DB) error {
	return db.AutoMigrate(
		&Ticket{},
		&Worker{},
		&PMState{},
		&PMDispatchJob{},
		&InboxItem{},
		&MergeItem{},
		&TaskRun{},
		&SubagentRun{},
		&TaskRuntimeSample{},
		&TaskSemanticReport{},
		&TaskEvent{},
		&TicketWorkflowEvent{},
		&WorkerStatusEvent{},
		&NoteItem{},
		&ShapedItem{},
		&ChannelBinding{},
		&ChannelConversation{},
		&ChannelMessage{},
		&ChannelTurnJob{},
		&ChannelPendingAction{},
		&ChannelOutbox{},
		&EventBusLog{},
	)
}

func migrateDropWorkerEventsTable(db *gorm.DB) error {
	return db.Exec(`DROP TABLE IF EXISTS worker_events;`).Error
}

func migrateDropWorkersLegacyRuntimeColumns(db *gorm.DB) error {
	for _, col := range []string{"runtime_state", "runtime_needs_user", "runtime_summary"} {
		if err := db.Exec(fmt.Sprintf(`ALTER TABLE workers DROP COLUMN %s;`, col)).Error; err != nil {
			msg := strings.ToLower(strings.TrimSpace(err.Error()))
			if !strings.Contains(msg, "no such column") {
				return err
			}
		}
	}
	return nil
}

func migrateAddWorkerZombieRetryFields(db *gorm.DB) error {
	statements := []string{
		`ALTER TABLE workers ADD COLUMN retry_count INTEGER NOT NULL DEFAULT 0;`,
		`ALTER TABLE workers ADD COLUMN last_retry_at TEXT DEFAULT NULL;`,
		`ALTER TABLE workers ADD COLUMN last_error_hash TEXT NOT NULL DEFAULT '';`,
	}
	for _, stmt := range statements {
		if err := db.Exec(stmt).Error; err != nil {
			msg := strings.ToLower(strings.TrimSpace(err.Error()))
			if strings.Contains(msg, "duplicate column name") {
				continue
			}
			return err
		}
	}
	return nil
}

func migrateAddWorkerProcessFields(db *gorm.DB) error {
	statements := []string{
		`ALTER TABLE workers ADD COLUMN process_pid INTEGER NOT NULL DEFAULT 0;`,
		`ALTER TABLE workers ADD COLUMN log_path TEXT NOT NULL DEFAULT '';`,
	}
	for _, stmt := range statements {
		if err := db.Exec(stmt).Error; err != nil {
			msg := strings.ToLower(strings.TrimSpace(err.Error()))
			if strings.Contains(msg, "duplicate column name") {
				continue
			}
			return err
		}
	}
	return nil
}

func normalizeMigrations(migrations []Migration) ([]Migration, error) {
	if len(migrations) == 0 {
		return nil, nil
	}
	ordered := append([]Migration(nil), migrations...)
	sort.Slice(ordered, func(i, j int) bool {
		return ordered[i].Version < ordered[j].Version
	})

	seen := make(map[int]struct{}, len(ordered))
	for i := range ordered {
		migration := ordered[i]
		if migration.Version <= 0 {
			return nil, fmt.Errorf("迁移版本号必须大于 0: %d", migration.Version)
		}
		if migration.Up == nil {
			return nil, fmt.Errorf("迁移版本 v%d(%s) 缺少 Up", migration.Version, migration.Name)
		}
		if _, exists := seen[migration.Version]; exists {
			return nil, fmt.Errorf("迁移版本重复: v%d", migration.Version)
		}
		seen[migration.Version] = struct{}{}
	}
	return ordered, nil
}

func ensureSchemaMigrationsTable(db *gorm.DB) error {
	return db.Exec(`
CREATE TABLE IF NOT EXISTS schema_migrations (
	version INTEGER PRIMARY KEY,
	name TEXT NOT NULL DEFAULT '',
	applied_at TEXT NOT NULL DEFAULT ''
);
`).Error
}

func loadAppliedMigrationVersions(db *gorm.DB) (map[int]bool, error) {
	type row struct {
		Version int `gorm:"column:version"`
	}
	var rows []row
	if err := db.Raw("SELECT version FROM schema_migrations;").Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := make(map[int]bool, len(rows))
	for _, item := range rows {
		out[item.Version] = true
	}
	return out, nil
}

func recordAppliedMigration(db *gorm.DB, version int, name string) error {
	ts := time.Now().UTC().Format(time.RFC3339)
	return db.Exec(`
INSERT INTO schema_migrations(version, name, applied_at)
VALUES (?, ?, ?)
ON CONFLICT(version) DO NOTHING;
`, version, strings.TrimSpace(name), ts).Error
}
