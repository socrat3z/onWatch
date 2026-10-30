package web

import (
	"fmt"

	"github.com/onllm-dev/onwatch/v2/internal/store"
)

func (h *Handler) queryProviderSessionsByAccount(provider string, accountID int64) ([]*store.Session, error) {
	sessions, err := h.store.QuerySessionHistory(fmt.Sprintf("%s:%d", provider, accountID))
	if err != nil || len(sessions) > 0 {
		return sessions, err
	}
	// Legacy single-account sessions predate account identity and remain visible
	// only for the provider's real default account.
	defaultAccount, defaultErr := h.store.ResolveDefaultProviderAccount(provider)
	if defaultErr != nil || defaultAccount.ID != accountID {
		return sessions, defaultErr
	}
	return h.store.QuerySessionHistory(provider)
}
