package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// ProviderAccountAlias returns the non-secret presentation alias selected by
// the user, falling back to the durable credential-directory name.
func ProviderAccountAlias(account ProviderAccount) string {
	var metadata map[string]interface{}
	if json.Unmarshal([]byte(account.Metadata), &metadata) == nil {
		if alias, ok := metadata["alias"].(string); ok && strings.TrimSpace(alias) != "" {
			return strings.TrimSpace(alias)
		}
	}
	return account.Name
}

// UpdateProviderAccountAlias changes only display metadata. It never renames a
// credential directory, so discovery remains stable after an alias edit.
func (s *Store) UpdateProviderAccountAlias(provider string, accountID int64, alias string) error {
	alias = strings.TrimSpace(alias)
	if alias == "" || len([]rune(alias)) > 64 {
		return fmt.Errorf("account alias must be 1-64 characters")
	}
	account, err := s.ResolveProviderAccount(provider, accountID)
	if err != nil {
		return err
	}
	metadata := map[string]interface{}{}
	_ = json.Unmarshal([]byte(account.Metadata), &metadata)
	metadata["alias"] = alias
	encoded, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	return s.UpdateProviderAccountMetadata(accountID, string(encoded))
}

// EnsureDefaultProviderAccount returns the real global database ID for a
// provider's default account. Only Codex retains its historical ID=1 contract.
func (s *Store) EnsureDefaultProviderAccount(provider string) (*ProviderAccount, error) {
	return s.GetOrCreateProviderAccount(strings.TrimSpace(provider), "default")
}

// ResolveProviderAccount verifies that an ID belongs to provider. This prevents
// a valid account ID for one provider from selecting another provider's data.
func (s *Store) ResolveProviderAccount(provider string, accountID int64) (*ProviderAccount, error) {
	if accountID <= 0 {
		return nil, fmt.Errorf("invalid %s account ID", provider)
	}
	account, err := s.GetProviderAccountByID(accountID)
	if err != nil {
		return nil, err
	}
	if account == nil || account.Provider != provider {
		return nil, fmt.Errorf("account %d does not belong to provider %s", accountID, provider)
	}
	return account, nil
}

// ResolveDefaultProviderAccount returns the active default account for provider.
func (s *Store) ResolveDefaultProviderAccount(provider string) (*ProviderAccount, error) {
	var account ProviderAccount
	var created string
	err := s.db.QueryRow(`SELECT id, provider, name, created_at, COALESCE(metadata, ''), deleted_at, COALESCE(external_id, '') FROM provider_accounts WHERE provider = ? AND name = 'default' AND deleted_at IS NULL`, provider).Scan(&account.ID, &account.Provider, &account.Name, &created, &account.Metadata, new(sql.NullString), &account.ExternalID)
	if err == sql.ErrNoRows {
		return s.EnsureDefaultProviderAccount(provider)
	}
	if err != nil {
		return nil, fmt.Errorf("resolve default %s account: %w", provider, err)
	}
	account.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	return &account, nil
}

func (s *Store) defaultProviderAccountID(provider string) (int64, error) {
	account, err := s.ResolveDefaultProviderAccount(provider)
	if err != nil {
		return 0, err
	}
	return account.ID, nil
}

func (s *Store) scopedProviderAccountID(provider string, requested []int64) (int64, error) {
	if len(requested) == 0 || requested[0] == 0 {
		return s.defaultProviderAccountID(provider)
	}
	account, err := s.ResolveProviderAccount(provider, requested[0])
	if err != nil {
		return 0, err
	}
	return account.ID, nil
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
