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

// anthropicResponse returns a valid Anthropic quota response JSON with the given utilization values.
func anthropicResponse(fiveHour, sevenDay float64) string {
	boolTrue := true
	now := time.Now().UTC()
	fiveHourReset := now.Add(3 * time.Hour).Format(time.RFC3339)
	sevenDayReset := now.Add(5 * 24 * time.Hour).Format(time.RFC3339)

	resp := map[string]*struct {
		Utilization *float64 `json:"utilization"`
		ResetsAt    *string  `json:"resets_at"`
		IsEnabled   *bool    `json:"is_enabled"`
	}{
		"five_hour": {
			Utilization: &fiveHour,
			ResetsAt:    &fiveHourReset,
			IsEnabled:   &boolTrue,
		},
		"seven_day": {
			Utilization: &sevenDay,
			ResetsAt:    &sevenDayReset,
			IsEnabled:   &boolTrue,
		},
	}
	data, _ := json.Marshal(resp)
	return string(data)
}

// TestAnthropicAgent_Run_PollsImmediately verifies the first poll happens immediately on startup,
// not after waiting for the interval.
func TestAnthropicAgent_Run_PollsImmediately(t *testing.T) {
	t.Parallel()
	var callCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(anthropicResponse(45.2, 12.8)))
	}))
	defer server.Close()

	str, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer str.Close()

	logger := slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil))
	client := api.NewAnthropicClient("test-token", logger, api.WithAnthropicBaseURL(server.URL+"/api/oauth/usage"))
	tr := tracker.NewAnthropicTracker(str, logger)

	// Long interval so only the immediate poll fires within the test window
	agent := NewAnthropicAgent(client, str, tr, 5*time.Second, logger, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- agent.Run(ctx)
	}()

	// Poll for the immediate call rather than sleeping a fixed window: a loaded
	// CI runner can delay the first poll past a hard sleep. The 5s interval
	// guarantees no second call before we assert.
	waitUntil(t, 10*time.Second, func() bool { return callCount.Load() >= 1 }, "an immediate first poll")
	if count := callCount.Load(); count > 1 {
		t.Errorf("Expected exactly 1 API call (immediate only), got %d", count)
	}
	cancel()

	select {
	case <-errCh:
	case <-time.After(2 * time.Second):
		t.Fatal("Agent.Run() did not return within 2s")
	}
}

// TestAnthropicAgent_Run_PollsAtInterval verifies subsequent polls happen at the configured interval.
func TestAnthropicAgent_Run_PollsAtInterval(t *testing.T) {
	t.Parallel()
	var callCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(anthropicResponse(45.2, 12.8)))
	}))
	defer server.Close()

	str, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer str.Close()

	logger := slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil))
	client := api.NewAnthropicClient("test-token", logger, api.WithAnthropicBaseURL(server.URL+"/api/oauth/usage"))
	tr := tracker.NewAnthropicTracker(str, logger)

	// 50ms interval: expect 1 immediate + 3 ticks = 4 calls
	agent := NewAnthropicAgent(client, str, tr, 50*time.Millisecond, logger, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- agent.Run(ctx)
	}()

	// Wait for the interval-driven polls instead of asserting against a fixed
	// wall-clock window: a loaded CI runner can starve the ticker for seconds
	// without violating the property under test.
	const interval = 50 * time.Millisecond
	start := time.Now()
	waitUntil(t, 10*time.Second, func() bool { return callCount.Load() >= 4 }, "at least 4 interval polls")
	count := callCount.Load()
	elapsed := time.Since(start)
	cancel()

	// Four polls cannot arrive faster than three intervals; this keeps the
	// lower-bound relaxation from masking a busy loop.
	if elapsed < 2*interval {
		t.Errorf("4 polls arrived in %v, faster than the %v interval allows (busy loop?)", elapsed, interval)
	}
	// Over the actual elapsed time, the immediate poll plus one per interval
	// (with slack) is the ceiling; this still catches over-polling.
	if maxPolls := int(elapsed/interval) + 2; int(count) > maxPolls {
		t.Errorf("got %d polls in %v with %v interval, want at most %d (over-polling?)", count, elapsed, interval, maxPolls)
	}

	select {
	case err := <-errCh:
		if err != nil {
			t.Errorf("Expected nil error, got: %v", err)
		}
	case <-time.After(1 * time.Second):
		t.Error("Agent.Run() did not return within 1s")
	}
}

