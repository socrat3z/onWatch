package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// DefaultProviderAccountName is the reserved account that holds history from
// before account discovery existed. It is never backed by a credential
// directory, so directory-driven reconciliation must leave it alone.
const DefaultProviderAccountName = "default"

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
	return s.GetOrCreateProviderAccount(strings.TrimSpace(provider), DefaultProviderAccountName)
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

const providerAccountColumns = "id, provider, name, created_at, COALESCE(metadata, ''), COALESCE(external_id, '')"

func (s *Store) scanProviderAccount(query string, args ...interface{}) (*ProviderAccount, error) {
	var account ProviderAccount
	var created string
	if err := s.db.QueryRow(query, args...).Scan(&account.ID, &account.Provider, &account.Name, &created, &account.Metadata, &account.ExternalID); err != nil {
		return nil, err
	}
	account.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	return &account, nil
}

// ResolveDefaultProviderAccount returns the account that account-unaware views
// read. An install whose accounts all come from directory discovery has no
// "default" row at all, so falling back to the oldest live account keeps those
// views on real data instead of minting an empty placeholder that discovery
// would never populate. Oldest rather than newest: the choice must not move
// when the user adds another alias.
func (s *Store) ResolveDefaultProviderAccount(provider string) (*ProviderAccount, error) {
	account, err := s.scanProviderAccount(
		"SELECT "+providerAccountColumns+" FROM provider_accounts WHERE provider = ? AND name = ? AND deleted_at IS NULL",
		provider, DefaultProviderAccountName)
	if err == nil {
		return account, nil
	}
	if err != sql.ErrNoRows {
		return nil, fmt.Errorf("resolve default %s account: %w", provider, err)
	}
	account, err = s.scanProviderAccount(
		"SELECT "+providerAccountColumns+" FROM provider_accounts WHERE provider = ? AND deleted_at IS NULL ORDER BY id ASC LIMIT 1",
		provider)
	if err == nil {
		return account, nil
	}
	if err != sql.ErrNoRows {
		return nil, fmt.Errorf("resolve default %s account: %w", provider, err)
	}
	return s.EnsureDefaultProviderAccount(provider)
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

// LookupDefaultProviderAccountID returns a provider's default account ID, or 0
// when it has none. Unlike ResolveDefaultProviderAccount it never creates a row,
// so hot read paths (notification keying) cannot write to the database.
func (s *Store) LookupDefaultProviderAccountID(provider string) (int64, error) {
	var id int64
	err := s.db.QueryRow(
		`SELECT id FROM provider_accounts WHERE provider = ? AND name = ? AND deleted_at IS NULL`,
		strings.TrimSpace(provider), DefaultProviderAccountName,
	).Scan(&id)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("look up default %s account: %w", provider, err)
	}
	return id, nil
}

// Credential health states persisted in provider_accounts.metadata. They come
// from account discovery, which never reads a secret - only whether the file a
// provider needs is there and parseable.
const (
	AccountCredentialsOK         = "ok"         // credentials found and readable
	AccountCredentialsMissing    = "missing"    // the expected file is not there
	AccountCredentialsUnreadable = "unreadable" // present but malformed or empty
	AccountCredentialsUnverified = "unverified" // discovery cannot tell (Antigravity keyring)
)

// SetProviderAccountCredentialHealth merges credential state into an account's
// metadata, leaving the user's alias untouched. It skips the write when nothing
// changed, so the once-a-minute reconcile does not churn the database.
func (s *Store) SetProviderAccountCredentialHealth(accountID int64, state, credentialPath string) error {
	account, err := s.GetProviderAccountByID(accountID)
	if err != nil {
		return err
	}
	if account == nil {
		return fmt.Errorf("account %d not found", accountID)
	}
	metadata := map[string]interface{}{}
	_ = json.Unmarshal([]byte(account.Metadata), &metadata)
	if metadata["credentials"] == state && metadata["credentials_path"] == credentialPath {
		return nil
	}
	metadata["credentials"] = state
	if credentialPath == "" {
		delete(metadata, "credentials_path")
	} else {
		metadata["credentials_path"] = credentialPath
	}
	encoded, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	return s.UpdateProviderAccountMetadata(accountID, string(encoded))
}

// ProviderAccountCredentialHealth reports the credential state recorded for an
// account, and the path the daemon expects that credential at. An account that
// predates health recording reports "unverified".
func ProviderAccountCredentialHealth(account ProviderAccount) (state, credentialPath string) {
	var metadata map[string]interface{}
	if json.Unmarshal([]byte(account.Metadata), &metadata) != nil {
		return AccountCredentialsUnverified, ""
	}
	state, _ = metadata["credentials"].(string)
	credentialPath, _ = metadata["credentials_path"].(string)
	if strings.TrimSpace(state) == "" {
		state = AccountCredentialsUnverified
	}
	return state, credentialPath
}
