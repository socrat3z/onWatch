package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/onllm-dev/onwatch/v2/internal/account"
	"github.com/onllm-dev/onwatch/v2/internal/api"
)

// codexNativeDiscovery keeps optional native-home state outside upstream's
// CodexProfile. Native discovery remains only for the container credential
// workflow; saved profiles are the preferred long-term account mechanism.
type codexNativeDiscovery struct {
	authRoot         string
	warnedCollisions map[string]bool
}

// SetAuthRoot enables discovery of native Codex homes laid out as
// <root>/<alias>/auth.json. Refresh-token write-back stays in that home.
func (m *CodexAgentManager) SetAuthRoot(root string) {
	m.authRoot = strings.TrimSpace(root)
}

func (m *CodexAgentManager) profileIsNative(profile CodexProfile) bool {
	return m.authRoot != "" && profile.SourcePath == filepath.Join(m.authRoot, profile.Name, "auth.json")
}

func (m *CodexAgentManager) aliasOwnedByProfile(name string) bool {
	m.mu.RLock()
	running := m.instances[name]
	m.mu.RUnlock()
	return running != nil && !m.profileIsNative(running.Profile)
}

func (m *CodexAgentManager) loadAndStartNativeAccounts() error {
	if m.authRoot == "" {
		return nil
	}
	names, err := account.ListDirectories(m.authRoot)
	if err != nil {
		return fmt.Errorf("read Codex auth root: %w", err)
	}
	for _, name := range names {
		if err := m.loadAndStartNativeAccount(name); err != nil {
			m.logger.Warn("failed to load native Codex account", "account", name, "error", err)
		}
	}
	return nil
}

func (m *CodexAgentManager) loadAndStartNativeAccount(name string) error {
	if err := account.ValidateName(name); err != nil {
		return err
	}
	if m.aliasOwnedByProfile(name) {
		m.warnOnceAboutCollision(name)
		return nil
	}
	authPath := filepath.Join(m.authRoot, name, "auth.json")
	creds := api.ReadCodexCredentialsFile(authPath)
	if creds == nil {
		return fmt.Errorf("no usable auth.json")
	}
	externalID := creds.CompositeExternalID()
	if externalID == "" {
		externalID = strings.TrimSpace(creds.AccountID)
	}
	if err := m.validateNativeAliasIdentity(name, externalID); err != nil {
		// Remember the file so the 30s rescan retries only after a new login
		// rewrites it, instead of logging the same refusal forever.
		if info, statErr := os.Stat(authPath); statErr == nil {
			m.mu.Lock()
			m.lastScanProfiles["native:"+name] = info.ModTime()
			m.mu.Unlock()
		}
		return err
	}
	profile := CodexProfile{Name: name, AccountID: creds.AccountID, UserID: creds.UserID, APIKey: creds.APIKey, SourcePath: authPath}
	profile.Tokens.AccessToken, profile.Tokens.RefreshToken, profile.Tokens.IDToken = creds.AccessToken, creds.RefreshToken, creds.IDToken
	if err := m.loadAndStartProfileValue(profile); err != nil {
		return err
	}
	if info, err := os.Stat(authPath); err == nil {
		m.mu.Lock()
		m.lastScanProfiles["native:"+name] = info.ModTime()
		m.mu.Unlock()
	}
	return nil
}

// validateNativeAliasIdentity prevents a different login from inheriting the
// deleted profile's alias and history through the store's legacy name fallback.
// A row without an identity is claimable: upstream seeds Codex "default" (ID 1)
// with no external_id, and /auth/codex/default is the container's default login.
func (m *CodexAgentManager) validateNativeAliasIdentity(name, externalID string) error {
	if externalID == "" || m.store == nil {
		return nil
	}
	accounts, err := m.store.QueryProviderAccounts("codex")
	if err != nil {
		return fmt.Errorf("check Codex alias identity: %w", err)
	}
	for _, existing := range accounts {
		if existing.Name != name {
			continue
		}
		if existing.ExternalID != "" && existing.ExternalID != externalID {
			return fmt.Errorf("native Codex account identity does not match existing alias history")
		}
	}
	return nil
}

func (m *CodexAgentManager) loadAndStartProfileValue(profile CodexProfile) error {
	m.mu.RLock()
	_, exists := m.instances[profile.Name]
	m.mu.RUnlock()
	if exists {
		if m.profileIsNative(profile) && m.aliasOwnedByProfile(profile.Name) {
			m.warnOnceAboutCollision(profile.Name)
		}
		return nil
	}
	return m.startAgentForProfile(profile)
}

// warnOnceAboutCollision keeps the collision warning out of every 30s rescan.
func (m *CodexAgentManager) warnOnceAboutCollision(name string) {
	m.mu.Lock()
	if m.warnedCollisions == nil {
		m.warnedCollisions = make(map[string]bool)
	}
	already := m.warnedCollisions[name]
	m.warnedCollisions[name] = true
	m.mu.Unlock()
	if already {
		return
	}
	m.logger.Warn("Codex native account is not polling: a legacy profile of the same name is already running",
		"account", name,
		"winner", "legacy profile",
		"ignored", "native auth root account",
		"fix", "rename the legacy profile or the native account home so the two aliases differ")
}

func (m *CodexAgentManager) scanForNativeAccountChanges() {
	if m.authRoot == "" || accountRootUnavailable(m.authRoot) {
		return
	}
	names, err := account.ListDirectories(m.authRoot)
	if err != nil {
		return
	}
	seen := make(map[string]bool, len(names))
	for _, name := range names {
		seen[name] = true
		path := filepath.Join(m.authRoot, name, "auth.json")
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		key := "native:" + name
		m.mu.RLock()
		last, known := m.lastScanProfiles[key]
		m.mu.RUnlock()
		if !known || info.ModTime().After(last) {
			if m.aliasOwnedByProfile(name) {
				m.warnOnceAboutCollision(name)
				m.mu.Lock()
				delete(m.lastScanProfiles, key)
				m.mu.Unlock()
				continue
			}
			if known {
				m.stopAgent(name)
			}
			if err := m.loadAndStartNativeAccount(name); err != nil {
				m.logger.Warn("failed to reload native Codex account", "account", name, "error", err)
			}
		}
	}
	m.mu.RLock()
	var missing []string
	for key := range m.lastScanProfiles {
		if strings.HasPrefix(key, "native:") && !seen[strings.TrimPrefix(key, "native:")] {
			missing = append(missing, strings.TrimPrefix(key, "native:"))
		}
	}
	m.mu.RUnlock()
	for _, name := range missing {
		m.mu.RLock()
		running := m.instances[name]
		m.mu.RUnlock()
		m.mu.Lock()
		delete(m.lastScanProfiles, "native:"+name)
		m.mu.Unlock()
		if running == nil || !m.profileIsNative(running.Profile) {
			continue
		}
		m.stopAgent(name)
		if err := m.store.MarkProviderAccountDeleted("codex", name); err != nil {
			m.logger.Warn("failed to mark removed native Codex account", "account", name, "error", err)
		}
	}
}
