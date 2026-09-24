package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/onllm-dev/onwatch/v2/internal/store"
)

// defaultProviderAccountID resolves a provider's default account for views that
// are not account-aware yet, so no store query is left to pick whichever
// account happened to poll last. Zero means "let the store resolve the default".
func (h *Handler) defaultProviderAccountID(provider string) int64 {
	if h.store == nil {
		return 0
	}
	account, err := h.store.ResolveDefaultProviderAccount(provider)
	if err != nil {
		return 0
	}
	return account.ID
}

func (h *Handler) parseProviderAccountID(r *http.Request, provider string) (int64, error) {
	value := strings.TrimSpace(r.URL.Query().Get("account"))
	if value == "" {
		if h.store == nil {
			return 0, nil
		}
		account, err := h.store.ResolveDefaultProviderAccount(provider)
		if err != nil {
			// A healthy database is migrated with a default account. Let a
			// storage failure surface from the data query as a server error.
			return 0, nil
		}
		return account.ID, nil
	}
	id, err := strconv.ParseInt(value, 10, 64)
	if err != nil || id <= 0 {
		return 0, fmt.Errorf("invalid %s account", provider)
	}
	account, err := h.store.ResolveProviderAccount(provider, id)
	if err != nil {
		return 0, err
	}
	return account.ID, nil
}

// ProviderAccounts exposes safe account presentation management. Credentials
// are intentionally not accepted here - login and discovery remain the source
// of truth for which accounts can poll.
func (h *Handler) ProviderAccounts(w http.ResponseWriter, r *http.Request) {
	provider := strings.TrimSpace(r.URL.Query().Get("provider"))
	if provider != "anthropic" && provider != "antigravity" {
		respondError(w, http.StatusBadRequest, "unsupported provider")
		return
	}
	if r.Method == http.MethodGet {
		accounts, err := h.store.QueryProviderAccounts(provider)
		if err != nil {
			respondError(w, http.StatusInternalServerError, "failed to query accounts")
			return
		}
		response := make([]map[string]interface{}, 0, len(accounts))
		for _, account := range accounts {
			state, credentialPath := store.ProviderAccountCredentialHealth(account)
			response = append(response, map[string]interface{}{
				"id":        account.ID,
				"name":      account.Name,
				"alias":     store.ProviderAccountAlias(account),
				"deletedAt": account.DeletedAt,
				// isDefault marks the pre-discovery account that holds history
				// from before this install had named accounts.
				"isDefault": account.Name == store.DefaultProviderAccountName,
				"health": map[string]interface{}{
					"credentials":    state,
					"credentialPath": credentialPath,
				},
			})
		}
		respondJSON(w, http.StatusOK, map[string]interface{}{"accounts": response})
		return
	}
	if r.Method != http.MethodPatch {
		respondError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var request struct {
		AccountID int64  `json:"account_id"`
		Alias     string `json:"alias"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&request); err != nil {
		respondError(w, http.StatusBadRequest, "invalid account request")
		return
	}
	if err := h.store.UpdateProviderAccountAlias(provider, request.AccountID, request.Alias); err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]interface{}{"ok": true})
}

// accountTelemetryEnabled resolves the "<provider>:<id>" visibility key the
// settings UI writes for every multi-account provider, falling back to the
// provider-wide switch. One implementation, so an account switched off is
// honoured the same way whichever provider it belongs to.
func accountTelemetryEnabled(visibility map[string]interface{}, provider string, accountID int64) bool {
	accountKey := fmt.Sprintf("%s:%d", provider, accountID)
	if polling, exists := providerPollingValue(visibility[accountKey]); exists {
		return polling
	}
	return providerTelemetryEnabled(visibility, provider)
}

// providerAccountUsages builds usage maps across all active accounts for a provider.
func (h *Handler) providerAccountUsages(provider string, build func(int64) map[string]interface{}, fallbackAccountID func() int64) []map[string]interface{} {
	if h.store == nil {
		return nil
	}
	accounts, err := h.store.QueryActiveProviderAccounts(provider)
	if err != nil {
		h.logger.Error("failed to query provider accounts", "provider", provider, "error", err)
		return nil
	}
	if len(accounts) == 0 {
		id := fallbackAccountID()
		if id <= 0 {
			return nil
		}
		accounts = []store.ProviderAccount{{ID: id, Name: store.DefaultProviderAccountName}}
	}

	usages := make([]map[string]interface{}, 0, len(accounts))
	for _, account := range accounts {
		usage := build(account.ID)
		usage["accountId"] = account.ID
		usage["id"] = account.ID
		usage["name"] = account.Name
		usage["accountName"] = store.ProviderAccountAlias(account)
		usages = append(usages, usage)
	}
	return usages
}

// attachAccountScopedCurrent writes one provider's slice of the combined
// payload under a single rule: two or more visible accounts are keyed
// "<provider>Accounts", one is keyed by the provider name so single-account
// installs keep the flat shape, and a provider whose every account has been
// switched off is omitted rather than silently reappearing as its default.
func (h *Handler) attachAccountScopedCurrent(response, visibility map[string]interface{}, provider string, build func(int64) map[string]interface{}, fallbackAccountID func() int64) {
	usages := h.providerAccountUsages(provider, build, fallbackAccountID)
	if len(usages) == 0 {
		// No store, or no account to attribute the numbers to.
		response[provider] = build(fallbackAccountID())
		return
	}

	visible := make([]map[string]interface{}, 0, len(usages))
	for _, usage := range usages {
		if accountTelemetryEnabled(visibility, provider, providerUsageAccountID(usage)) {
			visible = append(visible, usage)
		}
	}
	switch len(visible) {
	case 0:
		// Every account is hidden on purpose - say nothing about this provider.
	case 1:
		response[provider] = visible[0]
	default:
		response[provider+"Accounts"] = visible
	}
}

// providerUsageAccountID reads back the identity providerAccountUsages wrote.
func providerUsageAccountID(usage map[string]interface{}) int64 {
	if usage == nil {
		return 0
	}
	switch v := usage["accountId"].(type) {
	case int64:
		return v
	case int:
		return int64(v)
	case float64:
		return int64(v)
	}
	return 0
}

// accountIdentity says which account a payload belongs to. The combined
// dashboard has no account picker, so without this the Anthropic and
// Antigravity cards show numbers with no indication of whose they are.
// accountCount lets the UI stay completely unchanged on a single-account
// install and only add labelling once a second account exists.
func (h *Handler) accountIdentity(provider string, accountID int64) map[string]interface{} {
	if h.store == nil || accountID <= 0 {
		return nil
	}
	account, err := h.store.GetProviderAccountByID(accountID)
	if err != nil || account == nil {
		return nil
	}
	count := 0
	if accounts, err := h.store.QueryActiveProviderAccounts(provider); err == nil {
		count = len(accounts)
	}
	state, credentialPath := store.ProviderAccountCredentialHealth(*account)
	return map[string]interface{}{
		"id":           account.ID,
		"name":         account.Name,
		"alias":        store.ProviderAccountAlias(*account),
		"isDefault":    account.Name == store.DefaultProviderAccountName,
		"accountCount": count,
		"health": map[string]interface{}{
			"credentials":    state,
			"credentialPath": credentialPath,
		},
	}
}
