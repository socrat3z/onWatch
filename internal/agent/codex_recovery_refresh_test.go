package agent

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
	"github.com/onllm-dev/onwatch/v2/internal/store"
	"github.com/onllm-dev/onwatch/v2/internal/tracker"
)

// recoveryRefreshAgent drives a real CodexAgent whose usage endpoint and OAuth
// refresh are both stubbed. It never touches a real credential file or the real
// OAuth endpoint.
type recoveryRefreshAgent struct {
	ag    *CodexAgent
	clock *fakeClock
}

// newRecoveryRefreshAgent builds the fixture. acceptAccess is the bearer token
// the usage endpoint accepts (every other token 401s). expiry sets how far out
// the access token's expiry is, which gates the recovery refresh.
//
// A success from the refresh seam is persisted via tokenSave into the mutable
// credential holder, and the fingerprint tracks it, so the fixture models a real
// profile file that rotates on refresh.
func newRecoveryRefreshAgent(t *testing.T, expiry time.Duration, acceptAccess string, refresh codexOAuthRefreshFunc) *recoveryRefreshAgent {
	t.Helper()

	accepted := &atomic.Value{}
	accepted.Store(acceptAccess)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		want, _ := accepted.Load().(string)
		if r.Header.Get("Authorization") != "Bearer "+want {
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
	client := api.NewCodexClient("stale-access", logger, api.WithCodexBaseURL(server.URL))
	tr := tracker.NewCodexTracker(st, logger)
	ag := NewCodexAgent(client, st, tr, time.Minute, logger, nil)

	holder := &atomic.Pointer[api.CodexCredentials]{}
	holder.Store(&api.CodexCredentials{
		AccessToken:  "stale-access",
		RefreshToken: "refresh-token",
		ExpiresAt:    time.Now().Add(expiry),
		ExpiresIn:    expiry,
	})
	ag.SetCredentialsRefresh(func() *api.CodexCredentials { return holder.Load() })
	ag.SetTokenRefresh(func() string { return holder.Load().AccessToken })
	ag.SetCredentialFingerprint(func() string { return codexTokenFingerprint(holder.Load().AccessToken, holder.Load().RefreshToken) })
	ag.oauthRefresh = func(ctx context.Context, refreshToken string) (*api.CodexOAuthTokenResponse, error) {
		resp, err := refresh(ctx, refreshToken)
		if err == nil && resp != nil {
			// The seam's success is only "real" once persisted, matching prod.
			_ = ag.tokenSave(resp.AccessToken, resp.RefreshToken, resp.IDToken, resp.ExpiresIn, refreshToken)
		}
		return resp, err
	}
	// Model the credential file write the recovery refresh performs.
	ag.SetTokenSave(func(accessToken, refreshToken, idToken string, expiresIn int, _ string) error {
		holder.Store(&api.CodexCredentials{
			AccessToken:  accessToken,
			RefreshToken: refreshToken,
			ExpiresAt:    time.Now().Add(time.Duration(expiresIn) * time.Second),
			ExpiresIn:    time.Duration(expiresIn) * time.Second,
		})
		return nil
	})

	clock := &fakeClock{now: time.Now()}
	ag.now = clock.Now

	return &recoveryRefreshAgent{ag: ag, clock: clock}
}

// countRefresh wraps a refresh func with a call counter.
func countRefresh(counter *atomic.Int32, fn codexOAuthRefreshFunc) codexOAuthRefreshFunc {
	return func(ctx context.Context, refreshToken string) (*api.CodexOAuthTokenResponse, error) {
		counter.Add(1)
		return fn(ctx, refreshToken)
	}
}

// forcePaused puts the agent into the paused state with the next recovery
// attempt already due, without going through the usage-failure path (which the
// proactive refresh would otherwise race).
func forcePaused(t *testing.T, ag *CodexAgent) {
	t.Helper()
	ag.pauseAuth("stale-access")
	if !ag.AuthPaused() {
		t.Fatal("forcePaused did not pause the agent")
	}
	ag.authRetryAt = ag.currentTime().Add(-time.Second) // due now
}

// advanceWindow moves past the next scheduled recovery attempt.
func advanceWindow(fx *recoveryRefreshAgent, ag *CodexAgent) {
	if !ag.authRetryAt.IsZero() {
		fx.clock.Advance(ag.authRetryAt.Sub(fx.clock.Now()) + time.Second)
		return
	}
	fx.clock.Advance(codexAuthPausedRetryMaxInterval)
}

// A persistent usage-endpoint 401/403 while the access token is still valid
// must never trigger a recovery refresh: onWatch would otherwise rotate tokens
// (and rewrite auth.json) forever, logging the Codex CLI out (issue #147 review,
// item 1).
func TestCodexAgent_RecoveryRefreshSkippedWhenTokenNotExpiring(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	fx := newRecoveryRefreshAgent(t, 48*time.Hour, "never", countRefresh(&calls, func(ctx context.Context, refreshToken string) (*api.CodexOAuthTokenResponse, error) {
		return &api.CodexOAuthTokenResponse{AccessToken: "fresh-access", RefreshToken: "fresh-refresh", ExpiresIn: 3600}, nil
	}))

	forcePaused(t, fx.ag)
	ctx := context.Background()

	// Many recovery windows pass; the token is valid, so no refresh may fire.
	for i := 0; i < 8; i++ {
		advanceWindow(fx, fx.ag)
		fx.ag.poll(ctx)
	}
	if got := calls.Load(); got != 0 {
		t.Fatalf("recovery refresh fired %d times with a valid access token, want 0", got)
	}
	if !fx.ag.AuthPaused() {
		t.Fatal("expected to stay paused while the usage endpoint rejects the token")
	}
}

// An expiring token with a transient token-endpoint outage gets one refresh per
// recovery window; a later success installs the token, and only a successful
// usage poll lifts the pause (issue #147 review, item 1).
func TestCodexAgent_RecoveryRefreshExpiringTransientThenSuccess(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	fx := newRecoveryRefreshAgent(t, time.Hour, "fresh-access", countRefresh(&calls, func(ctx context.Context, refreshToken string) (*api.CodexOAuthTokenResponse, error) {
		if calls.Load() == 1 {
			return nil, fmt.Errorf("%w: HTTP 503", api.ErrCodexOAuthRefreshFailed)
		}
		return &api.CodexOAuthTokenResponse{AccessToken: "fresh-access", RefreshToken: "fresh-refresh", ExpiresIn: 3600}, nil
	}))

	forcePaused(t, fx.ag)
	ctx := context.Background()

	// Window 1: one refresh, transient 503, stays paused.
	advanceWindow(fx, fx.ag)
	fx.ag.poll(ctx)
	if got := calls.Load(); got != 1 {
		t.Fatalf("refresh calls after window 1 = %d, want 1", got)
	}
	if !fx.ag.AuthPaused() {
		t.Fatal("a transient refresh failure must not lift the pause")
	}

	// Window 2: refresh succeeds, the usage poll then succeeds and lifts the pause.
	advanceWindow(fx, fx.ag)
	fx.ag.poll(ctx)
	if got := calls.Load(); got != 2 {
		t.Fatalf("refresh calls after window 2 = %d, want 2", got)
	}
	if fx.ag.AuthPaused() {
		t.Fatal("a successful recovery refresh followed by a successful poll must lift the pause")
	}
}

// A successful recovery refresh alone must not reset the backoff: if the usage
// endpoint keeps rejecting the (now fresh) token, the pause stays and the
// windows keep escalating rather than restarting at 15m.
func TestCodexAgent_RecoveryRefreshKeepsBackoffUntilPollSucceeds(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	// The usage endpoint never accepts anything, so no poll ever succeeds.
	fx := newRecoveryRefreshAgent(t, time.Hour, "nobody", countRefresh(&calls, func(ctx context.Context, refreshToken string) (*api.CodexOAuthTokenResponse, error) {
		return &api.CodexOAuthTokenResponse{AccessToken: "fresh-access", RefreshToken: "fresh-refresh", ExpiresIn: 3600}, nil
	}))

	forcePaused(t, fx.ag)
	ctx := context.Background()

	advanceWindow(fx, fx.ag)
	fx.ag.poll(ctx)
	if got := calls.Load(); got != 1 {
		t.Fatalf("refresh calls = %d, want 1", got)
	}
	attemptAfterFirst := fx.ag.authRetryCount

	// The next window must be the escalated one, not reset to the base interval.
	advanceWindow(fx, fx.ag)
	fx.ag.poll(ctx)
	if fx.ag.authRetryCount <= attemptAfterFirst {
		t.Fatalf("authRetryCount = %d after a successful refresh, want > %d (backoff must keep escalating)", fx.ag.authRetryCount, attemptAfterFirst)
	}
	if !fx.ag.AuthPaused() {
		t.Fatal("a successful refresh alone must not lift the pause")
	}
}

// A reused / invalid refresh token is terminal: no further refresh may be
// attempted, or onWatch burns the credential the Codex CLI still needs.
func TestCodexAgent_RecoveryRefreshReusedTokenStopsRefreshing(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	fx := newRecoveryRefreshAgent(t, time.Hour, "never", countRefresh(&calls, func(ctx context.Context, refreshToken string) (*api.CodexOAuthTokenResponse, error) {
		return nil, api.ErrCodexRefreshTokenReused
	}))

	forcePaused(t, fx.ag)
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		advanceWindow(fx, fx.ag)
		fx.ag.poll(ctx)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("refresh calls = %d, want exactly 1 (dead refresh token must not be replayed)", got)
	}
	if !fx.ag.AuthPaused() {
		t.Fatal("expected to stay paused with a dead refresh token")
	}
}

