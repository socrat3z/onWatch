package web

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/agent"
	"github.com/onllm-dev/onwatch/v2/internal/api"
	"github.com/onllm-dev/onwatch/v2/internal/config"
	"github.com/onllm-dev/onwatch/v2/internal/store"
	"github.com/onllm-dev/onwatch/v2/internal/tracker"
)

// realisticProviderSettings renders the full payload saveProviderSettings in
// app.js sends: every non-password field is always present (only password
// fields are omitted when unchanged). The values mirror the modal defaults.
func realisticProviderSettings(overrides map[string]map[string]string) map[string]map[string]string {
	base := map[string]map[string]string{
		"codex": {
			"profiles_dir":  "",
			"display_mode":  "usage",
			"pace_mode":     "calendar",
			"auto_start_5h": "off",
			"auto_start_7d": "off",
		},
		"mistral": {
			"enabled":         "true",
			"auth_mode":       "automatic",
			"browser":         "auto",
			"browser_profile": "",
			"show_payg":       "true",
		},
		"zai": {
			"region": "global",
		},
		"ollama": {
			"monthly_limit": "",
			"reset_day":     "",
		},
		"opencode": {
			"workspace_id": "",
		},
		"muse": {
			"model": "",
		},
		"commandcode": {
			"base_url": "",
		},
	}
	for provider, fields := range overrides {
		if base[provider] == nil {
			base[provider] = map[string]string{}
		}
		for k, v := range fields {
			base[provider][k] = v
		}
	}
	return base
}

func settingsBodyJSON(t *testing.T, providerSettings map[string]map[string]string) *strings.Reader {
	t.Helper()
	body, err := json.Marshal(map[string]interface{}{"provider_settings": providerSettings})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return strings.NewReader(string(body))
}

func newReconcileHandler(t *testing.T, cfg *config.Config, running map[string]bool) (*Handler, *store.Store, *mockProviderAgentController) {
	t.Helper()
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	controller := &mockProviderAgentController{running: running}
	h := NewHandler(s, nil, nil, nil, cfg)
	h.SetAgentManager(controller)
	return h, s, controller
}

// TestUpdateSettings_StartsNewlyConfiguredProvider is the core regression: an
// OpenCode key saved from Settings must start its agent without a daemon
// restart (OpenCode's factory builds a fresh agent), instead of waiting for the
// next startup.
func TestUpdateSettings_StartsNewlyConfiguredProvider(t *testing.T) {
	cfg := &config.Config{}
	h, _, controller := newReconcileHandler(t, cfg, map[string]bool{})

	body := strings.NewReader(`{"provider_settings":{"opencode":{"api_key":"oc_sk_saved_from_ui"}}}`)
	req := httptest.NewRequest(http.MethodPut, "/api/settings", body)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	h.UpdateSettings(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 body=%s", rr.Code, rr.Body.String())
	}
	if !containsString(controller.started, "opencode") {
		t.Fatalf("opencode agent not started after key save, started=%v", controller.started)
	}
	if cfg.OpenCodeGoAPIKey != "oc_sk_saved_from_ui" {
		t.Fatalf("live config not updated, OpenCodeGoAPIKey=%q", cfg.OpenCodeGoAPIKey)
	}
}

// A key rotation must restart the running OpenCode agent so the new key is used.
func TestUpdateSettings_RotatedKeyRestartsRunningProvider(t *testing.T) {
	cfg := &config.Config{OpenCodeGoAPIKey: "oc_sk_old"}
	h, s, controller := newReconcileHandler(t, cfg, map[string]bool{"opencode": true})
	if err := s.SetSetting("provider_settings", `{"opencode":{"api_key":"oc_sk_old"}}`); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}

	body := strings.NewReader(`{"provider_settings":{"opencode":{"api_key":"oc_sk_new"}}}`)
	req := httptest.NewRequest(http.MethodPut, "/api/settings", body)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	h.UpdateSettings(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 body=%s", rr.Code, rr.Body.String())
	}
	if !containsString(controller.stopped, "opencode") || !containsString(controller.started, "opencode") {
		t.Fatalf("expected stop+start, stopped=%v started=%v", controller.stopped, controller.started)
	}
}

