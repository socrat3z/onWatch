package store

import (
	"database/sql"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func newRawStoreForMigrationTests(t *testing.T, statements ...string) *Store {
	t.Helper()

	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	for _, stmt := range statements {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("exec setup statement %q: %v", stmt, err)
		}
	}

	return &Store{db: db}
}

func TestStore_MigrateSchema_ErrorsWithoutBaseTables(t *testing.T) {
	t.Parallel()
	s := newRawStoreForMigrationTests(t)

	err := s.migrateSchema()
	if err == nil || !strings.Contains(err.Error(), "failed to add provider to quota_snapshots") {
		t.Fatalf("migrateSchema() error = %v", err)
	}
}

func TestStore_MigrateNotificationLogProviderScope_AlreadyMigrated(t *testing.T) {
	t.Parallel()
	s := newRawStoreForMigrationTests(t, `
		CREATE TABLE notification_log (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			provider TEXT NOT NULL DEFAULT 'legacy',
			quota_key TEXT NOT NULL,
			notification_type TEXT NOT NULL,
			sent_at TEXT NOT NULL,
			utilization REAL,
			UNIQUE(provider, quota_key, notification_type)
		)
	`)

	if err := s.migrateNotificationLogProviderScope(); err != nil {
		t.Fatalf("migrateNotificationLogProviderScope() error = %v", err)
	}

	hasProvider, err := s.tableHasColumn("notification_log", "provider")
	if err != nil {
		t.Fatalf("tableHasColumn(notification_log, provider): %v", err)
	}
	if !hasProvider {
		t.Fatal("expected provider column to exist")
	}
}

func TestStore_MigrateNotificationLogProviderScope_ErrorPaths(t *testing.T) {
	t.Parallel()
	t.Run("create v2 table failure", func(t *testing.T) {
		s := newRawStoreForMigrationTests(t,
			`CREATE TABLE notification_log (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				quota_key TEXT NOT NULL,
				notification_type TEXT NOT NULL,
				sent_at TEXT NOT NULL,
				utilization REAL
			)`,
			`CREATE TABLE notification_log_v2 (id INTEGER PRIMARY KEY)`,
		)

		err := s.migrateNotificationLogProviderScope()
		if err == nil || !strings.Contains(err.Error(), "failed to create notification_log_v2") {
			t.Fatalf("migrateNotificationLogProviderScope(create fail) = %v", err)
		}
	})

	t.Run("copy rows failure", func(t *testing.T) {
		s := newRawStoreForMigrationTests(t, `
			CREATE TABLE notification_log (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				sent_at TEXT NOT NULL
			)
		`)

		err := s.migrateNotificationLogProviderScope()
		if err == nil || !strings.Contains(err.Error(), "failed to copy notification_log rows") {
			t.Fatalf("migrateNotificationLogProviderScope(copy fail) = %v", err)
		}
	})
}

func TestStore_CreateTables_ErrorOnClosedDB(t *testing.T) {
	t.Parallel()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("db.Close: %v", err)
	}

	s := &Store{db: db}
	err = s.createTables()
	if err == nil || !strings.Contains(err.Error(), "failed to create schema") {
		t.Fatalf("createTables() error = %v", err)
	}
}

func TestStore_TableHasColumn_ErrorOnClosedDB(t *testing.T) {
	t.Parallel()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("db.Close: %v", err)
	}

	s := &Store{db: db}
	_, err = s.tableHasColumn("notification_log", "provider")
	if err == nil || !strings.Contains(err.Error(), "failed to inspect table") {
		t.Fatalf("tableHasColumn() error = %v", err)
	}
}

func TestStore_MigrateSchema_IdempotentOnCurrentSchema(t *testing.T) {
	t.Parallel()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	// Running migrations on an already-current schema should be a no-op.
	if err := s.migrateSchema(); err != nil {
		t.Fatalf("migrateSchema(idempotent): %v", err)
	}

	accounts, err := s.QueryProviderAccounts("codex")
	if err != nil {
		t.Fatalf("QueryProviderAccounts(codex): %v", err)
	}
	if len(accounts) == 0 {
		t.Fatal("expected default codex provider account after migration")
	}
}

