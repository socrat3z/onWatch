package agent

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/account"
	"github.com/onllm-dev/onwatch/v2/internal/api"
	"github.com/onllm-dev/onwatch/v2/internal/notify"
	"github.com/onllm-dev/onwatch/v2/internal/store"
	"github.com/onllm-dev/onwatch/v2/internal/tracker"
)

// AntigravityAgentManager runs one account home per alias. The CLI runner
// serializes fetches process-wide, keeping multiple configured accounts within
// the existing memory budget instead of warming several agy processes at once.
type AntigravityAgentManager struct {
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

func NewAntigravityAgentManager(s *store.Store, interval time.Duration, logger *slog.Logger) *AntigravityAgentManager {
	if logger == nil {
		logger = slog.Default()
	}
	return &AntigravityAgentManager{store: s, interval: interval, logger: logger, running: make(map[string]context.CancelFunc)}
}
func (m *AntigravityAgentManager) SetAuthRoot(root string) { m.root = root }
func (m *AntigravityAgentManager) SetAccountPollingCheck(check func(int64) bool) {
	m.pollingCheck = check
}
func (m *AntigravityAgentManager) SetNotifier(n *notify.NotificationEngine) { m.notifier = n }
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
	if m.root == "" {
		return
	}
	defs, err := (account.AntigravitySource{Root: m.root}).List(context.Background())
	if err != nil {
		m.logger.Warn("Antigravity account scan failed", "error", err)
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
		acc, err := m.store.CreateOrRestoreProviderAccount("antigravity", def.Name)
		if err != nil {
			m.logger.Error("register Antigravity account", "account", def.Name, "error", err)
			continue
		}
		child, cancel := context.WithCancel(ctx)
		tr := tracker.NewAntigravityTrackerForAccount(m.store, m.logger, acc.ID)
		ag := NewAntigravityAgent(api.NewAntigravityClient(m.logger), m.store, tr, m.interval, m.logger, NewSessionManager(m.store, fmt.Sprintf("antigravity:%d", acc.ID), 15*time.Minute, m.logger))
		ag.SetAccountContext(acc.ID, def.Name, def.AuthRoot)
		ag.SetNotifier(m.notifier)
		ag.SetSourceCheck(func() string { return api.AntigravitySourceCLI })
		ag.SetPollingCheck(func() bool { return m.pollingCheck == nil || m.pollingCheck(acc.ID) })
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
			_ = m.store.MarkProviderAccountDeleted("antigravity", alias)
		}
	}
	m.mu.Unlock()
}
func (m *AntigravityAgentManager) stopAll() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, cancel := range m.running {
		cancel()
	}
	m.running = make(map[string]context.CancelFunc)
}