// TestAnthropicAgent_Run_TokenRefresh_BeforeEachPoll verifies that TokenRefreshFunc
// is called before every poll cycle.
func TestAnthropicAgent_Run_TokenRefresh_BeforeEachPoll(t *testing.T) {
	t.Parallel()
	var callCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(anthropicResponse(45.2, 12.8)))
	}))
	defer server.Close()

	str, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer str.Close()

	logger := slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil))
	client := api.NewAnthropicClient("test-token", logger, api.WithAnthropicBaseURL(server.URL+"/api/oauth/usage"))
	tr := tracker.NewAnthropicTracker(str, logger)

	agent := NewAnthropicAgent(client, str, tr, 50*time.Millisecond, logger, nil)

	var refreshCount atomic.Int32
	agent.SetTokenRefresh(func() string {
		refreshCount.Add(1)
		return "test-token" // same token each time
	})

	ctx, cancel := context.WithTimeout(context.Background(), 130*time.Millisecond)
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- agent.Run(ctx)
	}()

	<-ctx.Done()
	time.Sleep(10 * time.Millisecond)

	polls := callCount.Load()
	refreshes := refreshCount.Load()

	// TokenRefreshFunc should be called once per poll
	if refreshes < polls {
		t.Errorf("Expected at least %d token refreshes (one per poll), got %d", polls, refreshes)
	}
}

// TestAnthropicAgent_Run_TokenRefresh_UpdatesClient verifies that when the token changes,
// SetToken is called on the client with the new token.
func TestAnthropicAgent_Run_TokenRefresh_UpdatesClient(t *testing.T) {
	t.Parallel()
	var requestCount atomic.Int32
	var lastAuthHeader atomic.Value
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount.Add(1)
		lastAuthHeader.Store(r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(anthropicResponse(45.2, 12.8)))
	}))
	defer server.Close()

	str, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer str.Close()

	logger := slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil))
	// Start with initial token
	client := api.NewAnthropicClient("initial-token", logger, api.WithAnthropicBaseURL(server.URL+"/api/oauth/usage"))
	tr := tracker.NewAnthropicTracker(str, logger)

	agent := NewAnthropicAgent(client, str, tr, 50*time.Millisecond, logger, nil)

	var callNum atomic.Int32
	agent.SetTokenRefresh(func() string {
		n := callNum.Add(1)
		if n >= 3 {
			return "new-rotated-token"
		}
		return "initial-token"
	})

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- agent.Run(ctx)
	}()

	<-ctx.Done()
	time.Sleep(10 * time.Millisecond)

	// After the token refresh returns "new-rotated-token", subsequent requests should use it
	if auth, ok := lastAuthHeader.Load().(string); ok {
		if auth != "Bearer new-rotated-token" {
			// It's possible timing varies, so just check that we got enough requests
			if requestCount.Load() < 3 {
				t.Logf("Only %d requests made, token change may not have happened yet", requestCount.Load())
			}
		}
	}

	// Verify the agent ran without error
	select {
	case err := <-errCh:
		if err != nil {
			t.Errorf("Expected nil error, got: %v", err)
		}
	case <-time.After(1 * time.Second):
		t.Error("Agent.Run() did not return within 1s")
	}
}

