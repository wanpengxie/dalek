package store

import (
	"errors"
	"path/filepath"
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

func TestOpenAndMigrate_PMStatePlannerColumnsPresent(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "dalek.sqlite3")
	db, err := OpenAndMigrate(dbPath)
	if err != nil {
		t.Fatalf("OpenAndMigrate failed: %v", err)
	}

	wantCols := []string{
		"planner_dirty",
		"planner_wake_version",
		"planner_active_task_run_id",
		"planner_cooldown_until",
		"planner_last_error",
		"planner_last_run_at",
	}
	for _, col := range wantCols {
		ok, err := tableHasColumn(db, "pm_states", col)
		if err != nil {
			t.Fatalf("tableHasColumn(pm_states.%s) failed: %v", col, err)
		}
		if !ok {
			t.Fatalf("pm_states should contain planner column: %s", col)
		}
	}

	for _, col := range []string{"planner_wake_version", "planner_last_error"} {
		if err := dropTableColumn(db, "pm_states", col); err != nil {
			t.Fatalf("drop pm_states.%s failed: %v", col, err)
		}
	}
	if err := db.Exec("DELETE FROM schema_migrations WHERE version >= 14;").Error; err != nil {
		t.Fatalf("rollback schema_migrations for v14 failed: %v", err)
	}
	if _, err := OpenAndMigrate(dbPath); err != nil {
		t.Fatalf("OpenAndMigrate (reapply v14) failed: %v", err)
	}

	db2, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	for _, col := range wantCols {
		ok, err := tableHasColumn(db2, "pm_states", col)
		if err != nil {
			t.Fatalf("tableHasColumn(pm_states.%s) after reapply failed: %v", col, err)
		}
		if !ok {
			t.Fatalf("pm_states should restore planner column after reapply: %s", col)
		}
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