// A display-only change must not disrupt a running agent, even when the
// payload resends the unchanged credential-bearing fields (as the UI does).
func TestUpdateSettings_DisplayOnlyDoesNotRestart(t *testing.T) {
	cfg := &config.Config{OpenCodeGoAPIKey: "oc_sk_keep", OpenCodeGoWorkspaceID: "wrk_keep"}
	h, s, controller := newReconcileHandler(t, cfg, map[string]bool{"opencode": true})
	if err := s.SetSetting("provider_settings", `{"opencode":{"api_key":"oc_sk_keep","workspace_id":"wrk_keep","display_mode":"usage"}}`); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}

	body := strings.NewReader(`{"provider_settings":{"opencode":{"workspace_id":"wrk_keep","display_mode":"available"}}}`)
	req := httptest.NewRequest(http.MethodPut, "/api/settings", body)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	h.UpdateSettings(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 body=%s", rr.Code, rr.Body.String())
	}
	if containsString(controller.stopped, "opencode") {
		t.Fatalf("display-only save must not stop the agent, stopped=%v", controller.stopped)
	}
	if controller.IsRunning("opencode") != true {
		t.Fatalf("agent should still be running, running=%v", controller.running)
	}
}

// A full, unchanged payload (what the UI actually sends) must not restart a
// running agent, even though it carries every restart-requiring field.
func TestUpdateSettings_UnchangedFullPayloadDoesNotRestart(t *testing.T) {
	stored := `{"opencode":{"api_key":"oc_sk_same","workspace_id":"wrk_same","display_mode":"usage"},` +
		`"mistral":{"enabled":"true","auth_mode":"automatic","browser":"auto","browser_profile":"","show_payg":"true"},` +
		`"codex":{"profiles_dir":"/tmp/profiles","display_mode":"usage","pace_mode":"calendar"},` +
		`"ollama":{"monthly_limit":"60","reset_day":"5"}}`
	cfg := &config.Config{
		OpenCodeGoAPIKey:      "oc_sk_same",
		OpenCodeGoWorkspaceID: "wrk_same",
		MistralEnabled:        true,
		OllamaMonthlyLimit:    60,
	}
	h, s, controller := newReconcileHandler(t, cfg, map[string]bool{"opencode": true, "mistral": true})
	if err := s.SetSetting("provider_settings", stored); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}

	full := realisticProviderSettings(nil)
	payload := map[string]map[string]string{
		"opencode": {"workspace_id": "wrk_same", "display_mode": "usage"},
		"mistral":  full["mistral"],
		"codex":    map[string]string{"profiles_dir": "/tmp/profiles", "display_mode": "usage", "pace_mode": "calendar", "auto_start_5h": "off", "auto_start_7d": "off"},
		"ollama":   map[string]string{"monthly_limit": "60", "reset_day": "5", "display_mode": "usage"},
	}
	req := httptest.NewRequest(http.MethodPut, "/api/settings", settingsBodyJSON(t, payload))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.UpdateSettings(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 body=%s", rr.Code, rr.Body.String())
	}
	if len(controller.stopped) != 0 || len(controller.started) != 0 {
		t.Fatalf("unchanged full payload must not touch agents, stopped=%v started=%v", controller.stopped, controller.started)
	}
}

// Codex always resends profiles_dir; a save that does not change it must not
// restart the (shared-instance) Codex agent, and Codex is not runtime
// restartable anyway.
func TestUpdateSettings_CodexProfilesDirUnchangedDoesNotRestart(t *testing.T) {
	cfg := &config.Config{CodexToken: "codex"}
	h, s, controller := newReconcileHandler(t, cfg, map[string]bool{"codex": true})
	if err := s.SetSetting("provider_settings", `{"codex":{"profiles_dir":"/tmp/profiles"}}`); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}

	body := strings.NewReader(`{"provider_settings":{"codex":{"profiles_dir":"/tmp/profiles","display_mode":"usage","pace_mode":"calendar","auto_start_5h":"off","auto_start_7d":"off"}}}`)
	req := httptest.NewRequest(http.MethodPut, "/api/settings", body)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.UpdateSettings(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 body=%s", rr.Code, rr.Body.String())
	}
	if containsString(controller.stopped, "codex") || containsString(controller.started, "codex") {
		t.Fatalf("codex must not restart, stopped=%v started=%v", controller.stopped, controller.started)
	}
}

// A shared-instance provider (Codex) never restarts at runtime, even when a
// restart-requiring field genuinely changes: its client was built at startup.
func TestUpdateSettings_SharedInstanceProviderNotRestarted(t *testing.T) {
	cfg := &config.Config{CodexToken: "codex"}
	h, s, controller := newReconcileHandler(t, cfg, map[string]bool{"codex": true})
	if err := s.SetSetting("provider_settings", `{"codex":{"profiles_dir":"/tmp/old"}}`); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}

	body := strings.NewReader(`{"provider_settings":{"codex":{"profiles_dir":"/tmp/new"}}}`)
	req := httptest.NewRequest(http.MethodPut, "/api/settings", body)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.UpdateSettings(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 body=%s", rr.Code, rr.Body.String())
	}
	if containsString(controller.stopped, "codex") || containsString(controller.started, "codex") {
		t.Fatalf("shared-instance codex must not restart at runtime, stopped=%v started=%v", controller.stopped, controller.started)
	}
}