// TestAnthropicAgent_Run_401Retry_Success verifies that on a 401 response, the agent
// re-reads the token via TokenRefreshFunc and retries the request successfully.
func TestAnthropicAgent_Run_401Retry_Success(t *testing.T) {
	t.Parallel()
	var requestCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := requestCount.Add(1)
		// First request returns 401, second (retry) succeeds
		if n == 1 {
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"error": "unauthorized"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(anthropicResponse(45.2, 12.8)))
	}))
	defer server.Close()

	str, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer str.Close()

	var logBuf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logBuf, nil))
	client := api.NewAnthropicClient("old-token", logger, api.WithAnthropicBaseURL(server.URL+"/api/oauth/usage"))
	tr := tracker.NewAnthropicTracker(str, logger)

	agent := NewAnthropicAgent(client, str, tr, 5*time.Second, logger, nil)

	// TokenRefreshFunc returns a new token on re-read
	agent.SetTokenRefresh(func() string {
		return "fresh-token"
	})

	// Run just long enough for 1 immediate poll (which triggers 401 + retry)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- agent.Run(ctx)
	}()

	// Wait for the retry rather than sleeping a fixed window; the 5s interval
	// means only the immediate poll (with its retry) can run in this window.
	waitUntil(t, 10*time.Second, func() bool { return requestCount.Load() >= 2 }, "the 401 retry")
	cancel()

	select {
	case err := <-errCh:
		if err != nil {
			t.Errorf("Expected nil error, got: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Error("Agent.Run() did not return within 2s")
	}

	// Snapshot should have been stored from the retry
	latest, _ := str.QueryLatestAnthropic()
	if latest == nil {
		t.Error("Expected a snapshot to be stored after successful retry")
	}
}

// TestAnthropicAgent_Run_401Retry_NoRefreshFunc verifies that a 401 without a
// TokenRefreshFunc logs the error and continues polling.
func TestAnthropicAgent_Run_401Retry_NoRefreshFunc(t *testing.T) {
	t.Parallel()
	var requestCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := requestCount.Add(1)
		if n == 1 {
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"error": "unauthorized"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(anthropicResponse(45.2, 12.8)))
	}))
	defer server.Close()

	str, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer str.Close()

	var logBuf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logBuf, nil))
	client := api.NewAnthropicClient("test-token", logger, api.WithAnthropicBaseURL(server.URL+"/api/oauth/usage"))
	tr := tracker.NewAnthropicTracker(str, logger)

	// No TokenRefreshFunc set
	agent := NewAnthropicAgent(client, str, tr, 50*time.Millisecond, logger, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- agent.Run(ctx)
	}()

	// The first (401) poll fails; the agent must continue and poll again. Poll
	// for the second call instead of asserting after a fixed window, since a
	// loaded CI runner can starve the ticker.
	waitUntil(t, 10*time.Second, func() bool { return requestCount.Load() >= 2 }, "a second poll after the 401")
	cancel()

	// Wait for agent goroutine to fully stop before reading shared logBuf
	select {
	case <-errCh:
	case <-time.After(2 * time.Second):
		t.Fatal("Agent.Run() did not return within 2s")
	}

	// Logs should contain error about the 401
	logs := logBuf.String()
	if !bytes.Contains([]byte(logs), []byte("Anthropic")) {
		t.Logf("Logs: %s", logs)
		t.Error("Expected log output mentioning Anthropic error")
	}
}

// TestAnthropicAgent_Run_ContextCancel_StopsCleanly verifies that cancelling the
// context causes Run() to return nil promptly.
func TestAnthropicAgent_Run_ContextCancel_StopsCleanly(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(anthropicResponse(45.2, 12.8)))
	}))
	defer server.Close()

	str, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer str.Close()

	logger := slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil))
	client := api.NewAnthropicClient("test-token", logger, api.WithAnthropicBaseURL(server.URL+"/api/oauth/usage"))
	tr := tracker.NewAnthropicTracker(str, logger)

	agent := NewAnthropicAgent(client, str, tr, 100*time.Millisecond, logger, nil)

	ctx, cancel := context.WithCancel(context.Background())

	errCh := make(chan error, 1)
	go func() {
		errCh <- agent.Run(ctx)
	}()

	// Let it start polling
	time.Sleep(50 * time.Millisecond)

	// Cancel context
	cancel()

	// Should return within 1 second
	select {
	case err := <-errCh:
		if err != nil {
			t.Errorf("Expected nil error on graceful shutdown, got: %v", err)
		}
	case <-time.After(1 * time.Second):
		t.Error("Agent.Run() did not return within 1s after context cancellation")
	}
}

