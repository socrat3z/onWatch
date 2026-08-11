package agent

import (
	"context"
	"log/slog"
	"os"
	"strconv"

	"github.com/onllm-dev/onwatch/v2/internal/api"
	"github.com/onllm-dev/onwatch/v2/internal/notify"

	"github.com/onllm-dev/onwatch/v2/internal/store"
)

// accountSession is a manager's handle on one running account agent. The map
// stores a pointer so a late-exiting agent can prove it still owns its entry
// before removing it: an unconditional delete would strand the replacement
// agent with no cancel func and let the next Reload start a duplicate.
type accountSession struct{ cancel context.CancelFunc }

// accountRootUnavailable reports a root that cannot be read at all. An
// unmounted volume looks exactly like "the user deleted every account", so
// reconciliation has to stand down rather than retire the whole install.
func accountRootUnavailable(root string) bool {
	info, err := os.Stat(root)
	return err != nil || !info.IsDir()
}

// reconcileAccountRemovals retires every account whose credential directory has
// disappeared, not just the ones that are currently polling. Iterating only the
// running map leaves an account that never started - registered, then rejected
// for missing credentials - permanently visible with no way to remove it.
//
// The legacy "default" row is never directory-backed (it holds pre-discovery
// history, see docs/WITH_USER_ENV.md), so it is deliberately exempt.
//
// Callers must hold the manager mutex: running is mutated in place.
func reconcileAccountRemovals(s *store.Store, provider string, present map[string]bool, running map[string]*accountSession, logger *slog.Logger) {
	for alias, session := range running {
		if !present[alias] {
			session.cancel()
			delete(running, alias)
		}
	}
	accounts, err := s.QueryActiveProviderAccounts(provider)
	if err != nil {
		logger.Warn("account reconcile scan failed", "provider", provider, "error", err)
		return
	}
	for _, acc := range accounts {
		if present[acc.Name] || acc.Name == store.DefaultProviderAccountName {
			continue
		}
		if err := s.MarkProviderAccountDeletedByID(acc.ID); err != nil {
			logger.Warn("soft delete removed account", "provider", provider, "account", acc.Name, "error", err)
		}
	}
}

// The accessors below exist because SetAuthRoot / SetAccountPollingCheck /
// SetNotifier are called from daemon setup while Reload may already be running
// on the manager's own ticker. Every field they touch is therefore read under
// the manager mutex, and the polling check is invoked outside it so a check
// that calls back into the store cannot deadlock the supervisor.

func (m *AnthropicAgentManager) authRoot() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.root
}

func (m *AnthropicAgentManager) shouldPoll(accountID int64) bool {
	m.mu.Lock()
	check := m.pollingCheck
	m.mu.Unlock()
	return check == nil || check(accountID)
}

func (m *AnthropicAgentManager) currentNotifier() *notify.NotificationEngine {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.notifier
}

func (m *AntigravityAgentManager) authRoot() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.root
}

func (m *AntigravityAgentManager) shouldPoll(accountID int64) bool {
	m.mu.Lock()
	check := m.pollingCheck
	m.mu.Unlock()
	return check == nil || check(accountID)
}

func (m *AntigravityAgentManager) currentNotifier() *notify.NotificationEngine {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.notifier
}

func (m *AnthropicAgentManager) accountClientOptions() []api.AnthropicOption {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.clientOpts
}

// notifyAccountID renders an account identity for the notification engine.
// The ambient single-account agents keep their historical empty identity: a
// numeric "0" would change every dedup key on upgrade and replay alerts the
// user has already seen.
func notifyAccountID(accountID int64) string {
	if accountID == 0 {
		return ""
	}
	return strconv.FormatInt(accountID, 10)
}
