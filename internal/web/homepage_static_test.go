package web

import (
	"strings"
	"testing"
)

func TestAppJSHomepageIncludesBalancesAndLimits(t *testing.T) {
	t.Parallel()

	appJS := readStaticAppJS(t)
	for _, required := range []string{
		"both: 'Home'",
		"function renderHomepageMetricsHTML(quotas)",
		"homepage-harness-metric",
		"provider === 'moonshot' && payload.balance",
		"document.getElementById('quota-grid-moonshot')",
		"payload.creditsBalance",
		"displayName: 'Credits Balance'",
		"displayName: 'Available Balance'",
		"used: item.usage",
		"total: item.limit",
		"total: quota.total ?? quota.limit ?? quota.entitlement",
	} {
		if !strings.Contains(appJS, required) {
			t.Fatalf("homepage current-balance integration missing %q", required)
		}
	}
	if strings.Contains(appJS, "// Redirect to saved default provider if no explicit provider in URL") {
		t.Fatal("root dashboard must remain on Home instead of redirecting to a saved harness")
	}
	if !strings.Contains(appJS, "if (provider === 'both') return;") {
		t.Fatal("homepage must skip insight and history requests")
	}
	renderStart := strings.Index(appJS, "function renderAllProvidersView()")
	if renderStart < 0 {
		t.Fatal("homepage renderer not found")
	}
	renderEnd := strings.Index(appJS[renderStart:], "function updateBothCharts(")
	if renderEnd < 0 {
		t.Fatal("homepage renderer boundaries not found")
	}
	renderBody := appJS[renderStart : renderStart+renderEnd]
	if strings.Contains(renderBody, "<canvas") || strings.Contains(renderBody, "renderProviderInsightsHTML") {
		t.Fatal("compact homepage must not render graphs or insight cards")
	}
}
