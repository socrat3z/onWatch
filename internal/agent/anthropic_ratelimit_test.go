package agent

import (
	"bytes"
	"context"
	"encoding/json"
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

// TestAnthropicAgent_RateLimitBackoff_OAuthEndpoint429 verifies that when the OAuth
// refresh endpoint returns 429 during a proactive refresh, the agent enters
// backoff and stops retrying immediately.
func TestAnthropicAgent_RateLimitBackoff_OAuthEndpoint429(t *testing.T) {
	// The usage API always returns 429. That no longer triggers a rotation -
	// the near-expiry credentials below drive the proactive refresh instead.
	var apiCalls atomic.Int32
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiCalls.Add(1)
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"error":"rate_limited"}`))
	}))
	defer apiServer.Close()

	// OAuth server also returns 429
	var oauthCalls atomic.Int32
	oauthServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		oauthCalls.Add(1)
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"error":"rate_limit_exceeded"}`))
	}))
	defer oauthServer.Close()

	// Override OAuth URL
	api.SetOAuthURLForTest(oauthServer.URL)
	defer api.SetOAuthURLForTest(api.AnthropicOAuthTokenURL)

	str, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer str.Close()

	logger := slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil))
	client := api.NewAnthropicClient("test-token", logger, api.WithAnthropicBaseURL(apiServer.URL+"/api/oauth/usage"))
	tr := tracker.NewAnthropicTracker(str, logger)

	agent := NewAnthropicAgent(client, str, tr, 30*time.Millisecond, logger, nil)
	agent.isClaudeCodeRunning = func() bool { return false }

	// Provide credentials refresh function
	agent.SetCredentialsRefresh(func() *api.AnthropicCredentials {
		return &api.AnthropicCredentials{
			AccessToken:  "test-token",
			RefreshToken: "test-refresh-token",
			ExpiresIn:    time.Minute,
			ExpiresAt:    time.Now().Add(time.Minute),
		}
	})

	// Run for long enough to have multiple poll cycles
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- agent.Run(ctx)
	}()

	<-ctx.Done()
	select {
	case <-errCh:
	case <-time.After(2 * time.Second):
		t.Fatal("Agent.Run() did not return within 2s")
	}

	// The first poll proactively refreshes the near-expiry token and gets an
	// OAuth 429. After that the agent is in backoff and must not call OAuth
	// again on subsequent polls.
	oauthCount := oauthCalls.Load()
	if oauthCount != 1 {
		t.Errorf("Expected exactly 1 OAuth refresh attempt (then backoff), got %d", oauthCount)
	}

	// API should be called multiple times (each poll hits 429)
	apiCount := apiCalls.Load()
	if apiCount < 2 {
		t.Errorf("Expected at least 2 API calls, got %d", apiCount)
	}

	// Verify agent is in backoff state
	if !agent.rateLimitPaused {
		t.Error("Expected agent to be in rate limit backoff")
	}
	if agent.rateLimitFailCount != 1 {
		t.Errorf("Expected rateLimitFailCount=1, got %d", agent.rateLimitFailCount)
	}
}

