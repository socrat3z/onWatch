package metrics

import (
	"strconv"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/store"
	"github.com/prometheus/client_golang/prometheus"
)

// Policy: Prometheus reports only live accounts. A soft-deleted account never
// gains new data, so its series would flat-line forever while its label pair
// stays in every scrape - unbounded cardinality growth for a value that is
// frozen. Its history is not lost: the dashboard and API still query it.

func (m *Metrics) scrapeAnthropic(s *store.Store, staleThreshold time.Duration) {
	method := "anthropic"
	accounts, err := s.QueryActiveProviderAccounts(method)
	if err != nil {
		m.scrapeErrorsTotal.WithLabelValues(method, "query_failed").Inc()
		return
	}
	if len(accounts) == 0 {
		defaultAccount, defaultErr := s.ResolveDefaultProviderAccount(method)
		if defaultErr != nil {
			m.scrapeErrorsTotal.WithLabelValues(method, "query_failed").Inc()
			return
		}
		accounts = []store.ProviderAccount{*defaultAccount}
	}
	for _, account := range accounts {
		accountID := strconv.FormatInt(account.ID, 10)
		m.accountInfo.WithLabelValues(method, accountID, store.ProviderAccountAlias(account)).Set(1)
		snap, queryErr := s.QueryLatestAnthropic(account.ID)
		if queryErr != nil {
			m.scrapeErrorsTotal.WithLabelValues(method, "query_failed").Inc()
			continue
		}
		if snap == nil {
			continue
		}
		m.recordLastCycleAge(method, accountID, snap.CapturedAt, staleThreshold)
		for _, v := range snap.Quotas {
			labels := prometheus.Labels{"provider": method, "quota_type": v.Name, "account_id": accountID}
			m.quotaUtilization.With(labels).Set(v.Utilization)
			if v.ResetsAt != nil && !v.ResetsAt.IsZero() {
				m.quotaResetTimestamp.With(labels).Set(float64(v.ResetsAt.Unix()))
			}
		}
	}
}

func (m *Metrics) scrapeAntigravity(s *store.Store, staleThreshold time.Duration) {
	method := "antigravity"
	accounts, err := s.QueryActiveProviderAccounts(method)
	if err != nil {
		m.scrapeErrorsTotal.WithLabelValues(method, "query_failed").Inc()
		return
	}
	if len(accounts) == 0 {
		defaultAccount, defaultErr := s.ResolveDefaultProviderAccount(method)
		if defaultErr != nil {
			m.scrapeErrorsTotal.WithLabelValues(method, "query_failed").Inc()
			return
		}
		accounts = []store.ProviderAccount{*defaultAccount}
	}
	for _, account := range accounts {
		accountID := strconv.FormatInt(account.ID, 10)
		m.accountInfo.WithLabelValues(method, accountID, store.ProviderAccountAlias(account)).Set(1)
		snap, queryErr := s.QueryLatestAntigravity(account.ID)
		if queryErr != nil {
			m.scrapeErrorsTotal.WithLabelValues(method, "query_failed").Inc()
			continue
		}
		if snap == nil {
			continue
		}
		m.recordLastCycleAge(method, accountID, snap.CapturedAt, staleThreshold)
		for _, v := range snap.Models {
			labels := prometheus.Labels{"provider": method, "quota_type": v.ModelID, "account_id": accountID}
			m.quotaUtilization.With(labels).Set(100 - v.RemainingPercent)
			if v.ResetTime != nil && !v.ResetTime.IsZero() {
				m.quotaResetTimestamp.With(labels).Set(float64(v.ResetTime.Unix()))
			}
		}
		if snap.PromptCredits > 0 {
			m.creditsBalance.With(prometheus.Labels{"provider": method, "account_id": accountID, "unit": "prompt_credits"}).Set(snap.PromptCredits)
		}
	}
}
