package agent

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"sync"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/account"
	"github.com/onllm-dev/onwatch/v2/internal/api"
	"github.com/onllm-dev/onwatch/v2/internal/notify"
	"github.com/onllm-dev/onwatch/v2/internal/store"
	"github.com/onllm-dev/onwatch/v2/internal/tracker"
)

// AnthropicAgentManager discovers named Claude homes and gives every valid
// account an isolated client, tracker and rotating credential writer.
type AnthropicAgentManager struct {
	store        *store.Store
	interval     time.Duration
	logger       *slog.Logger
	root         string
	pollingCheck func(int64) bool
	notifier     *notify.NotificationEngine
	mu           sync.Mutex
	running      map[string]*accountSession
	ctx          context.Context
	// clientOpts is a test seam: production leaves it nil so every account
	// client keeps its real endpoint, while tests can point one at httptest.
	clientOpts []api.AnthropicOption
}

func NewAnthropicAgentManager(s *store.Store, interval time.Duration, logger *slog.Logger) *AnthropicAgentManager {
	if logger == nil {
		logger = slog.Default()
	}
	return &AnthropicAgentManager{store: s, interval: interval, logger: logger, running: make(map[string]*accountSession)}
}

func (m *AnthropicAgentManager) SetAuthRoot(root string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.root = root
}
func (m *AnthropicAgentManager) SetAccountPollingCheck(check func(int64) bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pollingCheck = check
}
func (m *AnthropicAgentManager) SetNotifier(n *notify.NotificationEngine) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.notifier = n
}

func (m *AnthropicAgentManager) Run(ctx context.Context) error {
	m.mu.Lock()
	m.ctx = ctx
	m.mu.Unlock()
	m.Reload()
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			m.stopAll()
			return nil
		case <-ticker.C:
			m.Reload()
		}
	}
}

// Reload reconciles aliases from disk. Deleting a home stops polling and soft
// deletes the account row, but all existing history remains available.
func (m *AnthropicAgentManager) Reload() {
	root := m.authRoot()
	if root == "" || accountRootUnavailable(root) {
		return
	}
	defs, err := (account.AnthropicSource{Root: root}).List(context.Background())
	if err != nil {
		m.logger.Warn("Anthropic account scan failed", "error", err)
		return
	}
	present := make(map[string]bool, len(defs))
	for _, def := range defs {
		present[def.Name] = true
		acc, err := m.store.CreateOrRestoreProviderAccount("anthropic", def.Name)
		if err != nil {
			m.logger.Error("register Anthropic account", "account", def.Name, "error", err)
			continue
		}
		// Credential state is recorded on every pass, not only at first sight:
		// the dashboard reads it to explain an account that has no data yet,
		// and a re-login has to clear that explanation without a restart.
		path := filepath.Join(def.AuthRoot, ".claude", ".credentials.json")
		state := store.AccountCredentialsOK
		var creds *api.AnthropicCredentials
		if def.Metadata["credentials"] != "present" {
			state = store.AccountCredentialsMissing
		} else if creds, err = api.ReadAnthropicCredentialsFile(path); err != nil || creds == nil || creds.AccessToken == "" {
			state = store.AccountCredentialsUnreadable
		}
		if err := m.store.SetProviderAccountCredentialHealth(acc.ID, state, path); err != nil {
			m.logger.Warn("record Anthropic account health", "account", def.Name, "error", err)
		}
		if state != store.AccountCredentialsOK {
			m.logger.Warn("Anthropic account is not pollable", "account", def.Name, "account_id", acc.ID, "credentials", state, "path", path)
			continue
		}
		m.mu.Lock()
		_, exists := m.running[def.Name]
		ctx := m.ctx
		m.mu.Unlock()
		if exists || ctx == nil {
			continue
		}
		child, cancel := context.WithCancel(ctx)
		tr := tracker.NewAnthropicTrackerForAccount(m.store, m.logger, acc.ID)
		ag := NewAnthropicAgent(api.NewAnthropicClient(creds.AccessToken, m.logger, m.accountClientOptions()...), m.store, tr, m.interval, m.logger, NewSessionManager(m.store, fmt.Sprintf("anthropic:%d", acc.ID), 15*time.Minute, m.logger))
		ag.SetAccountContext(acc.ID, def.Name)
		ag.SetNotifier(m.currentNotifier())
		ag.SetPollingCheck(func() bool { return m.shouldPoll(acc.ID) })
		ag.SetTokenRefresh(func() string {
			c, _ := api.ReadAnthropicCredentialsFile(path)
			if c == nil {
				return ""
			}
			return c.AccessToken
		})
		ag.SetCredentialsRefresh(func() *api.AnthropicCredentials { c, _ := api.ReadAnthropicCredentialsFile(path); return c })
		ag.SetCredentialsWriter(func(access, refresh string, expires int) error {
			return api.WriteAnthropicCredentialsFile(path, access, refresh, expires)
		})
		m.mu.Lock()
		if _, raced := m.running[def.Name]; raced {
			m.mu.Unlock()
			cancel()
			continue
		}
		session := &accountSession{cancel: cancel}
		m.running[def.Name] = session
		m.mu.Unlock()
		go func(alias string, owned *accountSession) {
			_ = ag.Run(child)
			m.mu.Lock()
			if m.running[alias] == owned {
				delete(m.running, alias)
			}
			m.mu.Unlock()
		}(def.Name, session)
	}
	m.mu.Lock()
	reconcileAccountRemovals(m.store, "anthropic", present, m.running, m.logger)
	m.mu.Unlock()
}

func (m *AnthropicAgentManager) stopAll() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, session := range m.running {
		session.cancel()
	}
	m.running = make(map[string]*accountSession)
}
