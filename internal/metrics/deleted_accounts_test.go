package metrics

import (
	"strconv"
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
	"github.com/onllm-dev/onwatch/v2/internal/store"
)

// TestScrapeStopsSeriesForSoftDeletedAccounts pins the recorded policy: removing
// an account home stops its Prometheus series instead of freezing it forever.
func TestScrapeStopsSeriesForSoftDeletedAccounts(t *testing.T) {
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	live, err := s.CreateOrRestoreProviderAccount("antigravity", "work")
	if err != nil {
		t.Fatalf("create live account: %v", err)
	}
	retired, err := s.CreateOrRestoreProviderAccount("antigravity", "personal")
	if err != nil {
		t.Fatalf("create retired account: %v", err)
	}

	now := time.Now().UTC()
	for _, account := range []int64{live.ID, retired.ID} {
		if _, err := s.InsertAntigravitySnapshot(&api.AntigravitySnapshot{
			AccountID:  account,
			CapturedAt: now,
			Models:     []api.AntigravityModelQuota{{ModelID: "five_hour", RemainingPercent: 58}},
		}); err != nil {
			t.Fatalf("InsertAntigravitySnapshot: %v", err)
		}
	}

	liveLabels := map[string]string{"provider": "antigravity", "quota_type": "five_hour", "account_id": strconv.FormatInt(live.ID, 10)}
	retiredLabels := map[string]string{"provider": "antigravity", "quota_type": "five_hour", "account_id": strconv.FormatInt(retired.ID, 10)}

	m := New()
	m.Scrape(s, time.Minute)
	families, err := m.Gather().Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	if !hasGaugeMetric(families, "onwatch_quota_utilization_percent", liveLabels) {
		t.Fatal("a live account must export its quota series")
	}
	if !hasGaugeMetric(families, "onwatch_quota_utilization_percent", retiredLabels) {
		t.Fatal("both accounts must export before either is removed")
	}

	if err := s.MarkProviderAccountDeletedByID(retired.ID); err != nil {
		t.Fatalf("MarkProviderAccountDeletedByID: %v", err)
	}

	m = New()
	m.Scrape(s, time.Minute)
	families, err = m.Gather().Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	if !hasGaugeMetric(families, "onwatch_quota_utilization_percent", liveLabels) {
		t.Fatal("removing one account must not stop the other's series")
	}
	if hasGaugeMetric(families, "onwatch_quota_utilization_percent", retiredLabels) {
		t.Fatal("a soft-deleted account must stop exporting - its label pair would grow cardinality forever")
	}
	if hasGaugeMetric(families, "onwatch_account_info", map[string]string{"provider": "antigravity", "account_id": strconv.FormatInt(retired.ID, 10), "account_name": "personal"}) {
		t.Fatal("a soft-deleted account must not keep announcing itself")
	}
}
