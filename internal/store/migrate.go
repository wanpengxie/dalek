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
			Name:    "drop_workers_runtime_columns",
			Up:      migrateDropWorkersRuntimeColumns,
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
			Name:    "ensure_worker_log_path_column",
			Up:      migrateEnsureWorkerLogPathColumn,
		},
		{
			Version: 10,
			Name:    "drop_worker_process_pid_columns",
			Up:      migrateDropWorkerProcessPIDColumns,
		},
		{
			Version: 11,
			Name:    "drop_legacy_worker_tmux_and_dag_plans",
			Up:      migrateDropLegacyWorkerTmuxAndDagPlans,
		},
		{
			Version: 12,
			Name:    "ensure_worker_last_retry_at_datetime",
			Up:      migrateEnsureWorkerLastRetryAtDateTime,
		},
		{
			Version: 13,
			Name:    "ensure_ticket_label_column",
			Up:      migrateEnsureTicketLabelColumn,
		},
		{
			Version: 14,
			Name:    "ensure_pm_state_planner_columns",
			Up:      migrateEnsurePMStatePlannerColumns,
		},
		{
			Version: 15,
			Name:    "add_pmops_journal_checkpoint_tables",
			Up:      migrateAddPMOpsJournalCheckpointTables,
		},
		{
			Version: 16,
			Name:    "add_ticket_integration_columns",
			Up:      migrateAddTicketIntegrationColumns,
		},
		{
			Version: 17,
			Name:    "add_ticket_lifecycle_events",
			Up:      migrateAddTicketLifecycleEvents,
		},
	}
}

func LatestMigrationVersion() int {
	migrations := storeMigrations()
	latest := 0
	for _, migration := range migrations {
		if migration.Version > latest {
			latest = migration.Version
		}
	}
	return latest
}

func CurrentMigrationVersion(db *gorm.DB) (int, error) {
	if db == nil {
		return 0, fmt.Errorf("db 为空")
	}
	if err := ensureSchemaMigrationsTable(db); err != nil {
		return 0, err
	}
	var row struct {
		MaxVersion int `gorm:"column:max_version"`
	}
	if err := db.Raw("SELECT COALESCE(MAX(version), 0) AS max_version FROM schema_migrations;").Scan(&row).Error; err != nil {
		return 0, err
	}
	return row.MaxVersion, nil
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
		&InboxItem{},
		&MergeItem{},
		&TaskRun{},
		&SubagentRun{},
		&TaskRuntimeSample{},
		&TaskSemanticReport{},
		&TaskEvent{},
		&TicketWorkflowEvent{},
		&TicketLifecycleEvent{},
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

func migrateDropWorkersRuntimeColumns(db *gorm.DB) error {
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

func migrateEnsureWorkerLogPathColumn(db *gorm.DB) error {
	has, err := tableHasColumn(db, "workers", "log_path")
	if err != nil {
		return err
	}
	if has {
		return nil
	}
	if err := db.Exec(`ALTER TABLE workers ADD COLUMN log_path TEXT NOT NULL DEFAULT '';`).Error; err != nil {
		msg := strings.ToLower(strings.TrimSpace(err.Error()))
		if strings.Contains(msg, "duplicate column name") {
			return nil
		}
		return err
	}
	return nil
}

func migrateDropWorkerProcessPIDColumns(db *gorm.DB) error {
	if err := migrateEnsureWorkerLogPathColumn(db); err != nil {
		return err
	}
	for _, col := range []string{"process_pid", "process_p_id"} {
		if err := dropTableColumn(db, "workers", col); err != nil {
			return err
		}
	}
	return nil
}

func migrateDropLegacyWorkerTmuxAndDagPlans(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("db 为空")
	}
	for _, col := range []string{"tmux_socket", "tmux_session"} {
		if err := dropTableColumn(db, "workers", col); err != nil {
			return err
		}
	}
	if err := db.Exec(`DROP TABLE IF EXISTS dag_plans;`).Error; err != nil {
		return err
	}
	return nil
}

func migrateEnsureWorkerLastRetryAtDateTime(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("db 为空")
	}
	has, err := tableHasColumn(db, "workers", "last_retry_at")
	if err != nil {
		return err
	}
	if !has {
		return nil
	}

	colType, err := tableColumnType(db, "workers", "last_retry_at")
	if err != nil {
		return err
	}
	switch strings.ToLower(strings.TrimSpace(colType)) {
	case "datetime", "timestamp":
		return nil
	}

	const tempCol = "last_retry_at_tmp_datetime"
	hasTemp, err := tableHasColumn(db, "workers", tempCol)
	if err != nil {
		return err
	}
	if !hasTemp {
		if err := db.Exec(`ALTER TABLE workers ADD COLUMN last_retry_at_tmp_datetime datetime;`).Error; err != nil {
			msg := strings.ToLower(strings.TrimSpace(err.Error()))
			if !strings.Contains(msg, "duplicate column name") {
				return err
			}
		}
	}

	if err := db.Exec(`
UPDATE workers
SET last_retry_at_tmp_datetime = CASE
	WHEN TRIM(COALESCE(last_retry_at, '')) = '' THEN NULL
	ELSE last_retry_at
END;
`).Error; err != nil {
		return err
	}

	if err := dropTableColumn(db, "workers", "last_retry_at"); err != nil {
		return err
	}
	if err := db.Exec(`ALTER TABLE workers RENAME COLUMN last_retry_at_tmp_datetime TO last_retry_at;`).Error; err != nil {
		msg := strings.ToLower(strings.TrimSpace(err.Error()))
		if !strings.Contains(msg, "duplicate column name") {
			return err
		}
	}
	return nil
}

