package web

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
	"github.com/onllm-dev/onwatch/v2/internal/config"
	"github.com/onllm-dev/onwatch/v2/internal/store"
)

// The homepage renders one widget per profile, so the combined view has to ship
// every live account instead of collapsing the provider to its default one.
func TestCurrentBothListsEveryAccountPerProvider(t *testing.T) {
	t.Parallel()

	t.Run("anthropic", func(t *testing.T) {
		s, _ := store.New(":memory:")
		defer s.Close()
		def, err := s.ResolveDefaultProviderAccount("anthropic")
		if err != nil {
			t.Fatal(err)
		}
		other, err := s.CreateOrRestoreProviderAccount("anthropic", "personal")
		if err != nil {
			t.Fatal(err)
		}
		now := time.Now().UTC()
		for _, snap := range []*api.AnthropicSnapshot{
			{AccountID: def.ID, CapturedAt: now.Add(-time.Minute), Quotas: []api.AnthropicQuota{{Name: "five_hour", Utilization: 11}}},
			{AccountID: other.ID, CapturedAt: now, Quotas: []api.AnthropicQuota{{Name: "five_hour", Utilization: 88}}},
		} {
			if _, err := s.InsertAnthropicSnapshot(snap); err != nil {
				t.Fatal(err)
			}
		}
		h := NewHandler(s, nil, nil, nil, multiProviderTestConfig())

		response := currentResponse(t, h, "/api/current?provider=both")
		if _, ok := response["anthropic"]; ok {
			t.Fatal("multi-account Anthropic must not also report an unattributed default payload")
		}
		accounts := accountPayloads(t, response, "anthropicAccounts")
		if len(accounts) != 2 {
			t.Fatalf("anthropicAccounts = %d entries, want 2", len(accounts))
		}
		byName := map[string]float64{}
		for _, account := range accounts {
			byName[account["accountName"].(string)] = firstQuotaUtilizationOf(t, account)
		}
		if byName["default"] != 11 || byName["personal"] != 88 {
			t.Fatalf("per-account utilization = %v, want default 11 and personal 88", byName)
		}
	})

	t.Run("antigravity", func(t *testing.T) {
		s, _ := store.New(":memory:")
		defer s.Close()
		def, err := s.ResolveDefaultProviderAccount("antigravity")
		if err != nil {
			t.Fatal(err)
		}
		other, err := s.CreateOrRestoreProviderAccount("antigravity", "personal")
		if err != nil {
			t.Fatal(err)
		}
		now := time.Now().UTC()
		for _, snap := range []*api.AntigravitySnapshot{
			{AccountID: def.ID, CapturedAt: now.Add(-time.Minute), Email: "default@example.com"},
			{AccountID: other.ID, CapturedAt: now, Email: "other@example.com"},
		} {
			if _, err := s.InsertAntigravitySnapshot(snap); err != nil {
				t.Fatal(err)
			}
		}
		h := NewHandler(s, nil, nil, nil, multiProviderTestConfig())

		response := currentResponse(t, h, "/api/current?provider=both")
		if _, ok := response["antigravity"]; ok {
			t.Fatal("multi-account Antigravity must not also report an unattributed default payload")
		}
		accounts := accountPayloads(t, response, "antigravityAccounts")
		if len(accounts) != 2 {
			t.Fatalf("antigravityAccounts = %d entries, want 2", len(accounts))
		}
		emails := map[string]bool{}
		for _, account := range accounts {
			emails[account["email"].(string)] = true
		}
		if !emails["default@example.com"] || !emails["other@example.com"] {
			t.Fatalf("per-account emails = %v, want one widget per profile", emails)
		}
	})
}

