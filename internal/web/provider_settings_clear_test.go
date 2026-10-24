package web

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/onllm-dev/onwatch/v2/internal/config"
	"github.com/onllm-dev/onwatch/v2/internal/store"
)

func newClearTestStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// HIGH 1: an upgrade must not wipe an OpenCode workspace ID that lives in .env.
// v2.14.11 always sent workspace_id (even empty) and persisted a bare "",
// so a stored {"opencode":{"workspace_id":""}} must be treated as "keep the
// env/current value", not as an intentional clear.
func TestApplyProviderSettings_LegacyBlankWorkspaceIDKeepsEnvValue(t *testing.T) {
	s := newClearTestStore(t)
	if err := s.SetSetting("provider_settings", `{"opencode":{"workspace_id":""}}`); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}
	cfg := &config.Config{OpenCodeGoWorkspaceID: "wrk_from_env"}
	ApplyProviderSettingsFromDB(s, cfg, nil)
	if cfg.OpenCodeGoWorkspaceID != "wrk_from_env" {
		t.Fatalf("legacy blank workspace_id wiped the .env value, got %q", cfg.OpenCodeGoWorkspaceID)
	}
}

// The same rule must hold for a secret field: a legacy bare "" api_key keeps
// the env value until an explicit clear is recorded.
func TestApplyProviderSettings_LegacyBlankAPIKeyKeepsEnvValue(t *testing.T) {
	s := newClearTestStore(t)
	if err := s.SetSetting("provider_settings", `{"opencode":{"api_key":""}}`); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}
	cfg := &config.Config{OpenCodeGoAPIKey: "oc_sk_from_env"}
	ApplyProviderSettingsFromDB(s, cfg, nil)
	if cfg.OpenCodeGoAPIKey != "oc_sk_from_env" {
		t.Fatalf("legacy blank api_key wiped the .env value, got %q", cfg.OpenCodeGoAPIKey)
	}
}

// An explicit clear records a marker in the stored settings, so after a restart
// (a fresh ApplyProviderSettingsFromDB) the value stays cleared instead of
// being restored from .env.
func TestUpdateSettings_ExplicitClearPersistsAcrossRestart(t *testing.T) {
	cfg := &config.Config{OpenCodeGoWorkspaceID: "wrk_1", OpenCodeGoAPIKey: "oc_sk_1"}
	h, s, _ := newReconcileHandler(t, cfg, map[string]bool{})

	body := strings.NewReader(`{"provider_settings":{"opencode":{"clear_workspace_id":true,"clear_api_key":true}}}`)
	req := httptest.NewRequest(http.MethodPut, "/api/settings", body)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.UpdateSettings(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 body=%s", rr.Code, rr.Body.String())
	}

	// Simulate a daemon restart: a fresh config seeded from .env, then apply.
	fresh := &config.Config{OpenCodeGoWorkspaceID: "wrk_env", OpenCodeGoAPIKey: "oc_sk_env"}
	ApplyProviderSettingsFromDB(s, fresh, nil)
	if fresh.OpenCodeGoWorkspaceID != "" {
		t.Fatalf("explicit clear did not survive restart, workspace_id=%q", fresh.OpenCodeGoWorkspaceID)
	}
	if fresh.OpenCodeGoAPIKey != "" {
		t.Fatalf("explicit clear did not survive restart, api_key=%q", fresh.OpenCodeGoAPIKey)
	}
}