// An invalid_grant from the token endpoint is also terminal.
func TestCodexAgent_RecoveryRefreshInvalidGrantStopsRefreshing(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	fx := newRecoveryRefreshAgent(t, time.Hour, "never", countRefresh(&calls, func(ctx context.Context, refreshToken string) (*api.CodexOAuthTokenResponse, error) {
		return nil, api.ErrCodexRefreshTokenInvalid
	}))

	forcePaused(t, fx.ag)
	ctx := context.Background()
	for i := 0; i < 4; i++ {
		advanceWindow(fx, fx.ag)
		fx.ag.poll(ctx)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("refresh calls = %d, want exactly 1 for a terminal invalid_grant", got)
	}
}

// A real re-authentication (a new refresh token) must unblock refresh even
// after a terminal failure, because the consumed key no longer applies.
func TestCodexAgent_RecoveryRefreshUnblockedByNewRefreshToken(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	fx := newRecoveryRefreshAgent(t, time.Hour, "never", countRefresh(&calls, func(ctx context.Context, refreshToken string) (*api.CodexOAuthTokenResponse, error) {
		if refreshToken == "refresh-token-2" {
			return nil, fmt.Errorf("%w: HTTP 503", api.ErrCodexOAuthRefreshFailed)
		}
		return nil, api.ErrCodexRefreshTokenReused
	}))

	forcePaused(t, fx.ag)
	ctx := context.Background()
	advanceWindow(fx, fx.ag)
	fx.ag.poll(ctx)
	if got := calls.Load(); got != 1 {
		t.Fatalf("refresh calls = %d, want 1", got)
	}
	advanceWindow(fx, fx.ag)
	fx.ag.poll(ctx)
	if got := calls.Load(); got != 1 {
		t.Fatalf("terminal token replayed: refresh calls = %d, want 1", got)
	}

	// The user re-authenticates: a brand new refresh token.
	newCreds := &api.CodexCredentials{
		AccessToken:  "stale-access-2",
		RefreshToken: "refresh-token-2",
		ExpiresAt:    time.Now().Add(time.Hour),
		ExpiresIn:    time.Hour,
	}
	fx.ag.SetCredentialsRefresh(func() *api.CodexCredentials { return newCreds })
	fx.ag.SetTokenRefresh(func() string { return newCreds.AccessToken })
	fx.ag.SetCredentialFingerprint(func() string { return "fp-" + newCreds.RefreshToken })

	advanceWindow(fx, fx.ag)
	fx.ag.poll(ctx)
	if got := calls.Load(); got != 2 {
		t.Fatalf("refresh calls after re-auth = %d, want 2 (new refresh token is a new key)", got)
	}
}