// Mistral's show_payg is display-only: flipping it must not restart the agent,
// which would re-run the browser cookie import (and may prompt the keychain).
func TestUpdateSettings_MistralShowPaygDoesNotRestart(t *testing.T) {
	cfg := &config.Config{MistralEnabled: true, MistralAuthMode: "automatic"}
	h, s, controller := newReconcileHandler(t, cfg, map[string]bool{"mistral": true})
	stored := `{"mistral":{"enabled":"true","auth_mode":"automatic","browser":"auto","browser_profile":"","show_payg":"true"}}`
	if err := s.SetSetting("provider_settings", stored); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}

	body := strings.NewReader(`{"provider_settings":{"mistral":{"enabled":"true","auth_mode":"automatic","browser":"auto","browser_profile":"","show_payg":"false"}}}`)
	req := httptest.NewRequest(http.MethodPut, "/api/settings", body)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.UpdateSettings(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 body=%s", rr.Code, rr.Body.String())
	}
	if containsString(controller.stopped, "mistral") || containsString(controller.started, "mistral") {
		t.Fatalf("show_payg toggle must not restart mistral, stopped=%v started=%v", controller.stopped, controller.started)
	}
}

// A real Mistral credential change does restart, because Mistral's factory
// builds a fresh agent and client.
func TestUpdateSettings_MistralCredentialChangeRestarts(t *testing.T) {
	cfg := &config.Config{MistralEnabled: true, MistralAuthMode: "manual"}
	h, s, controller := newReconcileHandler(t, cfg, map[string]bool{"mistral": true})
	stored := `{"mistral":{"enabled":"true","auth_mode":"automatic","browser":"auto","show_payg":"true"}}`
	if err := s.SetSetting("provider_settings", stored); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}

	body := strings.NewReader(`{"provider_settings":{"mistral":{"enabled":"true","auth_mode":"manual","browser":"auto","show_payg":"true"}}}`)
	req := httptest.NewRequest(http.MethodPut, "/api/settings", body)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.UpdateSettings(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 body=%s", rr.Code, rr.Body.String())
	}
	if !containsString(controller.stopped, "mistral") || !containsString(controller.started, "mistral") {
		t.Fatalf("auth_mode change must restart mistral, stopped=%v started=%v", controller.stopped, controller.started)
	}
}

// A provider that loses its config (here the session cookie is gone, leaving
// only the workspace ID) is stopped by the reconcile path even though the
// running agent still holds the old credential. This is the path the old
// misnamed test claimed to cover.
func TestUpdateSettings_StopsProviderThatLosesItsConfig(t *testing.T) {
	cfg := &config.Config{}
	h, s, controller := newReconcileHandler(t, cfg, map[string]bool{"opencode": true})
	// Stored settings carry only the workspace ID (no cookie, no key), so after
	// the merge the provider is enabled but not configured.
	if err := s.SetSetting("provider_settings", `{"opencode":{"workspace_id":"wrk_x"}}`); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}

	body := strings.NewReader(`{"provider_settings":{"opencode":{"display_mode":"usage"}}}`)
	req := httptest.NewRequest(http.MethodPut, "/api/settings", body)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.UpdateSettings(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 body=%s", rr.Code, rr.Body.String())
	}
	if !containsString(controller.stopped, "opencode") {
		t.Fatalf("unconfigured provider must be stopped, stopped=%v", controller.stopped)
	}
}

// An unrelated provider's save must not disturb a healthy agent.
func TestUpdateSettings_UnrelatedSaveLeavesOtherAgentsAlone(t *testing.T) {
	cfg := &config.Config{OpenCodeGoAPIKey: "oc_sk_keep"}
	h, s, controller := newReconcileHandler(t, cfg, map[string]bool{"opencode": true})
	if err := s.SetSetting("provider_settings", `{"opencode":{"api_key":"oc_sk_keep"}}`); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}

	body := strings.NewReader(`{"provider_settings":{"zai":{"api_key":"zai_key"}}}`)
	req := httptest.NewRequest(http.MethodPut, "/api/settings", body)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.UpdateSettings(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 body=%s", rr.Code, rr.Body.String())
	}
	if containsString(controller.stopped, "opencode") {
		t.Fatalf("an unrelated save must not stop opencode, stopped=%v", controller.stopped)
	}
}