// A single-account install keeps the flat payload the rest of the UI reads.
func TestCurrentBothKeepsFlatPayloadForOneAccount(t *testing.T) {
	t.Parallel()

	s, _ := store.New(":memory:")
	defer s.Close()
	def, err := s.ResolveDefaultProviderAccount("anthropic")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.InsertAnthropicSnapshot(&api.AnthropicSnapshot{
		AccountID:  def.ID,
		CapturedAt: time.Now().UTC(),
		Quotas:     []api.AnthropicQuota{{Name: "five_hour", Utilization: 42}},
	}); err != nil {
		t.Fatal(err)
	}
	h := NewHandler(s, nil, nil, nil, multiProviderTestConfig())

	response := currentResponse(t, h, "/api/current?provider=both")
	if _, ok := response["anthropicAccounts"]; ok {
		t.Fatal("single-account install must not switch to the grouped payload")
	}
	if _, ok := response["anthropic"].(map[string]interface{}); !ok {
		t.Fatalf("anthropic payload = %#v, want the flat map", response["anthropic"])
	}
}

// Aliases are what the picker shows, so the homepage widget has to use them too.
func TestCurrentBothUsesDisplayAliasForAccountName(t *testing.T) {
	t.Parallel()

	s, _ := store.New(":memory:")
	defer s.Close()
	if _, err := s.ResolveDefaultProviderAccount("anthropic"); err != nil {
		t.Fatal(err)
	}
	other, err := s.CreateOrRestoreProviderAccount("anthropic", "personal")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateProviderAccountAlias("anthropic", other.ID, "Acme work"); err != nil {
		t.Fatal(err)
	}
	h := NewHandler(s, nil, nil, nil, multiProviderTestConfig())

	accounts := accountPayloads(t, currentResponse(t, h, "/api/current?provider=both"), "anthropicAccounts")
	found := false
	for _, account := range accounts {
		if account["accountName"] == "Acme work" {
			found = true
			if account["name"] != "personal" {
				t.Fatalf("durable folder name = %v, want personal", account["name"])
			}
		}
	}
	if !found {
		t.Fatalf("aliased account missing from %v", accounts)
	}
}

func accountPayloads(t *testing.T, response map[string]interface{}, key string) []map[string]interface{} {
	t.Helper()
	raw, ok := response[key].([]interface{})
	if !ok {
		t.Fatalf("%s missing from combined current response: %v", key, response)
	}
	accounts := make([]map[string]interface{}, 0, len(raw))
	for _, item := range raw {
		account, ok := item.(map[string]interface{})
		if !ok {
			t.Fatalf("%s entry is not an object: %#v", key, item)
		}
		accounts = append(accounts, account)
	}
	return accounts
}

func firstQuotaUtilizationOf(t *testing.T, payload map[string]interface{}) float64 {
	t.Helper()
	quotas, ok := payload["quotas"].([]interface{})
	if !ok || len(quotas) == 0 {
		t.Fatalf("account payload has no quotas: %v", payload)
	}
	return quotas[0].(map[string]interface{})["utilization"].(float64)
}

// The combined view only answers when several providers are configured.
func multiProviderTestConfig() *config.Config {
	return &config.Config{
		AnthropicToken:     "test_anthropic_token",
		AntigravityEnabled: true,
		PollInterval:       60 * time.Second,
		Port:               9211,
		AdminUser:          "admin",
		AdminPass:          "test",
		DBPath:             "./test.db",
	}
}

// The homepage widget grid has to consume the grouped payload, otherwise the
// extra accounts land in the response and are never drawn.
func TestAppJSHomepageRendersEveryProfileWidget(t *testing.T) {
	t.Parallel()

	appJS := readStaticAppJS(t)
	for _, required := range []string{
		"const MULTI_ACCOUNT_PROVIDER_KEYS = {",
		"anthropic: 'anthropicAccounts'",
		"antigravity: 'antigravityAccounts'",
		"function renderHomepageAccountsHTML(provider, accounts)",
		"function groupedAccountEntry(provider, accounts)",
		"accountsGroup: visible,",
		"codex: 'codexAccounts'",
		"minimax: 'minimaxAccounts'",
		"localStorage.setItem(providerAccountStorageKey(provider), block.dataset.accountId)",
	} {
		if !strings.Contains(appJS, required) {
			t.Fatalf("homepage multi-account widgets missing %q", required)
		}
	}
}

