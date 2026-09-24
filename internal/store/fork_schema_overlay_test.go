package store

import "testing"

func TestForkSchemaMigrationRunsOutsideUpstreamMigration(t *testing.T) {
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
		`CREATE TABLE anthropic_snapshots (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			captured_at TEXT NOT NULL
		)`,
		`CREATE TABLE anthropic_reset_cycles (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			quota_name TEXT NOT NULL,
			cycle_start TEXT NOT NULL,
			cycle_end TEXT
		)`,
		`CREATE TABLE openrouter_snapshots (id INTEGER PRIMARY KEY AUTOINCREMENT)`,
	)

	if err := s.migrateSchema(); err != nil {
		t.Fatalf("migrateSchema: %v", err)
	}
	assertColumn := func(want bool, table, column string) {
		t.Helper()
		hasColumn, err := s.tableHasColumn(table, column)
		if err != nil {
			t.Fatalf("inspect %s.%s: %v", table, column, err)
		}
		if hasColumn != want {
			t.Fatalf("%s.%s presence = %t, want %t", table, column, hasColumn, want)
		}
	}

	assertColumn(false, "anthropic_snapshots", "account_id")
	assertColumn(false, "openrouter_snapshots", "account_balance")
	if err := s.migrateForkOverlaySchema(); err != nil {
		t.Fatalf("migrateForkOverlaySchema: %v", err)
	}
	assertColumn(true, "anthropic_snapshots", "account_id")
	assertColumn(true, "openrouter_snapshots", "account_balance")

	if err := s.migrateForkOverlaySchema(); err != nil {
		t.Fatalf("migrateForkOverlaySchema idempotent: %v", err)
	}
}