func tableColumnType(db *gorm.DB, table string, col string) (string, error) {
	if db == nil {
		return "", fmt.Errorf("db 为空")
	}
	var err error
	table, err = normalizeSQLIdentifier(table)
	if err != nil {
		return "", err
	}
	col, err = normalizeSQLIdentifier(col)
	if err != nil {
		return "", err
	}
	type pragmaColumn struct {
		Name string `gorm:"column:name"`
		Type string `gorm:"column:type"`
	}
	var cols []pragmaColumn
	if err := db.Raw(fmt.Sprintf("PRAGMA table_info(%s);", table)).Scan(&cols).Error; err != nil {
		return "", err
	}
	for _, item := range cols {
		if strings.EqualFold(strings.TrimSpace(item.Name), col) {
			return strings.TrimSpace(item.Type), nil
		}
	}
	return "", nil
}

func migrateEnsureTicketLabelColumn(db *gorm.DB) error {
	has, err := tableHasColumn(db, "tickets", "label")
	if err != nil {
		return err
	}
	if !has {
		if err := db.Exec(`ALTER TABLE tickets ADD COLUMN label TEXT NOT NULL DEFAULT '';`).Error; err != nil {
			msg := strings.ToLower(strings.TrimSpace(err.Error()))
			if !strings.Contains(msg, "duplicate column name") {
				return err
			}
		}
	}
	return db.Exec(`
UPDATE tickets
SET label = ''
WHERE label IS NULL;
`).Error
}

func migrateEnsurePMStatePlannerColumns(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("db 为空")
	}
	return db.AutoMigrate(&PMState{})
}

func migrateAddPMOpsJournalCheckpointTables(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("db 为空")
	}
	return db.AutoMigrate(&PMOpJournalEntry{}, &PMCheckpoint{})
}

func migrateAddTicketIntegrationColumns(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("db 为空")
	}
	addColumnIfMissing := func(col, ddl string) error {
		has, err := tableHasColumn(db, "tickets", col)
		if err != nil {
			return err
		}
		if has {
			return nil
		}
		if err := db.Exec(ddl).Error; err != nil {
			msg := strings.ToLower(strings.TrimSpace(err.Error()))
			if strings.Contains(msg, "duplicate column name") {
				return nil
			}
			return err
		}
		return nil
	}
	if err := addColumnIfMissing("integration_status", `ALTER TABLE tickets ADD COLUMN integration_status TEXT NOT NULL DEFAULT '';`); err != nil {
		return err
	}
	if err := addColumnIfMissing("merge_anchor_sha", `ALTER TABLE tickets ADD COLUMN merge_anchor_sha TEXT NOT NULL DEFAULT '';`); err != nil {
		return err
	}
	if err := addColumnIfMissing("target_branch", `ALTER TABLE tickets ADD COLUMN target_branch TEXT NOT NULL DEFAULT '';`); err != nil {
		return err
	}
	if err := addColumnIfMissing("merged_at", `ALTER TABLE tickets ADD COLUMN merged_at DATETIME;`); err != nil {
		return err
	}
	if err := addColumnIfMissing("abandoned_reason", `ALTER TABLE tickets ADD COLUMN abandoned_reason TEXT NOT NULL DEFAULT '';`); err != nil {
		return err
	}
	if err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_tickets_integration_status ON tickets(integration_status);`).Error; err != nil {
		return err
	}
	return nil
}

func migrateAddTicketLifecycleEvents(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("db 为空")
	}
	if err := db.AutoMigrate(&TicketLifecycleEvent{}); err != nil {
		return err
	}
	if err := db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_ticket_lifecycle_ticket_sequence ON ticket_lifecycle_events(ticket_id, sequence);`).Error; err != nil {
		return err
	}
	if err := db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_ticket_lifecycle_idempotency_key ON ticket_lifecycle_events(idempotency_key);`).Error; err != nil {
		return err
	}
	if err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_ticket_lifecycle_event_type ON ticket_lifecycle_events(event_type);`).Error; err != nil {
		return err
	}
	if err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_ticket_lifecycle_worker_id ON ticket_lifecycle_events(worker_id);`).Error; err != nil {
		return err
	}
	if err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_ticket_lifecycle_task_run_id ON ticket_lifecycle_events(task_run_id);`).Error; err != nil {
		return err
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
