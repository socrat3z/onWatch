package agent

import (
	"context"
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

// The access token governs API access, so it is the source of truth for a
// profile's expiry. An id_token that expired days ago must not mark the
// credentials as expiring, or the agent refreshes (and pauses) spuriously even
// though the access token is valid for days (issue #147 follow-up).
func TestCodexCredentialsFromProfile_UsesAccessTokenExpiry(t *testing.T) {
	t.Parallel()

	accessToken := makeCodexIDToken(t, time.Now().Add(7*24*time.Hour), "acct", "user")
	expiredIDToken := makeCodexIDToken(t, time.Now().Add(-10*24*time.Hour), "acct", "user")

	profile := CodexProfile{Name: "temp-plus", AccountID: "acct", UserID: "user"}
	profile.Tokens.AccessToken = accessToken
	profile.Tokens.RefreshToken = "refresh"
	profile.Tokens.IDToken = expiredIDToken

	creds := codexCredentialsFromProfile(profile)
	if creds == nil {
		t.Fatal("codexCredentialsFromProfile returned nil")
	}
	if creds.IsExpiringSoon(codexTokenRefreshThreshold) {
		t.Fatalf("credentials with a valid access token and an expired id_token reported expiring soon: expires_in=%v", creds.ExpiresIn)
	}
	if creds.IsExpired() {
		t.Fatal("credentials with a valid access token reported expired")
	}
}

// The id_token is still the fallback when the access token cannot be parsed.
func TestCodexCredentialsFromProfile_FallsBackToIDTokenExpiry(t *testing.T) {
	t.Parallel()

	idToken := makeCodexIDToken(t, time.Now().Add(7*24*time.Hour), "acct", "user")
	profile := CodexProfile{Name: "fallback", AccountID: "acct", UserID: "user"}
	profile.Tokens.AccessToken = "opaque-not-a-jwt"
	profile.Tokens.RefreshToken = "refresh"
	profile.Tokens.IDToken = idToken

	creds := codexCredentialsFromProfile(profile)
	if creds == nil {
		t.Fatal("codexCredentialsFromProfile returned nil")
	}
	if creds.IsExpiringSoon(codexTokenRefreshThreshold) {
		t.Fatalf("fallback id_token expiry ignored: expires_in=%v", creds.ExpiresIn)
	}
}

// A profile whose access token is still valid must not drive a proactive OAuth
// refresh while the id_token has lapsed. This is the temp-plus failure mode.
func TestCodexAgent_ProfileValidAccessTokenSkipsProactiveRefresh(t *testing.T) {
	t.Parallel()

	var fetchCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fetchCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"plan_type":"plus","rate_limit":{"primary_window":{"used_percent":0,"reset_at":1766000000,"limit_window_seconds":18000}}}`))
	}))
	t.Cleanup(server.Close)

	st, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	logger := slog.New(slog.DiscardHandler)
	client := api.NewCodexClient("access", logger, api.WithCodexBaseURL(server.URL))
	tr := tracker.NewCodexTracker(st, logger)
	ag := NewCodexAgent(client, st, tr, time.Minute, logger, nil)

	accessToken := makeCodexIDToken(t, time.Now().Add(7*24*time.Hour), "acct", "user")
	expiredIDToken := makeCodexIDToken(t, time.Now().Add(-10*24*time.Hour), "acct", "user")
	profile := CodexProfile{Name: "temp-plus", AccountID: "acct", UserID: "user"}
	profile.Tokens.AccessToken = accessToken
	profile.Tokens.RefreshToken = "refresh"
	profile.Tokens.IDToken = expiredIDToken

	creds := codexCredentialsFromProfile(profile)
	ag.SetCredentialsRefresh(func() *api.CodexCredentials { return creds })
	ag.SetTokenRefresh(func() string { return accessToken })

	// The seam stands in for the OAuth endpoint: a valid access token must
	// never reach it.
	var oauthRefreshCalls atomic.Int32
	ag.oauthRefresh = func(ctx context.Context, refreshToken string) (*api.CodexOAuthTokenResponse, error) {
		oauthRefreshCalls.Add(1)
		return nil, api.ErrCodexOAuthRefreshFailed
	}

	ctx := context.Background()
	ag.poll(ctx)

	if got := oauthRefreshCalls.Load(); got != 0 {
		t.Fatalf("OAuth refresh attempted %d times, want 0 for a valid access token", got)
	}
	if ag.AuthPaused() {
		t.Fatal("a valid access token must not pause polling")
	}
	if fetchCalls.Load() == 0 {
		t.Fatal("expected a usage poll with the valid access token")
	}
}
