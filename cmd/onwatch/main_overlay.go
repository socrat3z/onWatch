package main

import (
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/onllm-dev/onwatch/v2/internal/agent"
	"github.com/onllm-dev/onwatch/v2/internal/api"
	"github.com/onllm-dev/onwatch/v2/internal/config"
	"github.com/onllm-dev/onwatch/v2/internal/store"
)

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