// sharedInstanceRunner stands in for an agent whose factory returns one shared
// instance built at startup (every provider except opencode and mistral). It
// counts concurrent Run loops and simulated OAuth refreshes, so a restart that
// ran two copies at once would be caught.
type sharedInstanceRunner struct {
	mu          sync.Mutex
	runCalls    int
	concurrent  int
	maxConc     int
	refreshRuns int
	maxRefresh  int
	started     chan struct{}
}

func (r *sharedInstanceRunner) Run(ctx context.Context) error {
	r.mu.Lock()
	r.runCalls++
	r.concurrent++
	if r.concurrent > r.maxConc {
		r.maxConc = r.concurrent
	}
	r.refreshRuns++
	if r.refreshRuns > r.maxRefresh {
		r.maxRefresh = r.refreshRuns
	}
	r.mu.Unlock()
	select {
	case r.started <- struct{}{}:
	default:
	}

	defer func() {
		r.mu.Lock()
		r.concurrent--
		r.mu.Unlock()
	}()
	<-ctx.Done()
	return nil
}

func (r *sharedInstanceRunner) snapshot() (runCalls, maxConc, maxRefresh int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.runCalls, r.maxConc, r.maxRefresh
}

// TestUpdateSettings_AnthropicNeverDoubleRuns pins the shipping blocker: no
// settings path - including a direct API call carrying anthropic.* fields -
// may start a second Anthropic Run (which would refresh OAuth concurrently).
// Anthropic's factory returns one shared instance, so it must never restart at
// runtime.
func TestUpdateSettings_AnthropicNeverDoubleRuns(t *testing.T) {
	cfg := &config.Config{AnthropicToken: "anth_token"}
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	mgr := agent.NewAgentManager(slog.New(slog.NewTextHandler(io.Discard, nil)))
	runner := &sharedInstanceRunner{started: make(chan struct{}, 1)}
	mgr.RegisterFactory("anthropic", func() (agent.AgentRunner, error) { return runner, nil })

	h := NewHandler(s, nil, nil, nil, cfg)
	h.SetAgentManager(mgr)

	if err := mgr.Start("anthropic"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	select {
	case <-runner.started:
	case <-time.After(2 * time.Second):
		t.Fatal("anthropic Run did not start")
	}
	t.Cleanup(func() { mgr.StopAll() })

	// Several saves carrying every anthropic field the UI can send.
	for i := 0; i < 5; i++ {
		body := strings.NewReader(`{"provider_settings":{"anthropic":{"api_key":"tok_` + strconv.Itoa(i) + `","source":"api","display_mode":"usage","api_poll_cycle_interval":"10","staleness_minutes":"5","cc_detection":"on"}}}`)
		req := httptest.NewRequest(http.MethodPut, "/api/settings", body)
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		h.UpdateSettings(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("save %d status = %d body=%s", i, rr.Code, rr.Body.String())
		}
	}

	runCalls, maxConc, maxRefresh := runner.snapshot()
	if runCalls != 1 {
		t.Fatalf("anthropic Run called %d times, want 1 (no runtime restart)", runCalls)
	}
	if maxConc != 1 {
		t.Fatalf("anthropic ran %d copies concurrently, want 1", maxConc)
	}
	if maxRefresh != 1 {
		t.Fatalf("anthropic refreshed %d times concurrently, want 1", maxRefresh)
	}

	// The full-reconcile path (no changed list) forces a restart for every
	// provider. It must still skip shared-instance providers like Anthropic.
	h.reconcileProviderAgents(nil, nil)
	runCalls, maxConc, maxRefresh = runner.snapshot()
	if runCalls != 1 || maxConc != 1 || maxRefresh != 1 {
		t.Fatalf("full reconcile restarted anthropic: runCalls=%d maxConc=%d maxRefresh=%d", runCalls, maxConc, maxRefresh)
	}
}

// openCodeStubFetcher is a no-op OpenCode fetcher for the save-during-poll test.
type openCodeStubFetcher struct{}

func (openCodeStubFetcher) FetchSnapshot(context.Context, string, string) (*api.OpenCodeSnapshot, error) {
	return &api.OpenCodeSnapshot{CapturedAt: time.Now().UTC()}, nil
}

func (openCodeStubFetcher) FetchUsageSnapshot(context.Context, string) (*api.OpenCodeSnapshot, error) {
	return &api.OpenCodeSnapshot{CapturedAt: time.Now().UTC()}, nil
}

// pollKeyFetcher records the credential each poll used and holds the read open
// briefly, widening the overlap with a concurrent config write.
type pollKeyFetcher struct {
	mu     sync.Mutex
	keys   []string
	seenCh chan struct{}
}

func (f *pollKeyFetcher) FetchSnapshot(context.Context, string, string) (*api.OpenCodeSnapshot, error) {
	return f.record(""), nil
}

func (f *pollKeyFetcher) FetchUsageSnapshot(_ context.Context, apiKey string) (*api.OpenCodeSnapshot, error) {
	return f.record(apiKey), nil
}

func (f *pollKeyFetcher) record(key string) *api.OpenCodeSnapshot {
	f.mu.Lock()
	f.keys = append(f.keys, key)
	f.mu.Unlock()
	if f.seenCh != nil {
		select {
		case f.seenCh <- struct{}{}:
		default:
		}
	}
	return &api.OpenCodeSnapshot{CapturedAt: time.Now().UTC()}
}

// A settings save mutates the shared config through ApplyProviderSettingsFromDB
// while a real OpenCode agent polls. The agent snapshots its credentials at
// build time, so the poll never reads the live config. Run under -race; before
// the snapshot this test raced on cfg.OpenCodeGoAPIKey.
func TestUpdateSettings_SaveDuringPollIsRaceFree(t *testing.T) {
	cfg := &config.Config{OpenCodeGoAPIKey: "oc_sk_start", PollInterval: time.Millisecond}
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	// The key is already stored, so a display-only save does not restart the
	// agent - but the reconcile path still re-applies provider_settings to the
	// live config, writing OpenCodeGoAPIKey while the agent polls.
	if err := s.SetSetting("provider_settings", `{"opencode":{"api_key":"oc_sk_live","display_mode":"usage"}}`); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}

	fetcher := &pollKeyFetcher{seenCh: make(chan struct{}, 1)}
	mgr := agent.NewAgentManager(slog.New(slog.NewTextHandler(io.Discard, nil)))
	mgr.RegisterFactory("opencode", func() (agent.AgentRunner, error) {
		ag := agent.NewOpenCodeAgent(fetcher, s, tracker.NewOpenCodeTracker(s, nil), cfg, time.Millisecond, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
		return ag, nil
	})

	h := NewHandler(s, nil, nil, nil, cfg)
	h.SetAgentManager(mgr)
	if err := mgr.Start("opencode"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { mgr.StopAll() })

	// Wait until the agent has polled at least once, so a save is guaranteed to
	// overlap a live poll.
	select {
	case <-fetcher.seenCh:
	case <-time.After(2 * time.Second):
		t.Fatal("agent did not poll")
	}

	for i := 0; i < 200; i++ {
		body := strings.NewReader(`{"provider_settings":{"opencode":{"display_mode":"available"}}}`)
		req := httptest.NewRequest(http.MethodPut, "/api/settings", body)
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		h.UpdateSettings(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("save %d status = %d body=%s", i, rr.Code, rr.Body.String())
		}
	}
}

// A settings save writes the shared h.config while an agent factory copies it
// (Mistral's factory does `*cfg`) and ReloadProviders replaces it. The write
// paths must be serialized against the factory builds so the race detector is
// quiet. This exercises saves concurrent with Start on a factory that reads cfg.
func TestUpdateSettings_ConfigWritesSerializedWithFactory(t *testing.T) {
	cfg := &config.Config{OpenCodeGoAPIKey: "oc_sk_start", MistralEnabled: true}
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()
	if err := s.SetSetting("provider_settings", `{"opencode":{"api_key":"oc_sk_start"},"mistral":{"enabled":"true"}}`); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}

	// A factory that snapshots the shared config, exactly like the mistral and
	// opencode factories in cmd/onwatch/main.go. Snapshot takes the read lock,
	// so a concurrent save (write lock) cannot race the copy.
	mgr := agent.NewAgentManager(slog.New(slog.NewTextHandler(io.Discard, nil)))
	mgr.RegisterFactory("mistral", func() (agent.AgentRunner, error) {
		snapshot := cfg.Snapshot()
		_ = snapshot.MistralEnabled
		return agentRunnerNoop{}, nil
	})

	h := NewHandler(s, nil, nil, nil, cfg)
	h.SetAgentManager(mgr)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			if err := mgr.Start("mistral"); err != nil {
				continue
			}
			mgr.Stop("mistral")
		}
	}()
	for i := 0; i < 100; i++ {
		body := strings.NewReader(`{"provider_settings":{"mistral":{"enabled":"true","auth_mode":"automatic"}}}`)
		req := httptest.NewRequest(http.MethodPut, "/api/settings", body)
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		h.UpdateSettings(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("save %d status = %d body=%s", i, rr.Code, rr.Body.String())
		}
	}
	wg.Wait()
	mgr.StopAll()
}

