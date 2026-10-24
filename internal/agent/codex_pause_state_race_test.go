package agent

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
	"github.com/onllm-dev/onwatch/v2/internal/store"
	"github.com/onllm-dev/onwatch/v2/internal/tracker"
)

// drivePauseResumeCycles makes a real agent repeatedly pause (3 auth failures)
// and resume (credential fingerprint change), so the pause-state writes in
// pauseAuth/resumeAuth overlap the readers.
func drivePauseResumeCycles(ctx context.Context, ag *CodexAgent, fingerprint *atomic.Value, healthy *atomic.Bool, cycles int) {
	for i := 0; i < cycles; i++ {
		// A changed fingerprint lifts the pause and resets the failure budget.
		fingerprint.Store(fmt.Sprintf("creds-%d", i))
		healthy.Store(false)
		for j := 0; j < maxCodexAuthFailures+1; j++ {
			ag.poll(ctx)
		}
		healthy.Store(true)
		ag.poll(ctx)
	}
}

// The web layer reads pause state from handler goroutines (via
// CodexAgentManager.PausedAccounts -> Agent.PauseState) while the poll
// goroutine writes it in pauseAuth/resumeAuth. Run under -race, this test
// fails if those fields are not synchronized.
func TestCodexAgent_PauseStateConcurrentWithPolling(t *testing.T) {
	t.Parallel()

	var healthy atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !healthy.Load() {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"plan_type":"pro","rate_limit":{"primary_window":{"used_percent":25,"reset_at":1766000000,"limit_window_seconds":18000}}}`)
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
	ag.SetTokenRefresh(func() string { return "tok" })

	var fingerprint atomic.Value
	fingerprint.Store("creds-init")
	ag.SetCredentialFingerprint(func() string {
		v, _ := fingerprint.Load().(string)
		return v
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		drivePauseResumeCycles(ctx, ag, &fingerprint, &healthy, 60)
	}()

	// Reader goroutines: mimic web handler calls into the agent. A short sleep
	// between reads keeps them overlapping the poll writer without pinning a
	// core for the whole window - a tight loop starves the interval-based
	// polling tests that run in parallel on the low-core CI runners.
	var reads atomic.Int64
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			deadline := time.Now().Add(3 * time.Second)
			for time.Now().Before(deadline) {
				_ = ag.AuthPaused()
				state := ag.PauseState()
				_ = state.Since
				reads.Add(1)
				time.Sleep(time.Millisecond)
			}
		}()
	}

	wg.Wait()
	if reads.Load() == 0 {
		t.Fatal("no pause-state reads ran")
	}
}

// PausedAccounts fans the per-agent PauseState out to the web layer; it must be
// safe to call concurrently with the agent goroutines.
func TestCodexAgentManager_PausedAccountsConcurrentWithPolling(t *testing.T) {
	// Not parallel: newCodexManagerFixture calls t.Setenv.
	var healthy atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !healthy.Load() {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"plan_type":"pro","rate_limit":{"primary_window":{"used_percent":25,"reset_at":1766000000,"limit_window_seconds":18000}}}`)
	}))
	t.Cleanup(server.Close)

	fx := newCodexManagerFixture(t)
	profile := CodexProfile{Name: "race", AccountID: "acct-race", UserID: "user-race"}
	profile.Tokens.AccessToken = makeCodexIDToken(t, time.Now().Add(24*time.Hour), "acct-race", "user-race")
	profile.Tokens.RefreshToken = "refresh"
	fx.writeProfile(t, profile)

	if err := fx.manager.startAgentForProfile(profile); err != nil {
		t.Fatalf("startAgentForProfile: %v", err)
	}
	inst := fx.instance("race")
	if inst == nil {
		t.Fatal("missing race agent instance")
	}
	// Drive the real poll loop against the fake endpoint.
	inst.Agent.client = api.NewCodexClient("tok", fx.logger, api.WithCodexBaseURL(server.URL))
	inst.Agent.SetTokenRefresh(func() string { return "tok" })
	inst.Agent.SetCredentialsRefresh(func() *api.CodexCredentials { return nil })

	var fingerprint atomic.Value
	fingerprint.Store("creds-init")
	inst.Agent.SetCredentialFingerprint(func() string {
		v, _ := fingerprint.Load().(string)
		return v
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		drivePauseResumeCycles(ctx, inst.Agent, &fingerprint, &healthy, 60)
	}()
	for r := 0; r < 3; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			deadline := time.Now().Add(3 * time.Second)
			for time.Now().Before(deadline) {
				_ = fx.manager.PausedAccounts()
				time.Sleep(time.Millisecond)
			}
		}()
	}
	wg.Wait()
}