// TestAnthropicAgent_RateLimitBackoff_ResetsOnSuccess verifies that a successful
// proactive OAuth refresh resets the backoff state.
func TestAnthropicAgent_RateLimitBackoff_ResetsOnSuccess(t *testing.T) {
	var apiCallCount atomic.Int32
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiCallCount.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(anthropicResponse(45.2, 12.8)))
	}))
	defer apiServer.Close()

	// OAuth server: returns new tokens successfully
	oauthServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]interface{}{
			"token_type":    "bearer",
			"access_token":  "new-access-token",
			"refresh_token": "new-refresh-token",
			"expires_in":    3600,
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer oauthServer.Close()

	api.SetOAuthURLForTest(oauthServer.URL)
	defer api.SetOAuthURLForTest(api.AnthropicOAuthTokenURL)

	str, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer str.Close()

	logger := slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil))
	client := api.NewAnthropicClient("test-token", logger, api.WithAnthropicBaseURL(apiServer.URL+"/api/oauth/usage"))
	tr := tracker.NewAnthropicTracker(str, logger)

	agent := NewAnthropicAgent(client, str, tr, 5*time.Second, logger, nil)
	agent.isClaudeCodeRunning = func() bool { return false }

	// Pre-set some backoff state to verify it gets cleared
	agent.rateLimitFailCount = 3
	agent.rateLimitPaused = false // not paused so refresh attempt proceeds

	agent.SetCredentialsRefresh(func() *api.AnthropicCredentials {
		return &api.AnthropicCredentials{
			AccessToken:  "test-token",
			RefreshToken: "test-refresh-token",
			ExpiresIn:    time.Minute,
			ExpiresAt:    time.Now().Add(time.Minute),
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- agent.Run(ctx)
	}()

	<-ctx.Done()
	select {
	case <-errCh:
	case <-time.After(2 * time.Second):
		t.Fatal("Agent.Run() did not return within 2s")
	}

	// Backoff should be fully reset after successful refresh
	if agent.rateLimitFailCount != 0 {
		t.Errorf("Expected rateLimitFailCount=0 after success, got %d", agent.rateLimitFailCount)
	}
	if agent.rateLimitPaused {
		t.Error("Expected rateLimitPaused=false after success")
	}
}

// TestAnthropicAgent_RateLimitBackoff_ResetsOnCredentialChange verifies that
// new credentials detected on disk reset the rate limit backoff.
func TestAnthropicAgent_RateLimitBackoff_ResetsOnCredentialChange(t *testing.T) {
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(anthropicResponse(45.2, 12.8)))
	}))
	defer apiServer.Close()

	str, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer str.Close()

	logger := slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil))
	client := api.NewAnthropicClient("old-token", logger, api.WithAnthropicBaseURL(apiServer.URL+"/api/oauth/usage"))
	tr := tracker.NewAnthropicTracker(str, logger)

	agent := NewAnthropicAgent(client, str, tr, 50*time.Millisecond, logger, nil)
	agent.isClaudeCodeRunning = func() bool { return false }

	// Simulate being in rate limit backoff
	agent.rateLimitPaused = true
	agent.rateLimitFailCount = 3
	agent.rateLimitResumeAt = time.Now().Add(1 * time.Hour)
	agent.lastToken = "old-token"

	// Token refresh returns a new token - should reset backoff
	agent.SetTokenRefresh(func() string {
		return "new-token-from-disk"
	})

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- agent.Run(ctx)
	}()

	<-ctx.Done()
	select {
	case <-errCh:
	case <-time.After(2 * time.Second):
		t.Fatal("Agent.Run() did not return within 2s")
	}

	// Backoff should be reset because credentials changed
	if agent.rateLimitPaused {
		t.Error("Expected rateLimitPaused=false after credential change")
	}
	if agent.rateLimitFailCount != 0 {
		t.Errorf("Expected rateLimitFailCount=0, got %d", agent.rateLimitFailCount)
	}
}