// The coordinator admits at most one refresh per credential source per window.
func TestCodexRefreshCoordinator_SharedFingerprintSingleClaim(t *testing.T) {
	t.Parallel()
	coord := newCodexRefreshCoordinator()
	now := time.Now()

	if !coord.claim("shared-fp", now, codexAuthPausedRetryInterval) {
		t.Fatal("first claim should succeed")
	}
	if coord.claim("shared-fp", now.Add(time.Minute), codexAuthPausedRetryInterval) {
		t.Fatal("second claim within the window must be refused")
	}
	if !coord.claim("shared-fp", now.Add(codexAuthPausedRetryInterval+time.Second), codexAuthPausedRetryInterval) {
		t.Fatal("claim after the window should succeed")
	}
	if !coord.claim("other-fp", now, codexAuthPausedRetryInterval) {
		t.Fatal("a different fingerprint is independent")
	}
}

// A successful recovery refresh persists the rotated token and installs it on
// the client, and a subsequent successful poll lifts the pause (the happy path).
func TestCodexAgent_RecoveryRefreshSuccessThenPollResumes(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	fx := newRecoveryRefreshAgent(t, time.Hour, "fresh-access", countRefresh(&calls, func(ctx context.Context, refreshToken string) (*api.CodexOAuthTokenResponse, error) {
		return &api.CodexOAuthTokenResponse{AccessToken: "fresh-access", RefreshToken: "fresh-refresh", ExpiresIn: 3600}, nil
	}))

	forcePaused(t, fx.ag)
	ctx := context.Background()

	advanceWindow(fx, fx.ag)
	fx.ag.poll(ctx)
	if got := calls.Load(); got != 1 {
		t.Fatalf("refresh calls = %d, want 1", got)
	}
	if fx.ag.AuthPaused() {
		t.Fatal("expected the successful poll to lift the pause")
	}
	if fx.ag.authRetryCount != 0 {
		t.Fatalf("authRetryCount = %d, want 0 after a successful poll", fx.ag.authRetryCount)
	}
}
