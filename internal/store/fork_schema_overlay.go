package store

import (
	"fmt"
	"strings"
)

// migrateForkOverlaySchema executes schema extensions introduced by the fork
// after the upstream migration sequence has completed. It must remain
// idempotent so it supports both fresh databases and upgrades.
func (s *Store) migrateForkOverlaySchema() error {
	// Anthropic and Antigravity now share the same account-scoped persistence
	// contract as Codex and MiniMax. Legacy rows are assigned to their provider's
	// real default account ID atomically.
	for _, table := range []string{"anthropic_snapshots", "anthropic_reset_cycles", "antigravity_snapshots", "antigravity_reset_cycles"} {
		if _, err := s.db.Exec(`ALTER TABLE ` + table + ` ADD COLUMN account_id INTEGER NOT NULL DEFAULT 0`); err != nil &&
			!strings.Contains(err.Error(), "duplicate column name") && !strings.Contains(err.Error(), "no such table") {
			return fmt.Errorf("failed to add account_id to %s: %w", table, err)
		}
	}
	if err := s.backfillProviderAccount("anthropic", []string{"anthropic_snapshots", "anthropic_reset_cycles"}); err != nil {
		return err
	}
	if err := s.backfillProviderAccount("antigravity", []string{"antigravity_snapshots", "antigravity_reset_cycles"}); err != nil {
		return err
	}

	// The original Antigravity unique index did not include account_id, which
	// would reject identical model names from two independent accounts.
	if _, err := s.db.Exec(`DROP INDEX IF EXISTS idx_antigravity_cycles_model_active_unique`); err != nil {
		return fmt.Errorf("failed to replace antigravity active-cycle index: %w", err)
	}
	for _, stmt := range []string{
		`CREATE INDEX IF NOT EXISTS idx_anthropic_snapshots_account ON anthropic_snapshots(account_id, captured_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_anthropic_cycles_account ON anthropic_reset_cycles(account_id, quota_name, cycle_start DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_antigravity_snapshots_account ON antigravity_snapshots(account_id, captured_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_antigravity_cycles_account ON antigravity_reset_cycles(account_id, model_id, cycle_start DESC)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_antigravity_cycles_model_active_unique ON antigravity_reset_cycles(account_id, model_id) WHERE cycle_end IS NULL`,
	} {
		if _, err := s.db.Exec(stmt); err != nil && !strings.Contains(err.Error(), "no such table") {
			return fmt.Errorf("failed to create multi-account index: %w", err)
		}
	}

	// Store account-wide OpenRouter credits separately from per-key limits.
	for _, col := range []string{"account_credits REAL", "account_usage REAL", "account_balance REAL"} {
		if _, err := s.db.Exec(`ALTER TABLE openrouter_snapshots ADD COLUMN ` + col); err != nil {
			if !strings.Contains(err.Error(), "duplicate column name") &&
				!strings.Contains(err.Error(), "no such table") {
				return fmt.Errorf("failed to add OpenRouter account credits column: %w", err)
			}
		}
	}
	return nil
}

// backfillProviderAccount migrates only placeholder rows, so it is idempotent
// and never changes historical account assignments made by a newer daemon.
func (s *Store) backfillProviderAccount(provider string, tables []string) error {
	accountID, err := s.defaultProviderAccountID(provider)
	if err != nil {
		return fmt.Errorf("ensure default %s account: %w", provider, err)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin %s account migration: %w", provider, err)
	}
	defer tx.Rollback()
	for _, table := range tables {
		if _, err := tx.Exec(`UPDATE `+table+` SET account_id = ? WHERE account_id = 0`, accountID); err != nil && !strings.Contains(err.Error(), "no such table") {
			return fmt.Errorf("backfill %s account IDs: %w", table, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit %s account migration: %w", provider, err)
	}
	return nil
}