// TestAnthropicAgent_InvalidGrant_PausesPolling verifies that an invalid_grant
// error from a proactive OAuth refresh pauses polling like a terminal auth error.
func TestAnthropicAgent_InvalidGrant_PausesPolling(t *testing.T) {
	var apiCalls atomic.Int32
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiCalls.Add(1)
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"error":"rate_limited"}`))
	}))
	defer apiServer.Close()

	// OAuth returns invalid_grant
	oauthServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":"invalid_grant","error_description":"Token revoked"}`))
	}))
	defer oauthServer.Close()

	api.SetOAuthURLForTest(oauthServer.URL)
	defer api.SetOAuthURLForTest(api.AnthropicOAuthTokenURL)

	str, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer str.Close()

	logger := slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil))
	client := api.NewAnthropicClient("test-token", logger, api.WithAnthropicBaseURL(apiServer.URL+"/api/oauth/usage"))
	tr := tracker.NewAnthropicTracker(str, logger)

	agent := NewAnthropicAgent(client, str, tr, 30*time.Millisecond, logger, nil)
	agent.isClaudeCodeRunning = func() bool { return false }
	agent.SetCredentialsRefresh(func() *api.AnthropicCredentials {
		return &api.AnthropicCredentials{
			AccessToken:  "test-token",
			RefreshToken: "test-refresh-token",
			ExpiresIn:    time.Minute,
			ExpiresAt:    time.Now().Add(time.Minute),
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- agent.Run(ctx)
	}()

	<-ctx.Done()
	select {
	case <-errCh:
	case <-time.After(2 * time.Second):
		t.Fatal("Agent.Run() did not return within 2s")
	}

	// Agent should be in auth-paused state (terminal error)
	if !agent.authPaused {
		t.Error("Expected authPaused=true after invalid_grant")
	}

	// The proactive refresh runs before FetchQuotas, so the first poll pauses
	// the agent and the usage API is never reached.
	if totalAPI := apiCalls.Load(); totalAPI != 0 {
		t.Errorf("Expected 0 API calls after invalid_grant paused polling, got %d", totalAPI)
	}
}

// TestAnthropicAgent_UsageRateLimit_PreservesCredentials verifies the
// post-bypass contract for a usage-API 429: it never exchanges the refresh
// token. Rotating a one-time token to dodge a rate limit races every other
// Claude Code process on the account and can force a global re-login.
func TestAnthropicAgent_UsageRateLimit_PreservesCredentials(t *testing.T) {
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"error":"rate_limited"}`))
	}))
	defer apiServer.Close()

	var oauthCalls atomic.Int32
	oauthServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		oauthCalls.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer oauthServer.Close()

	api.SetOAuthURLForTest(oauthServer.URL)
	defer api.SetOAuthURLForTest(api.AnthropicOAuthTokenURL)

	str, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer str.Close()

	logger := slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil))
	client := api.NewAnthropicClient("test-token", logger, api.WithAnthropicBaseURL(apiServer.URL+"/api/oauth/usage"))
	tr := tracker.NewAnthropicTracker(str, logger)

	agent := NewAnthropicAgent(client, str, tr, 50*time.Millisecond, logger, nil)
	agent.isClaudeCodeRunning = func() bool { return false }

	// Elevated from an earlier OAuth failure, and no longer paused.
	agent.rateLimitFailCount = 5

	var rotations atomic.Int32
	agent.SetCredentialsRefresh(func() *api.AnthropicCredentials {
		return &api.AnthropicCredentials{
			AccessToken:  "test-token",
			RefreshToken: "test-refresh-token",
			ExpiresIn:    time.Hour,
			ExpiresAt:    time.Now().Add(time.Hour),
		}
	})
	agent.SetCredentialsWriter(func(access, refresh string, expires int) error {
		rotations.Add(1)
		return nil
	})

	ctx := context.Background()
	agent.poll(ctx)

	if got := oauthCalls.Load(); got != 0 {
		t.Errorf("OAuth refresh attempts = %d, want 0 (a usage 429 must not rotate credentials)", got)
	}
	if got := rotations.Load(); got != 0 {
		t.Errorf("credential writes = %d, want 0", got)
	}
	if agent.rateLimitFailCount != 5 {
		t.Errorf("rateLimitFailCount = %d, want 5 (a usage 429 must not touch OAuth backoff)", agent.rateLimitFailCount)
	}
	if agent.rateLimitPaused {
		t.Error("rateLimitPaused = true, want false")
	}
}

// TestAnthropicAgent_RateLimitBackoff_DecaysOnBackoffExpiry verifies that once
// an OAuth backoff window elapses, the next refresh attempt decays the failure
// count rather than escalating from where it left off.
func TestAnthropicAgent_RateLimitBackoff_DecaysOnBackoffExpiry(t *testing.T) {
	oauthServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"error":"rate_limit_exceeded"}`))
	}))
	defer oauthServer.Close()

	api.SetOAuthURLForTest(oauthServer.URL)
	defer api.SetOAuthURLForTest(api.AnthropicOAuthTokenURL)

	str, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer str.Close()

	logger := slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil))
	client := api.NewAnthropicClient("test-token", logger, api.WithAnthropicBaseURL("http://127.0.0.1:1/api/oauth/usage"))
	tr := tracker.NewAnthropicTracker(str, logger)

	agent := NewAnthropicAgent(client, str, tr, 50*time.Millisecond, logger, nil)
	agent.isClaudeCodeRunning = func() bool { return false }

	// Prior backoff at fail_count=5, window already elapsed.
	agent.rateLimitFailCount = 5
	agent.rateLimitPaused = true
	agent.rateLimitResumeAt = time.Now().Add(-time.Second)

	agent.SetCredentialsRefresh(func() *api.AnthropicCredentials {
		return &api.AnthropicCredentials{
			AccessToken:  "test-token",
			RefreshToken: "test-refresh-token",
			ExpiresIn:    time.Minute,
			ExpiresAt:    time.Now().Add(time.Minute),
		}
	})

	agent.poll(context.Background())

	// Decayed to 4 on expiry, then re-incremented to 5 by the fresh OAuth 429.
	// NOT 6 - the backoff level must hold rather than escalate forever.
	if agent.rateLimitFailCount != 5 {
		t.Errorf("rateLimitFailCount = %d, want 5 (decayed then re-incremented, not escalated to 6)", agent.rateLimitFailCount)
	}
}