// A pre-status opencode_quota_values table must gain the column additively,
// with existing rows defaulting to an empty (status-unknown) value.
func TestStore_MigrateSchema_AddsOpenCodeStatusColumn(t *testing.T) {
	t.Parallel()
	s := newRawStoreForMigrationTests(t,
		`CREATE TABLE quota_snapshots (id INTEGER PRIMARY KEY, provider TEXT NOT NULL DEFAULT 'synthetic')`,
		`CREATE TABLE reset_cycles (id INTEGER PRIMARY KEY, provider TEXT NOT NULL DEFAULT 'synthetic')`,
		`CREATE TABLE sessions (
			id TEXT PRIMARY KEY,
			provider TEXT NOT NULL DEFAULT 'synthetic',
			start_sub_requests REAL NOT NULL DEFAULT 0,
			start_search_requests REAL NOT NULL DEFAULT 0,
			start_tool_requests REAL NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE opencode_quota_values (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			snapshot_id INTEGER NOT NULL,
			quota_name TEXT NOT NULL,
			used REAL NOT NULL DEFAULT 0,
			limit_value REAL NOT NULL DEFAULT 0,
			utilization REAL NOT NULL DEFAULT 0,
			format TEXT NOT NULL DEFAULT 'percent',
			resets_at TEXT
		)`,
		`INSERT INTO opencode_quota_values (snapshot_id, quota_name, used, limit_value, utilization) VALUES (1, 'five_hour', 10, 100, 10)`,
		`CREATE TABLE notification_log (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			provider TEXT NOT NULL DEFAULT 'legacy',
			quota_key TEXT NOT NULL,
			notification_type TEXT NOT NULL,
			sent_at TEXT NOT NULL,
			utilization REAL,
			UNIQUE(provider, quota_key, notification_type)
		)`,
	)

	if err := s.migrateSchema(); err != nil {
		t.Fatalf("migrateSchema: %v", err)
	}
	// The migration is idempotent: a second run must not error.
	if err := s.migrateSchema(); err != nil {
		t.Fatalf("migrateSchema(idempotent): %v", err)
	}

	var status string
	if err := s.db.QueryRow(`SELECT status FROM opencode_quota_values WHERE quota_name = 'five_hour'`).Scan(&status); err != nil {
		t.Fatalf("select status: %v", err)
	}
	if status != "" {
		t.Fatalf("legacy row status = %q, want empty", status)
	}
}

func TestStore_MigrateSchema_ToleratesMissingOptionalTables(t *testing.T) {
	t.Parallel()
	s := newRawStoreForMigrationTests(t,
		`CREATE TABLE quota_snapshots (id INTEGER PRIMARY KEY, provider TEXT NOT NULL DEFAULT 'synthetic')`,
		`CREATE TABLE reset_cycles (id INTEGER PRIMARY KEY, provider TEXT NOT NULL DEFAULT 'synthetic')`,
		`CREATE TABLE sessions (
			id TEXT PRIMARY KEY,
			provider TEXT NOT NULL DEFAULT 'synthetic',
			start_sub_requests REAL NOT NULL DEFAULT 0,
			start_search_requests REAL NOT NULL DEFAULT 0,
			start_tool_requests REAL NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE notification_log (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			provider TEXT NOT NULL DEFAULT 'legacy',
			quota_key TEXT NOT NULL,
			notification_type TEXT NOT NULL,
			sent_at TEXT NOT NULL,
			utilization REAL,
			UNIQUE(provider, quota_key, notification_type)
		)`,
	)

	// No zai/antigravity/codex tables exist in this schema: migration should
	// still complete by ignoring those optional-table errors.
	if err := s.migrateSchema(); err != nil {
		t.Fatalf("migrateSchema(optional missing tables): %v", err)
	}

	accounts, err := s.QueryProviderAccounts("codex")
	if err != nil {
		t.Fatalf("QueryProviderAccounts(codex): %v", err)
	}
	if len(accounts) == 0 || accounts[0].Name != "default" {
		t.Fatalf("expected default codex provider account, got %+v", accounts)
	}
}
