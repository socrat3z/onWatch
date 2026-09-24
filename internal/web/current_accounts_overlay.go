package web

import (
	"fmt"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
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

func (h *Handler) buildAntigravityCurrentForAccount(accountID int64) map[string]interface{} {
	// The regular builder has the full presentation treatment; selecting an
	// account changes only the underlying snapshot, never the quota semantics.
	now := time.Now().UTC()
	response := map[string]interface{}{"capturedAt": now.Format(time.RFC3339), "quotas": []interface{}{}, "pools": []interface{}{}}
	latest, err := h.store.QueryLatestAntigravity(accountID)
	if err != nil || latest == nil {
		return response
	}
	response["capturedAt"], response["snapshotAt"] = latest.CapturedAt.Format(time.RFC3339), latest.CapturedAt.Format(time.RFC3339)
	if latest.Email != "" {
		response["email"] = latest.Email
	}
	if latest.PlanName != "" {
		response["planName"] = latest.PlanName
	}
	if latest.Source != "" {
		response["source"] = latest.Source
	}
	if latest.Source == api.AntigravitySourceCLI {
		quotas := h.buildAntigravityCLIQuotas(latest.Models)
		response["quotas"], response["pools"] = quotas, quotas
		applyDisplayModeToResponse(response, h.getDisplayMode("antigravity"))
		return response
	}
	groups := api.GroupAntigravityModelsByLogicalQuota(latest.Models)
	quotas := make([]map[string]interface{}, 0, len(groups))
	for _, group := range groups {
		quotas = append(quotas, map[string]interface{}{"modelId": group.GroupKey, "quotaGroup": group.GroupKey, "label": group.DisplayName, "displayName": group.DisplayName, "remainingFraction": group.RemainingFraction, "remainingPercent": group.RemainingPercent, "usagePercent": group.UsagePercent, "isExhausted": group.IsExhausted, "status": antigravityUsageStatus(group.UsagePercent), "models": group.ModelIDs, "modelLabels": group.Labels, "color": group.Color})
	}
	response["quotas"], response["pools"] = quotas, quotas
	applyDisplayModeToResponse(response, h.getDisplayMode("antigravity"))
	return response
}