// TestAnthropicAgent_RateLimitBackoff_DecaysOnSuccessfulPoll verifies that successful
// polls gradually decay the rateLimitFailCount even when no 429 is encountered.
func TestAnthropicAgent_RateLimitBackoff_DecaysOnSuccessfulPoll(t *testing.T) {
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(anthropicResponse(25.0, 10.0)))
	}))
	defer apiServer.Close()

	str, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer str.Close()

	logger := slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil))
	client := api.NewAnthropicClient("test-token", logger, api.WithAnthropicBaseURL(apiServer.URL+"/api/oauth/usage"))
	tr := tracker.NewAnthropicTracker(str, logger)

	agent := NewAnthropicAgent(client, str, tr, 50*time.Millisecond, logger, nil)

	// Simulate prior backoff state (not currently paused, but failCount is elevated)
	agent.rateLimitFailCount = 3
	agent.lastToken = "test-token"

	ctx := context.Background()
	agent.poll(ctx)

	if agent.rateLimitFailCount != 2 {
		t.Errorf("rateLimitFailCount = %d, want 2 (decayed by 1 after success)", agent.rateLimitFailCount)
	}

	agent.poll(ctx)
	if agent.rateLimitFailCount != 1 {
		t.Errorf("rateLimitFailCount = %d, want 1 after second success", agent.rateLimitFailCount)
	}

	agent.poll(ctx)
	if agent.rateLimitFailCount != 0 {
		t.Errorf("rateLimitFailCount = %d, want 0 after third success", agent.rateLimitFailCount)
	}

	// Should not go below 0
	agent.poll(ctx)
	if agent.rateLimitFailCount != 0 {
		t.Errorf("rateLimitFailCount = %d, want 0 (floor)", agent.rateLimitFailCount)
	}
}

// TestRateLimitBackoff_Calculation verifies the exponential backoff formula.
func TestRateLimitBackoff_Calculation(t *testing.T) {
	tests := []struct {
		failCount int
		want      time.Duration
	}{
		{0, rateLimitBaseBackoff},      // 5m
		{1, rateLimitBaseBackoff},      // 5m * 2^0 = 5m
		{2, rateLimitBaseBackoff * 2},  // 5m * 2^1 = 10m
		{3, rateLimitBaseBackoff * 4},  // 5m * 2^2 = 20m
		{4, rateLimitBaseBackoff * 8},  // 5m * 2^3 = 40m
		{5, rateLimitBaseBackoff * 16}, // 5m * 2^4 = 80m
		{10, rateLimitMaxBackoff},      // capped at 6h
		{20, rateLimitMaxBackoff},      // still capped
	}
	for _, tt := range tests {
		got := rateLimitBackoff(tt.failCount)
		if got != tt.want {
			t.Errorf("rateLimitBackoff(%d) = %v, want %v", tt.failCount, got, tt.want)
		}
	}
}
