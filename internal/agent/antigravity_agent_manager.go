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

// AntigravityAgentManager runs one account home per alias. Serializing fetches
// bounds concurrency, not memory; the memory budget is held by the CLI runner's
// resident-process cap (api.agyMaxResidentSessions), which evicts one account's
// warm agy process when another account needs the slot.
type AntigravityAgentManager struct {
	store        *store.Store
	interval     time.Duration
	logger       *slog.Logger
	root         string
	pollingCheck func(int64) bool
	notifier     *notify.NotificationEngine
	mu           sync.Mutex
	running      map[string]*accountSession
	ctx          context.Context
}

func NewAntigravityAgentManager(s *store.Store, interval time.Duration, logger *slog.Logger) *AntigravityAgentManager {
	if logger == nil {
		logger = slog.Default()
	}
	return &AntigravityAgentManager{store: s, interval: interval, logger: logger, running: make(map[string]*accountSession)}
}
func (m *AntigravityAgentManager) SetAuthRoot(root string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.root = root
}
func (m *AntigravityAgentManager) SetAccountPollingCheck(check func(int64) bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pollingCheck = check
}
func (m *AntigravityAgentManager) SetNotifier(n *notify.NotificationEngine) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.notifier = n
}
func (m *AntigravityAgentManager) Run(ctx context.Context) error {
	m.mu.Lock()
	m.ctx = ctx
	m.mu.Unlock()
	m.Reload()
	tick := time.NewTicker(time.Minute)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			m.stopAll()
			return nil
		case <-tick.C:
			m.Reload()
		}
	}
}

func (m *AntigravityAgentManager) Reload() {
	root := m.authRoot()
	if root == "" || accountRootUnavailable(root) {
		return
	}
	defs, err := (account.AntigravitySource{Root: root}).List(context.Background())
	if err != nil {
		m.logger.Warn("Antigravity account scan failed", "error", err)
		return
	}
	present := make(map[string]bool, len(defs))
	for _, def := range defs {
		present[def.Name] = true
		acc, err := m.store.CreateOrRestoreProviderAccount("antigravity", def.Name)
		if err != nil {
			m.logger.Error("register Antigravity account", "account", def.Name, "error", err)
			continue
		}
		// Discovery cannot verify an Antigravity login without reading the
		// keyring, so the recorded state stays "unverified" and the dashboard
		// says so rather than showing an unexplained empty chart.
		if err := m.store.SetProviderAccountCredentialHealth(acc.ID, store.AccountCredentialsUnverified, filepath.Join(def.AuthRoot, ".gemini")); err != nil {
			m.logger.Warn("record Antigravity account health", "account", def.Name, "error", err)
		}
		m.mu.Lock()
		_, exists := m.running[def.Name]
		ctx := m.ctx
		m.mu.Unlock()
		if exists || ctx == nil {
			continue
		}
		child, cancel := context.WithCancel(ctx)
		tr := tracker.NewAntigravityTrackerForAccount(m.store, m.logger, acc.ID)
		ag := NewAntigravityAgent(api.NewAntigravityClient(m.logger), m.store, tr, m.interval, m.logger, NewSessionManager(m.store, fmt.Sprintf("antigravity:%d", acc.ID), 15*time.Minute, m.logger))
		ag.SetAccountContext(acc.ID, def.Name, def.AuthRoot)
		ag.SetNotifier(m.currentNotifier())
		ag.SetSourceCheck(func() string { return api.AntigravitySourceCLI })
		ag.SetPollingCheck(func() bool { return m.shouldPoll(acc.ID) })
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
	reconcileAccountRemovals(m.store, "antigravity", present, m.running, m.logger)
	m.mu.Unlock()
}
func (m *AntigravityAgentManager) stopAll() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, session := range m.running {
		session.cancel()
	}
	m.running = make(map[string]*accountSession)
}
