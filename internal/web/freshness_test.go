package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
	"github.com/onllm-dev/onwatch/v2/internal/config"
	"github.com/onllm-dev/onwatch/v2/internal/store"
)

// snapshotAt must reflect the real capture time of the newest stored snapshot so
// the dashboard can flag stale data. capturedAt alone is ambiguous: builders fall
// back to "now" when no snapshot exists.
func TestCurrentExposesSnapshotAtForStoredData(t *testing.T) {
	t.Parallel()

	s, err := store.New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })

	captured := time.Now().UTC().Add(-4 * time.Hour).Truncate(time.Second)
	if _, err := s.InsertCopilotSnapshot(&api.CopilotSnapshot{
		CapturedAt: captured,
		Quotas: []api.CopilotQuota{{
			Name:             "chat",
			Entitlement:      1000,
			Remaining:        750,
			PercentRemaining: 75,
		}},
	}); err != nil {
		t.Fatal(err)
	}

	h := NewHandler(s, nil, nil, nil, &config.Config{CopilotToken: "configured"})
	rec := httptest.NewRecorder()
	h.Current(rec, httptest.NewRequest(http.MethodGet, "/api/current?provider=copilot", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}

	var payload map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	got, _ := payload["snapshotAt"].(string)
	if got == "" {
		t.Fatalf("snapshotAt missing from copilot current payload: %s", rec.Body.String())
	}
	parsed, err := time.Parse(time.RFC3339, got)
	if err != nil {
		t.Fatalf("snapshotAt %q not RFC3339: %v", got, err)
	}
	if !parsed.Equal(captured) {
		t.Fatalf("snapshotAt = %s, want %s", parsed, captured)
	}
}

// With no stored snapshot there is nothing fresh to report, so snapshotAt must be
// absent (or null) rather than defaulting to the request time.
func TestCurrentOmitsSnapshotAtWithoutData(t *testing.T) {
	t.Parallel()

	s, err := store.New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })

	h := NewHandler(s, nil, nil, nil, &config.Config{CopilotToken: "configured"})
	rec := httptest.NewRecorder()
	h.Current(rec, httptest.NewRequest(http.MethodGet, "/api/current?provider=copilot", nil))

	var payload map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if v, ok := payload["snapshotAt"]; ok && v != nil {
		t.Fatalf("snapshotAt = %v, want absent when no snapshot is stored", v)
	}
}

func TestAppJSRendersProviderFreshness(t *testing.T) {
	t.Parallel()

	appJS := readStaticAppJS(t)
	for _, required := range []string{
		"FRESHNESS_STALE_MS",
		"FRESHNESS_ERROR_MS",
		"function providerFreshness",
		"homepage-harness-freshness",
		"data-freshness",
		"renderFreshnessBannerHTML",
	} {
		if !strings.Contains(appJS, required) {
			t.Fatalf("app.js missing freshness integration %q", required)
		}
	}
}

func TestStyleCSSStylesFreshnessStates(t *testing.T) {
	t.Parallel()

	data, err := staticFS.ReadFile("static/style.css")
	if err != nil {
		t.Fatalf("read static/style.css: %v", err)
	}
	css := string(data)
	for _, required := range []string{
		".homepage-harness-freshness",
		"[data-freshness=\"stale\"]",
		"[data-freshness=\"error\"]",
		".freshness-banner",
	} {
		if !strings.Contains(css, required) {
			t.Fatalf("style.css missing freshness style %q", required)
		}
	}
}