type agentRunnerNoop struct{}

func (agentRunnerNoop) Run(ctx context.Context) error {
	<-ctx.Done()
	return nil
}

// An empty provider_settings payload changes nothing, so it must not force a
// restart of the runtime-restartable providers (opencode, mistral).
func TestUpdateSettings_EmptyPayloadDoesNotRestart(t *testing.T) {
	cfg := &config.Config{OpenCodeGoAPIKey: "oc_sk_keep", MistralEnabled: true}
	h, s, controller := newReconcileHandler(t, cfg, map[string]bool{"opencode": true, "mistral": true})
	if err := s.SetSetting("provider_settings", `{"opencode":{"api_key":"oc_sk_keep"},"mistral":{"enabled":"true"}}`); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}

	body := strings.NewReader(`{"provider_settings":{}}`)
	req := httptest.NewRequest(http.MethodPut, "/api/settings", body)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.UpdateSettings(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 body=%s", rr.Code, rr.Body.String())
	}
	if len(controller.stopped) != 0 || len(controller.started) != 0 {
		t.Fatalf("empty payload must not touch agents, stopped=%v started=%v", controller.stopped, controller.started)
	}
}

// An explicit clear (null or a clear_<field> flag) removes the stored credential
// so the live config loses it and the running agent stops. A blank password
// (field absent) must keep the existing "unchanged" behaviour.
func TestUpdateSettings_ClearedKeyStopsAgent(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
	}{
		{"null api_key", `{"provider_settings":{"opencode":{"api_key":null}}}`},
		{"clear flag", `{"provider_settings":{"opencode":{"clear_api_key":true}}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &config.Config{OpenCodeGoAPIKey: "oc_sk_present"}
			h, s, controller := newReconcileHandler(t, cfg, map[string]bool{"opencode": true})
			if err := s.SetSetting("provider_settings", `{"opencode":{"api_key":"oc_sk_present","display_mode":"usage"}}`); err != nil {
				t.Fatalf("SetSetting: %v", err)
			}

			req := httptest.NewRequest(http.MethodPut, "/api/settings", strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			rr := httptest.NewRecorder()
			h.UpdateSettings(rr, req)

			if rr.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200 body=%s", rr.Code, rr.Body.String())
			}
			if cfg.OpenCodeGoAPIKey != "" {
				t.Fatalf("live config still has key %q, want cleared", cfg.OpenCodeGoAPIKey)
			}
			if !containsString(controller.stopped, "opencode") {
				t.Fatalf("clearing the key must stop the agent, stopped=%v", controller.stopped)
			}
		})
	}
}

// A blank password (field absent from the payload) keeps the stored key and
// does not stop the agent - the existing "blank = keep" contract.
func TestUpdateSettings_BlankPasswordKeepsKey(t *testing.T) {
	cfg := &config.Config{OpenCodeGoAPIKey: "oc_sk_present"}
	h, s, controller := newReconcileHandler(t, cfg, map[string]bool{"opencode": true})
	if err := s.SetSetting("provider_settings", `{"opencode":{"api_key":"oc_sk_present"}}`); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}

	body := strings.NewReader(`{"provider_settings":{"opencode":{"display_mode":"usage"}}}`)
	req := httptest.NewRequest(http.MethodPut, "/api/settings", body)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.UpdateSettings(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 body=%s", rr.Code, rr.Body.String())
	}
	if cfg.OpenCodeGoAPIKey != "oc_sk_present" {
		t.Fatalf("blank password must keep the key, got %q", cfg.OpenCodeGoAPIKey)
	}
	if containsString(controller.stopped, "opencode") {
		t.Fatalf("blank password must not stop the agent, stopped=%v", controller.stopped)
	}
}

func TestProviderSettingsNeedRestart(t *testing.T) {
	cases := []struct {
		name   string
		in     map[string]interface{}
		stored map[string]interface{}
		want   map[string]bool
	}{
		{
			name: "display only",
			in:   map[string]interface{}{"opencode": map[string]interface{}{"display_mode": "usage"}},
			want: map[string]bool{},
		},
		{
			name: "changed api key",
			in:   map[string]interface{}{"opencode": map[string]interface{}{"api_key": "new"}},
			want: map[string]bool{"opencode": true},
		},
		{
			name:   "unchanged api key",
			in:     map[string]interface{}{"opencode": map[string]interface{}{"api_key": "same"}},
			stored: map[string]interface{}{"opencode": map[string]interface{}{"api_key": "same"}},
			want:   map[string]bool{},
		},
		{
			name:   "mixed changed field",
			in:     map[string]interface{}{"opencode": map[string]interface{}{"display_mode": "usage", "api_key": "new"}},
			stored: map[string]interface{}{"opencode": map[string]interface{}{"api_key": "old"}},
			want:   map[string]bool{"opencode": true},
		},
		{
			name:   "shared instance provider never restarts",
			in:     map[string]interface{}{"codex": map[string]interface{}{"profiles_dir": "/new"}},
			stored: map[string]interface{}{"codex": map[string]interface{}{"profiles_dir": "/old"}},
			want:   map[string]bool{},
		},
		{
			name:   "empty value is not a change",
			in:     map[string]interface{}{"opencode": map[string]interface{}{"workspace_id": ""}},
			stored: map[string]interface{}{"opencode": map[string]interface{}{"workspace_id": "wrk_1"}},
			want:   map[string]bool{},
		},
		{
			name:   "mistral show_payg only",
			in:     map[string]interface{}{"mistral": map[string]interface{}{"show_payg": "false"}},
			stored: map[string]interface{}{"mistral": map[string]interface{}{"show_payg": "true"}},
			want:   map[string]bool{},
		},
		{
			name:   "mistral auth mode change",
			in:     map[string]interface{}{"mistral": map[string]interface{}{"auth_mode": "manual"}},
			stored: map[string]interface{}{"mistral": map[string]interface{}{"auth_mode": "automatic"}},
			want:   map[string]bool{"mistral": true},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := providerSettingsNeedRestart(tc.in, tc.stored, nil)
			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			for k, v := range tc.want {
				if got[k] != v {
					t.Fatalf("got %v, want %v", got, tc.want)
				}
			}
		})
	}
}

// MEDIUM 2: workspace_id is a plain text field, so the UI always sends "" when
// it is empty. A cookie-mode user whose workspace ID lives in .env and who
// pastes a new cookie must keep the .env workspace ID: an empty non-secret
// field is "unchanged", not a clear. Only an explicit clear_<field> (or null)
// clears.
func TestUpdateSettings_BlankWorkspaceIDKeepsEnvValue(t *testing.T) {
	cfg := &config.Config{OpenCodeGoWorkspaceID: "wrk_from_env", OpenCodeGoAuthCookie: "old_cookie"}
	h, s, controller := newReconcileHandler(t, cfg, map[string]bool{"opencode": true})
	// The workspace ID lives only in .env, so provider_settings has no entry for
	// it. This is the real cookie-mode setup.
	if err := s.SetSetting("provider_settings", `{"opencode":{"display_mode":"usage"}}`); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}

	// Full payload as the UI sends it: workspace_id present but blank, a new
	// cookie typed in.
	body := strings.NewReader(`{"provider_settings":{"opencode":{"workspace_id":"","auth_cookie":"new_cookie","display_mode":"usage"}}}`)
	req := httptest.NewRequest(http.MethodPut, "/api/settings", body)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.UpdateSettings(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 body=%s", rr.Code, rr.Body.String())
	}
	if cfg.OpenCodeGoWorkspaceID != "wrk_from_env" {
		t.Fatalf("blank workspace_id wiped the .env value, got %q", cfg.OpenCodeGoWorkspaceID)
	}
	if cfg.OpenCodeGoAuthCookie != "new_cookie" {
		t.Fatalf("cookie not updated, got %q", cfg.OpenCodeGoAuthCookie)
	}
	// The cookie change legitimately restarts the agent (stop+start), so it must
	// end up running - not left stopped because the workspace ID was wiped.
	if !controller.IsRunning("opencode") {
		t.Fatalf("provider must stay running (cookie mode), running=%v stopped=%v", controller.running, controller.stopped)
	}
}

// An explicit clear_workspace_id removes it even though the field is not secret.
func TestUpdateSettings_ExplicitClearWorkspaceID(t *testing.T) {
	cfg := &config.Config{OpenCodeGoWorkspaceID: "wrk_1", OpenCodeGoAuthCookie: "c"}
	h, s, _ := newReconcileHandler(t, cfg, map[string]bool{"opencode": true})
	if err := s.SetSetting("provider_settings", `{"opencode":{"workspace_id":"wrk_1","auth_cookie":"c"}}`); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}

	body := strings.NewReader(`{"provider_settings":{"opencode":{"clear_workspace_id":true}}}`)
	req := httptest.NewRequest(http.MethodPut, "/api/settings", body)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.UpdateSettings(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 body=%s", rr.Code, rr.Body.String())
	}
	if cfg.OpenCodeGoWorkspaceID != "" {
		t.Fatalf("explicit clear did not remove workspace_id, got %q", cfg.OpenCodeGoWorkspaceID)
	}
}

// Mistral's non-secret fields (browser, browser_profile, auth_mode) must not be
// wiped by a blank value either; only an explicit clear removes them.
func TestUpdateSettings_BlankMistralNonSecretFieldsKept(t *testing.T) {
	cfg := &config.Config{MistralEnabled: true, MistralAuthMode: "automatic", MistralBrowser: "chrome", MistralBrowserProfile: "Profile 1"}
	h, s, _ := newReconcileHandler(t, cfg, map[string]bool{"mistral": true})
	stored := `{"mistral":{"enabled":"true","auth_mode":"automatic","browser":"chrome","browser_profile":"Profile 1"}}`
	if err := s.SetSetting("provider_settings", stored); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}

	// The UI always sends these fields; blank means "use the default", but it
	// must not wipe an explicitly configured profile.
	body := strings.NewReader(`{"provider_settings":{"mistral":{"enabled":"true","auth_mode":"automatic","browser":"auto","browser_profile":"","show_payg":"true"}}}`)
	req := httptest.NewRequest(http.MethodPut, "/api/settings", body)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.UpdateSettings(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 body=%s", rr.Code, rr.Body.String())
	}
	if cfg.MistralBrowser != "auto" {
		t.Fatalf("browser = %q, want auto (a select value is a real choice)", cfg.MistralBrowser)
	}
}

// LOW: detection (keychain reads, the 2s Antigravity probe) must run WITHOUT
// the config write lock, so a slow probe never blocks a settings save. This
// holds detection open and proves a concurrent save can take the lock.
func TestIsProviderConfigured_DetectionDoesNotHoldLock(t *testing.T) {
	cfg := &config.Config{} // no commandcode key, so detection runs
	h, _, _ := newReconcileHandler(t, cfg, map[string]bool{})

	inDetect := make(chan struct{})
	release := make(chan struct{})
	testHookBeforeDetect = func(provider string) {
		if provider != "commandcode" {
			return
		}
		close(inDetect)
		<-release
	}
	t.Cleanup(func() { testHookBeforeDetect = nil })

	done := make(chan bool, 1)
	go func() { done <- h.isProviderConfigured("commandcode") }()
	<-inDetect

	// While detection is in progress, a save must be able to take the write
	// lock. If detection held the read lock, this would block.
	acquired := make(chan struct{})
	go func() {
		cfg.WithWrite(func() {})
		close(acquired)
	}()
	select {
	case <-acquired:
	case <-time.After(2 * time.Second):
		close(release)
		t.Fatal("a save could not take the config lock while detection was running")
	}
	close(release)
	<-done
}

// slowStopRunner ignores cancellation until released, so a Stop times out and
// the key is left stopping.
type slowStopRunner struct {
	release chan struct{}
	runs    *int32
}

func (r *slowStopRunner) Run(ctx context.Context) error {
	atomic.AddInt32(r.runs, 1)
	<-ctx.Done()
	<-r.release
	return nil
}

// MEDIUM 1: toggling polling off then on must leave the provider running, even
// when the off-Stop times out because the old Run is slow to unwind. The
// refused Start is queued and runs once the old Run exits.
func TestToggleProvider_OffThenOnKeepsRunning(t *testing.T) {
	prev := agent.SetStopWaitTimeoutForTest(20 * time.Millisecond)
	t.Cleanup(prev)

	cfg := &config.Config{OpenCodeGoAPIKey: "oc_sk_x"}
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	mgr := agent.NewAgentManager(slog.New(slog.NewTextHandler(io.Discard, nil)))
	release := make(chan struct{})
	var runs int32
	mgr.RegisterFactory("opencode", func() (agent.AgentRunner, error) {
		return &slowStopRunner{release: release, runs: &runs}, nil
	})

	h := NewHandler(s, nil, nil, nil, cfg)
	h.SetAgentManager(mgr)

	toggle := func(polling bool) {
		body := fmt.Sprintf(`{"provider":"opencode","polling":%t}`, polling)
		req := httptest.NewRequest(http.MethodPost, "/api/providers/toggle", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		h.ToggleProvider(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("toggle %t status = %d body=%s", polling, rr.Code, rr.Body.String())
		}
	}

	toggle(true)
	toggle(false) // Stop times out, key left stopping
	toggle(true)  // deferred, not run alongside the old copy
	if got := atomic.LoadInt32(&runs); got != 1 {
		t.Fatalf("runs = %d, want 1 (deferred start must not overlap)", got)
	}

	// The old Run exits; the deferred start must come up.
	close(release)
	deadline := time.Now().Add(2 * time.Second)
	for !mgr.IsRunning("opencode") {
		if time.Now().After(deadline) {
			t.Fatal("provider left stopped after off-then-on")
		}
		time.Sleep(time.Millisecond)
	}
	mgr.StopAll()
}