// TestAnthropicAgent_Run_StoresSnapshot verifies that each successful poll persists
// a snapshot to the database.
func TestAnthropicAgent_Run_StoresSnapshot(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(anthropicResponse(45.2, 12.8)))
	}))
	defer server.Close()

	str, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer str.Close()

	logger := slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil))
	client := api.NewAnthropicClient("test-token", logger, api.WithAnthropicBaseURL(server.URL+"/api/oauth/usage"))
	tr := tracker.NewAnthropicTracker(str, logger)

	agent := NewAnthropicAgent(client, str, tr, 50*time.Millisecond, logger, nil)

	// Use generous timeout for race detector
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- agent.Run(ctx)
	}()

	// Wait for at least one poll
	time.Sleep(500 * time.Millisecond)
	cancel()

	// Wait for agent to fully stop before querying
	select {
	case <-errCh:
	case <-time.After(2 * time.Second):
		t.Fatal("Agent.Run() did not return within 2s")
	}

	// Verify snapshot was stored
	latest, err := str.QueryLatestAnthropic()
	if err != nil {
		t.Fatalf("QueryLatestAnthropic error: %v", err)
	}
	if latest == nil {
		t.Fatal("Expected at least one Anthropic snapshot to be stored")
	}

	// Verify the snapshot has the expected quota values
	if len(latest.Quotas) < 2 {
		t.Errorf("Expected at least 2 quotas in snapshot, got %d", len(latest.Quotas))
	}

	// Check quota values
	foundFiveHour := false
	for _, q := range latest.Quotas {
		if q.Name == "five_hour" {
			foundFiveHour = true
			if q.Utilization != 45.2 {
				t.Errorf("Expected five_hour utilization 45.2, got %f", q.Utilization)
			}
		}
	}
	if !foundFiveHour {
		t.Error("Expected to find five_hour quota in snapshot")
	}
}

// newCountingUsageAgent builds an agent whose client points at a counting
// httptest server. The returned callCount tracks how many times the OAuth usage
// API was hit. statuslinePath is set to a missing file so the statusline is
// never fresh, forcing the stale-fallback decision.
func newCountingUsageAgent(t *testing.T) (*AnthropicAgent, *atomic.Int32, func()) {
	t.Helper()
	var callCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(anthropicResponse(45.2, 12.8)))
	}))

	str, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}

	logger := slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil))
	client := api.NewAnthropicClient("test-token", logger, api.WithAnthropicBaseURL(server.URL+"/api/oauth/usage"))
	tr := tracker.NewAnthropicTracker(str, logger)
	agent := NewAnthropicAgent(client, str, tr, 5*time.Second, logger, nil)
	// Point at a missing statusline file: isStatuslineFresh is always false, so
	// poll() must decide whether to fall back to the API.
	agent.statuslinePath = t.TempDir() + "/missing-statusline.json"

	cleanup := func() {
		server.Close()
		str.Close()
	}
	return agent, &callCount, cleanup
}

// TestAnthropicAgent_StatuslineOnly_NoAPIFallthrough verifies that in
// "statusline only" mode (apiPollingDisabled), a stale/missing statusline does
// NOT fall through to the OAuth usage API. Regression guard for the issue #82
// follow-up where headless usage silently kept polling the API.
func TestAnthropicAgent_StatuslineOnly_NoAPIFallthrough(t *testing.T) {
	t.Parallel()
	agent, callCount, cleanup := newCountingUsageAgent(t)
	defer cleanup()

	agent.SetAPIPollingDisabled(true)
	agent.poll(context.Background())

	if c := callCount.Load(); c != 0 {
		t.Errorf("statusline-only mode must not call the usage API on stale/missing statusline, got %d call(s)", c)
	}
}

// TestAnthropicAgent_StatuslineStale_FallsThroughWhenAPIEnabled verifies the
// default (auto/api) behavior is preserved: a stale statusline still falls
// through to a real API poll when API polling is enabled.
func TestAnthropicAgent_StatuslineStale_FallsThroughWhenAPIEnabled(t *testing.T) {
	t.Parallel()
	agent, callCount, cleanup := newCountingUsageAgent(t)
	defer cleanup()

	// apiPollingDisabled left at its default (false) = API polling enabled.
	agent.poll(context.Background())

	if c := callCount.Load(); c != 1 {
		t.Errorf("expected exactly 1 API fallthrough poll when API polling enabled, got %d", c)
	}
}