// accountName is a display label everywhere it is consumed, so it must mean the
// display alias for every provider - not the folder name for some of them.
func TestCurrentBothAccountNameIsTheAliasForEveryProvider(t *testing.T) {
	t.Parallel()

	for _, provider := range []string{"anthropic", "antigravity", "codex", "minimax"} {
		t.Run(provider, func(t *testing.T) {
			s, _ := store.New(":memory:")
			defer s.Close()
			if _, err := s.ResolveDefaultProviderAccount(provider); err != nil {
				t.Fatal(err)
			}
			other, err := s.CreateOrRestoreProviderAccount(provider, "personal")
			if err != nil {
				t.Fatal(err)
			}
			if err := s.UpdateProviderAccountAlias(provider, other.ID, "Acme work"); err != nil {
				t.Fatal(err)
			}
			h := NewHandler(s, nil, nil, nil, allAccountProvidersTestConfig())

			accounts := accountPayloads(t, currentResponse(t, h, "/api/current?provider=both"), provider+"Accounts")
			for _, account := range accounts {
				if account["name"] != "personal" {
					continue
				}
				if account["accountName"] != "Acme work" {
					t.Fatalf("%s accountName = %v, want the display alias", provider, account["accountName"])
				}
				return
			}
			t.Fatalf("%s aliased account missing from %v", provider, accounts)
		})
	}
}

// Every account-scoped payload says which account it is, whether it arrived
// grouped or flat. Without that a single-account payload is indistinguishable
// from an unattributed one.
func TestCurrentBothFlatPayloadCarriesAccountIdentity(t *testing.T) {
	t.Parallel()

	for _, provider := range []string{"anthropic", "antigravity", "codex", "minimax"} {
		t.Run(provider, func(t *testing.T) {
			s, _ := store.New(":memory:")
			defer s.Close()
			def, err := s.ResolveDefaultProviderAccount(provider)
			if err != nil {
				t.Fatal(err)
			}
			h := NewHandler(s, nil, nil, nil, allAccountProvidersTestConfig())

			payload, ok := currentResponse(t, h, "/api/current?provider=both")[provider].(map[string]interface{})
			if !ok {
				t.Fatalf("%s flat payload missing", provider)
			}
			id, ok := payload["accountId"].(float64)
			if !ok {
				t.Fatalf("%s flat payload carries no accountId: %v", provider, payload)
			}
			if int64(id) != def.ID {
				t.Fatalf("%s accountId = %v, want the default account %d", provider, payload["accountId"], def.ID)
			}
			if payload["accountName"] != "default" {
				t.Fatalf("%s accountName = %v, want default", provider, payload["accountName"])
			}
		})
	}
}

// Per-account visibility is keyed "<provider>:<id>" for every provider, so an
// account switched off must not be shipped to the dashboard at all.
func TestCurrentBothHonoursPerAccountVisibility(t *testing.T) {
	t.Parallel()

	for _, provider := range []string{"anthropic", "antigravity", "codex", "minimax"} {
		t.Run(provider, func(t *testing.T) {
			s, _ := store.New(":memory:")
			defer s.Close()
			if _, err := s.ResolveDefaultProviderAccount(provider); err != nil {
				t.Fatal(err)
			}
			hidden, err := s.CreateOrRestoreProviderAccount(provider, "personal")
			if err != nil {
				t.Fatal(err)
			}
			if err := s.SetSetting("provider_visibility", fmt.Sprintf(`{"%s:%d":{"polling":false}}`, provider, hidden.ID)); err != nil {
				t.Fatal(err)
			}
			h := NewHandler(s, nil, nil, nil, allAccountProvidersTestConfig())

			response := currentResponse(t, h, "/api/current?provider=both")
			if accounts, ok := response[provider+"Accounts"]; ok {
				t.Fatalf("%s still grouped after hiding one of two accounts: %v", provider, accounts)
			}
			payload, ok := response[provider].(map[string]interface{})
			if !ok {
				t.Fatalf("%s flat payload missing for the one visible account", provider)
			}
			if payload["accountName"] == "personal" {
				t.Fatalf("%s reported the hidden account", provider)
			}
		})
	}
}

func allAccountProvidersTestConfig() *config.Config {
	cfg := multiProviderTestConfig()
	cfg.CodexToken = "test_codex_token"
	cfg.MiniMaxAPIKey = "test_minimax_key"
	return cfg
}