// Setting a non-empty value after a clear removes the marker, so the new value
// is applied (and would survive a restart).
func TestUpdateSettings_ClearThenSetAppliesNewValue(t *testing.T) {
	cfg := &config.Config{OpenCodeGoWorkspaceID: "wrk_1", OpenCodeGoAPIKey: "oc_sk_1"}
	h, s, _ := newReconcileHandler(t, cfg, map[string]bool{})

	clear := strings.NewReader(`{"provider_settings":{"opencode":{"clear_workspace_id":true,"clear_api_key":true}}}`)
	req := httptest.NewRequest(http.MethodPut, "/api/settings", clear)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.UpdateSettings(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("clear status = %d body=%s", rr.Code, rr.Body.String())
	}

	set := strings.NewReader(`{"provider_settings":{"opencode":{"workspace_id":"wrk_new","api_key":"oc_sk_new"}}}`)
	req = httptest.NewRequest(http.MethodPut, "/api/settings", set)
	req.Header.Set("Content-Type", "application/json")
	rr = httptest.NewRecorder()
	h.UpdateSettings(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("set status = %d body=%s", rr.Code, rr.Body.String())
	}

	fresh := &config.Config{}
	ApplyProviderSettingsFromDB(s, fresh, nil)
	if fresh.OpenCodeGoWorkspaceID != "wrk_new" {
		t.Fatalf("workspace_id = %q, want wrk_new", fresh.OpenCodeGoWorkspaceID)
	}
	if fresh.OpenCodeGoAPIKey != "oc_sk_new" {
		t.Fatalf("api_key = %q, want oc_sk_new", fresh.OpenCodeGoAPIKey)
	}
}

// MEDIUM 2: a save for one provider must not re-apply every other provider's
// stored fields to the live config. Before the fix, an OpenCode save rewrote
// cfg.ZaiRegion/ZaiAPIKey from the DB while handler/menubar reads of those
// fields are unlocked, which is a data race even when the values are unchanged.
func TestUpdateSettings_UnrelatedProviderFieldsNotReapplied(t *testing.T) {
	cfg := &config.Config{ZaiAPIKey: "zai_live", ZaiRegion: "global", OpenCodeGoAPIKey: "oc_live"}
	h, s, _ := newReconcileHandler(t, cfg, map[string]bool{})
	if err := s.SetSetting("provider_settings", `{"zai":{"api_key":"zai_stored","region":"cn"},"opencode":{"api_key":"oc_live"}}`); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}

	body := strings.NewReader(`{"provider_settings":{"opencode":{"api_key":"oc_live"}}}`)
	req := httptest.NewRequest(http.MethodPut, "/api/settings", body)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.UpdateSettings(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	if h.config.ZaiRegion != "global" {
		t.Fatalf("unrelated save re-applied zai region from DB: got %q, want global", h.config.ZaiRegion)
	}
	if h.config.ZaiAPIKey != "zai_live" {
		t.Fatalf("unrelated save re-applied zai key from DB: got %q, want zai_live", h.config.ZaiAPIKey)
	}
}

// MEDIUM 2 (race): an OpenCode save concurrent with unlocked reads of an
// unrelated provider's config field must be race-free. Before the fix, the
// save re-applied the zai fields under the config write lock while the reader
// touched them without it.
func TestUpdateSettings_UnrelatedProviderReadIsRaceFree(t *testing.T) {
	cfg := &config.Config{ZaiAPIKey: "zai_live", ZaiRegion: "global", OpenCodeGoAPIKey: "oc_live"}
	h, s, _ := newReconcileHandler(t, cfg, map[string]bool{})
	if err := s.SetSetting("provider_settings", `{"zai":{"api_key":"zai_stored","region":"cn"},"opencode":{"api_key":"oc_live"}}`); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}

	var wg sync.WaitGroup
	stop := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				// Unlocked reads, mirroring the ~190 handler/menubar call sites.
				_ = h.config.ZaiRegion
				_ = h.config.ZaiAPIKey
			}
		}
	}()

	for i := 0; i < 300; i++ {
		body := strings.NewReader(`{"provider_settings":{"opencode":{"api_key":"oc_` + strconv.Itoa(i) + `"}}}`)
		req := httptest.NewRequest(http.MethodPut, "/api/settings", body)
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		h.UpdateSettings(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("save %d status = %d body=%s", i, rr.Code, rr.Body.String())
		}
	}
	close(stop)
	wg.Wait()
}
