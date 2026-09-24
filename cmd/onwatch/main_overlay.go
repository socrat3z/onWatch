package main

import (
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/onllm-dev/onwatch/v2/internal/agent"
	"github.com/onllm-dev/onwatch/v2/internal/api"
	"github.com/onllm-dev/onwatch/v2/internal/config"
	"github.com/onllm-dev/onwatch/v2/internal/notify"
	"github.com/onllm-dev/onwatch/v2/internal/store"
)

// forkAccountManagers owns the downstream multi-account lifecycle hooks. The
// upstream startup path only needs to create, configure, and register this one
// coordinator as new fork-managed providers are added.
type forkAccountManagers struct {
	db          *store.Store
	anthropic   *agent.AnthropicAgentManager
	antigravity *agent.AntigravityAgentManager
}

func setupForkAccountManagers(cfg *config.Config, db *store.Store, logger *slog.Logger) *forkAccountManagers {
	return &forkAccountManagers{
		db:          db,
		anthropic:   setupAnthropicAccountManager(cfg, db, logger),
		antigravity: setupAntigravityAccountManager(cfg, db, logger),
	}
}

func (m *forkAccountManagers) SetNotifier(notifier *notify.NotificationEngine) {
	if m.anthropic != nil {
		m.anthropic.SetNotifier(notifier)
		m.anthropic.SetAccountPollingCheck(func(accountID int64) bool {
			return isAccountPollingEnabled(m.db, "anthropic", accountID)
		})
	}
	if m.antigravity != nil {
		m.antigravity.SetNotifier(notifier)
		m.antigravity.SetAccountPollingCheck(func(accountID int64) bool {
			return isAccountPollingEnabled(m.db, "antigravity", accountID)
		})
	}
}

func (m *forkAccountManagers) Register(manager *agent.AgentManager) {
	if m.anthropic != nil {
		manager.RegisterFactory("anthropic", func() (agent.AgentRunner, error) { return m.anthropic, nil })
	}
	if m.antigravity != nil {
		manager.RegisterFactory("antigravity", func() (agent.AgentRunner, error) { return m.antigravity, nil })
	}
}

// setupAnthropicAccountManager initializes multi-account management for Anthropic
// when ANTHROPIC_AUTH_ROOT is configured.
func setupAnthropicAccountManager(cfg *config.Config, db *store.Store, logger *slog.Logger) *agent.AnthropicAgentManager {
	if cfg.AnthropicAuthRoot == "" {
		return nil
	}
	mgr := agent.NewAnthropicAgentManager(db, cfg.PollInterval, logger)
	mgr.SetAuthRoot(cfg.AnthropicAuthRoot)
	logger.Info("Anthropic named account discovery configured", "root", cfg.AnthropicAuthRoot)
	if cfg.AnthropicToken != "" {
		logger.Warn("ANTHROPIC_TOKEN is ignored while ANTHROPIC_AUTH_ROOT is set",
			"reason", "each named account authenticates from its own <root>/<alias>/.claude/.credentials.json",
			"root", cfg.AnthropicAuthRoot)
	}
	return mgr
}

// setupAntigravityAccountManager initializes multi-account management for Antigravity
// when ANTIGRAVITY_AUTH_ROOT is configured.
func setupAntigravityAccountManager(cfg *config.Config, db *store.Store, logger *slog.Logger) *agent.AntigravityAgentManager {
	if cfg.AntigravityAuthRoot == "" {
		return nil
	}
	mgr := agent.NewAntigravityAgentManager(db, cfg.PollInterval, logger)
	mgr.SetAuthRoot(cfg.AntigravityAuthRoot)
	logger.Info("Antigravity named account discovery configured", "root", cfg.AntigravityAuthRoot)
	if cfg.AntigravitySource != api.AntigravitySourceCLI {
		logger.Warn("ANTIGRAVITY_SOURCE is ignored while ANTIGRAVITY_AUTH_ROOT is set",
			"ignored_value", cfg.AntigravitySource,
			"effective_source", api.AntigravitySourceCLI,
			"reason", "the IDE probe cannot be scoped to one account home, so every named account polls through the agy CLI")
	}
	return mgr
}

// isAccountPollingEnabled mirrors the dashboard's provider visibility model for
// dynamic account managers. A per-account setting always wins over the provider
// toggle, so users can pause a work alias without losing its history.
func isAccountPollingEnabled(db *store.Store, provider string, accountID int64) bool {
	value, err := db.GetSetting("provider_visibility")
	if err != nil || value == "" {
		return true
	}
	var visibility map[string]interface{}
	if json.Unmarshal([]byte(value), &visibility) != nil {
		return true
	}
	if entry, ok := visibility[fmt.Sprintf("%s:%d", provider, accountID)].(map[string]interface{}); ok {
		if enabled, exists := entry["polling"].(bool); exists {
			return enabled
		}
	}
	if entry, ok := visibility[provider].(map[string]interface{}); ok {
		if enabled, exists := entry["polling"].(bool); exists {
			return enabled
		}
	}
	return true
}
