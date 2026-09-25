package notify

import (
	"strconv"
	"strings"

	"github.com/onllm-dev/onwatch/v2/internal/store"
)

// notificationQuotaKey generates a unique key for notification tracking. The
// provider is already a separate column in the notification log, so this key
// only has to namespace by account.
//
// Every spelling of "this provider's default account" - "", "0" from the
// ambient agent, and the default row's real ID from an account manager -
// collapses onto the bare quota key. That keeps one account in one dedup
// namespace across an ambient-to-manager switch (and preserves the keys
// written by every earlier version), while any other account is prefixed so
// two accounts alert independently on the same quota.
func (e *NotificationEngine) notificationQuotaKey(status QuotaStatus) string {
	id := strings.TrimSpace(status.AccountID)
	if id == "" || id == "0" || id == e.defaultAccountID(status.Provider) {
		return status.QuotaKey
	}
	return id + ":" + status.QuotaKey
}

// defaultAccountID returns the provider's default account ID as a string, or ""
// when it has none. It is cached because Check runs on every poll of every
// quota and the answer changes only when the database is recreated.
func (e *NotificationEngine) defaultAccountID(provider string) string {
	provider = normalizeNotificationProvider(provider)
	e.mu.RLock()
	cached, ok := e.defaultAccountIDs[provider]
	e.mu.RUnlock()
	if ok {
		return cached
	}
	resolved := ""
	if e.store != nil {
		id, err := e.store.LookupDefaultProviderAccountID(provider)
		if err != nil {
			e.logger.Debug("could not resolve default account for notification key", "provider", provider, "error", err)
			return ""
		}
		if id > 0 {
			resolved = strconv.FormatInt(id, 10)
		}
	}
	e.mu.Lock()
	if e.defaultAccountIDs == nil {
		e.defaultAccountIDs = make(map[string]string)
	}
	e.defaultAccountIDs[provider] = resolved
	e.mu.Unlock()
	return resolved
}

// accountLabel turns a raw account ID into the alias a user recognises. Alert
// bodies said "Account: 12" before this; a global autoincrement row ID is
// meaningless to the person reading the email.
func (e *NotificationEngine) accountLabel(provider, accountID string) string {
	id := strings.TrimSpace(accountID)
	if id == "" || id == "0" || e.store == nil {
		return ""
	}
	parsed, err := strconv.ParseInt(id, 10, 64)
	if err != nil {
		return id // Non-numeric IDs are already names (legacy Codex profiles).
	}
	account, err := e.store.GetProviderAccountByID(parsed)
	if err != nil || account == nil {
		return id
	}
	if provider != "" && !strings.EqualFold(account.Provider, normalizeNotificationProvider(provider)) {
		return id
	}
	return store.ProviderAccountAlias(*account)
}
