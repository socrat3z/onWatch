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
	running      map[string]context.CancelFunc
	ctx          context.Context
}

func NewAnthropicAgentManager(s *store.Store, interval time.Duration, logger *slog.Logger) *AnthropicAgentManager {
	if logger == nil {
		logger = slog.Default()
	}
	return &AnthropicAgentManager{store: s, interval: interval, logger: logger, running: make(map[string]context.CancelFunc)}
}

func (m *AnthropicAgentManager) SetAuthRoot(root string) { m.root = root }
func (m *AnthropicAgentManager) SetAccountPollingCheck(check func(int64) bool) {
	m.pollingCheck = check
}
func (m *AnthropicAgentManager) SetNotifier(n *notify.NotificationEngine) { m.notifier = n }

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
	if m.root == "" {
		return
	}
	defs, err := (account.AnthropicSource{Root: m.root}).List(context.Background())
	if err != nil {
		m.logger.Warn("Anthropic account scan failed", "error", err)
		return
	}
	present := make(map[string]bool, len(defs))
	for _, def := range defs {
		present[def.Name] = true
		m.mu.Lock()
		_, exists := m.running[def.Name]
		ctx := m.ctx
		m.mu.Unlock()
		if exists || ctx == nil {
			continue
		}
		acc, err := m.store.CreateOrRestoreProviderAccount("anthropic", def.Name)
		if err != nil {
			m.logger.Error("register Anthropic account", "account", def.Name, "error", err)
			continue
		}
		if def.Metadata["credentials"] != "present" {
			m.logger.Warn("Anthropic account credentials are missing", "account", def.Name, "account_id", acc.ID)
			continue
		}
		path := filepath.Join(def.AuthRoot, ".claude", ".credentials.json")
		creds, err := api.ReadAnthropicCredentialsFile(path)
		if err != nil || creds == nil || creds.AccessToken == "" {
			m.logger.Warn("Anthropic account credentials are unreadable", "account", def.Name)
			continue
		}
		child, cancel := context.WithCancel(ctx)
		tr := tracker.NewAnthropicTrackerForAccount(m.store, m.logger, acc.ID)
		ag := NewAnthropicAgent(api.NewAnthropicClient(creds.AccessToken, m.logger), m.store, tr, m.interval, m.logger, NewSessionManager(m.store, fmt.Sprintf("anthropic:%d", acc.ID), 15*time.Minute, m.logger))
		ag.SetAccountContext(acc.ID, def.Name)
		ag.SetNotifier(m.notifier)
		ag.SetPollingCheck(func() bool { return m.pollingCheck == nil || m.pollingCheck(acc.ID) })
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
		m.running[def.Name] = cancel
		m.mu.Unlock()
		go func(alias string) { _ = ag.Run(child); m.mu.Lock(); delete(m.running, alias); m.mu.Unlock() }(def.Name)
	}
	m.mu.Lock()
	for alias, cancel := range m.running {
		if !present[alias] {
			cancel()
			delete(m.running, alias)
			_ = m.store.MarkProviderAccountDeleted("anthropic", alias)
		}
	}
	m.mu.Unlock()
}

func (m *AnthropicAgentManager) stopAll() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, cancel := range m.running {
		cancel()
	}
	m.running = make(map[string]context.CancelFunc)
}
