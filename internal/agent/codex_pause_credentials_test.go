package agent

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
	"github.com/onllm-dev/onwatch/v2/internal/store"
	"github.com/onllm-dev/onwatch/v2/internal/tracker"
)

// newFingerprintCodexAgent builds an agent whose credential fingerprint is
// controlled by the caller, so a simulated re-authentication can be delivered
// without touching the real ~/.codex/auth.json.
func newFingerprintCodexAgent(t *testing.T, handler http.HandlerFunc) (*CodexAgent, *store.Store, *atomic.Value, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		handler(w, r)
	}))
	t.Cleanup(server.Close)

	st, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	logger := slog.New(slog.DiscardHandler)
	client := api.NewCodexClient("tok", logger, api.WithCodexBaseURL(server.URL))
	tr := tracker.NewCodexTracker(st, logger)
	ag := NewCodexAgent(client, st, tr, time.Minute, logger, nil)

	var fingerprint atomic.Value
	fingerprint.Store("creds-v1")
	ag.SetCredentialFingerprint(func() string {
		v, _ := fingerprint.Load().(string)
		return v
	})
	ag.SetTokenRefresh(func() string { return "tok" })
	return ag, st, &fingerprint, &calls
}

// A re-authentication that rotates the credential file must lift the pause and
// poll again, without a daemon restart (issue #147).
func TestCodexAgent_PausedCredentialChangeResumesPolling(t *testing.T) {
	t.Parallel()
	var healthy atomic.Bool
	ag, st, fingerprint, calls := newFingerprintCodexAgent(t, func(w http.ResponseWriter, r *http.Request) {
		if !healthy.Load() {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"plan_type":"pro","rate_limit":{"primary_window":{"used_percent":25,"reset_at":1766000000,"limit_window_seconds":18000}}}`)
	})

	ctx := context.Background()
	for i := 0; i < maxCodexAuthFailures; i++ {
		ag.poll(ctx)
	}
	if !ag.AuthPaused() {
		t.Fatalf("expected pause after %d auth failures", maxCodexAuthFailures)
	}

	// While paused with an unchanged credential source, the agent must make no
	// network calls at all.
	pausedCalls := calls.Load()
	ag.poll(ctx)
	ag.poll(ctx)
	if got := calls.Load(); got != pausedCalls {
		t.Fatalf("paused polls made network calls: %d -> %d", pausedCalls, got)
	}

	// The user re-authenticates: the credential source changes on disk.
	healthy.Store(true)
	fingerprint.Store("creds-v2")
	ag.poll(ctx)

	if ag.AuthPaused() {
		t.Fatal("expected the pause to lift after the credential source changed")
	}
	if got := calls.Load(); got <= pausedCalls {
		t.Fatalf("expected a poll after re-authentication, calls stayed at %d", got)
	}
	latest, err := st.QueryLatestCodex(store.DefaultCodexAccountID)
	if err != nil {
		t.Fatalf("QueryLatestCodex: %v", err)
	}
	if latest == nil {
		t.Fatal("expected a snapshot after the pause lifted")
	}
}

// A paused agent must not keep calling the credentials-refresh hook (which is
// what drives the OAuth refresh) every tick with a dead credential.
func TestCodexAgent_PausedDoesNotCallCredentialsRefresh(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(server.Close)

	st, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	logger := slog.New(slog.DiscardHandler)
	client := api.NewCodexClient("tok", logger, api.WithCodexBaseURL(server.URL))
	tr := tracker.NewCodexTracker(st, logger)
	ag := NewCodexAgent(client, st, tr, time.Minute, logger, nil)

	var refreshCalls atomic.Int32
	ag.SetCredentialsRefresh(func() *api.CodexCredentials {
		refreshCalls.Add(1)
		// No refresh token: the proactive OAuth path is skipped, so this test
		// never touches the real OpenAI endpoint.
		return &api.CodexCredentials{
			AccessToken: "tok",
			ExpiresIn:   -1 * time.Hour,
			ExpiresAt:   time.Now().Add(-1 * time.Hour),
		}
	})
	ag.SetTokenRefresh(func() string { return "tok" })
	ag.SetCredentialFingerprint(func() string { return "creds-v1" })

	ctx := context.Background()
	for i := 0; i < maxCodexAuthFailures; i++ {
		ag.poll(ctx)
	}
	if !ag.AuthPaused() {
		t.Fatal("expected pause after repeated auth failures")
	}

	pausedRefreshes := refreshCalls.Load()
	for i := 0; i < 5; i++ {
		ag.poll(ctx)
	}
	if got := refreshCalls.Load(); got != pausedRefreshes {
		t.Fatalf("credentials refresh called %d more times while paused", got-pausedRefreshes)
	}
}

// The pause notice must be logged once, not on every tick.
func TestCodexAgent_PauseLoggedOnce(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(server.Close)

	st, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	client := api.NewCodexClient("tok", logger, api.WithCodexBaseURL(server.URL))
	tr := tracker.NewCodexTracker(st, logger)
	ag := NewCodexAgent(client, st, tr, time.Minute, logger, nil)
	ag.SetTokenRefresh(func() string { return "tok" })
	ag.SetCredentialFingerprint(func() string { return "creds-v1" })

	ctx := context.Background()
	for i := 0; i < maxCodexAuthFailures+6; i++ {
		ag.poll(ctx)
	}

	if n := strings.Count(buf.String(), "polling PAUSED"); n != 1 {
		t.Fatalf("pause notice logged %d times, want exactly 1\n%s", n, buf.String())
	}
}

// The fingerprint must change only when the tokens change, so an unrelated
// rewrite (for example SavedAt) does not look like a re-authentication.
func TestCodexCredentialFingerprint(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "auth.json")

	if err := os.WriteFile(path, []byte(`{"tokens":{"access_token":"a1","refresh_token":"r1","id_token":"i1"}}`), 0o600); err != nil {
		t.Fatalf("write auth: %v", err)
	}
	first := codexCredentialFingerprint(path)
	if first == "" {
		t.Fatal("fingerprint of a readable credential file is empty")
	}

	// Same tokens, different bookkeeping: fingerprint stays stable.
	if err := os.WriteFile(path, []byte(`{"tokens":{"access_token":"a1","refresh_token":"r1","id_token":"i1"},"saved_at":"2026-01-01T00:00:00Z"}`), 0o600); err != nil {
		t.Fatalf("rewrite auth: %v", err)
	}
	if got := codexCredentialFingerprint(path); got != first {
		t.Fatalf("fingerprint changed on a non-token rewrite: %q -> %q", first, got)
	}

	// Rotated tokens: fingerprint must change.
	if err := os.WriteFile(path, []byte(`{"tokens":{"access_token":"a2","refresh_token":"r2","id_token":"i2"}}`), 0o600); err != nil {
		t.Fatalf("rotate auth: %v", err)
	}
	if got := codexCredentialFingerprint(path); got == first {
		t.Fatal("fingerprint unchanged after token rotation")
	}

	if got := codexCredentialFingerprint(filepath.Join(dir, "missing.json")); got != "" {
		t.Fatalf("fingerprint of a missing file = %q, want empty", got)
	}
}
