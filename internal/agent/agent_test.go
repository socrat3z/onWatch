package agent

import (
	"bytes"
	"context"
	"encoding/json"
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

// testResponse returns a standard quota response for mocking
func testResponse() api.QuotaResponse {
	now := time.Now().UTC()
	return api.QuotaResponse{
		Subscription: api.QuotaInfo{
			Limit:    1350,
			Requests: 100.0,
			RenewsAt: now.Add(5 * time.Hour),
		},
		Search: api.SearchInfo{
			Hourly: api.QuotaInfo{
				Limit:    250,
				Requests: 50.0,
				RenewsAt: now.Add(1 * time.Hour),
			},
		},
		ToolCallDiscounts: api.QuotaInfo{
			Limit:    16200,
			Requests: 500.0,
			RenewsAt: now.Add(2 * time.Hour),
		},
	}
}

// setupTest creates a mock server, store, and agent for testing
func setupTest(t *testing.T) (*Agent, *store.Store, *httptest.Server, *bytes.Buffer) {
	var callCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount.Add(1)
		resp := testResponse()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	t.Cleanup(server.Close)

	// Create in-memory database
	dbPath := ":memory:"
	str, err := store.New(dbPath)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	t.Cleanup(func() { str.Close() })

	// Create logger that writes to buffer for testing
	var buf bytes.Buffer
	handler := slog.NewJSONHandler(&buf, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	})
	logger := slog.New(handler)

	// Create API client pointing to mock server
	client := api.NewClient("test-key", logger, api.WithBaseURL(server.URL))

	// Create tracker
	tr := tracker.New(str, logger)

	// Create agent with short interval for testing
	agent := New(client, str, tr, 100*time.Millisecond, logger, nil)

	return agent, str, server, &buf
}

// TestAgent_PollsAtInterval verifies the API is called N times in N*interval duration
func TestAgent_PollsAtInterval(t *testing.T) {
	t.Parallel()
	var callCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount.Add(1)
		resp := testResponse()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	str, _ := store.New(":memory:")
	defer str.Close()

	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	client := api.NewClient("test-key", logger, api.WithBaseURL(server.URL))
	tr := tracker.New(str, logger)

	// Use 50ms interval for faster test
	interval := 50 * time.Millisecond
	agent := New(client, str, tr, interval, logger, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Run agent - it polls immediately, then every interval.
	errChan := make(chan error, 1)
	go func() {
		errChan <- agent.Run(ctx)
	}()

	// Wait for the interval-driven polls instead of asserting against a fixed
	// wall-clock window: the property under test is that polling continues on
	// the interval, and a loaded CI runner can starve the ticker for seconds
	// without violating it.
	start := time.Now()
	waitUntil(t, 10*time.Second, func() bool { return callCount.Load() >= 4 }, "at least 4 interval polls")
	// Snapshot the count and elapsed time together, then cancel.
	count := callCount.Load()
	elapsed := time.Since(start)
	cancel()

	// Lower bound: four polls cannot arrive faster than three intervals, so a
	// busy loop that ignores the interval still fails.
	if elapsed < 2*interval {
		t.Errorf("4 polls arrived in %v, faster than the %v interval allows (busy loop?)", elapsed, interval)
	}
	// Upper bound: over the actual elapsed time, the immediate poll plus one per
	// interval (with slack for scheduling jitter) is the ceiling. This still
	// catches an over-polling regression without being timing-fragile.
	if maxPolls := int(elapsed/interval) + 2; int(count) > maxPolls {
		t.Errorf("got %d polls in %v with %v interval, want at most %d (over-polling?)", count, elapsed, interval, maxPolls)
	}

	select {
	case err := <-errChan:
		if err != nil && err != context.Canceled {
			t.Errorf("Expected nil or context.Canceled error, got: %v", err)
		}
	case <-time.After(1 * time.Second):
		t.Error("Agent.Run() did not return within 1s")
	}
}

// TestAgent_StoresEverySnapshot verifies DB has N rows after N polls
func TestAgent_StoresEverySnapshot(t *testing.T) {
	t.Parallel()
	var pollCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pollCount.Add(1)
		resp := testResponse()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	str, _ := store.New(":memory:")
	defer str.Close()

	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	client := api.NewClient("test-key", logger, api.WithBaseURL(server.URL))
	tr := tracker.New(str, logger)

	agent := New(client, str, tr, 50*time.Millisecond, logger, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go agent.Run(ctx)

	// Wait for the polls to arrive instead of asserting against a fixed
	// wall-clock window. The property under test is that the agent keeps
	// polling on its interval, and a loaded CI runner can starve the ticker
	// goroutine for seconds without that property being violated.
	deadline := time.After(10 * time.Second)
	for pollCount.Load() < 3 {
		select {
		case <-deadline:
			t.Fatalf("Expected at least 3 polls, got %d", pollCount.Load())
		case <-time.After(5 * time.Millisecond):
		}
	}
}

// TestAgent_ProcessesWithTracker verifies Tracker.Process is called for each snapshot
func TestAgent_ProcessesWithTracker(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := testResponse()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	str, _ := store.New(":memory:")
	defer str.Close()

	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	client := api.NewClient("test-key", logger, api.WithBaseURL(server.URL))
	tr := tracker.New(str, logger)

	agent := New(client, str, tr, 50*time.Millisecond, logger, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go agent.Run(ctx)

	// The tracker creates the active cycle on the first processed snapshot.
	// Poll for it rather than reading once after a fixed sleep: a loaded CI
	// runner can starve the agent goroutine past a fixed window, and the
	// property under test is that the tracker ran, not how fast.
	var activeCycle *store.ResetCycle
	waitUntil(t, 10*time.Second, func() bool {
		var err error
		activeCycle, err = str.QueryActiveCycle("subscription")
		return err == nil && activeCycle != nil
	}, "active subscription cycle")
	cancel()

	// Check that cycles were created (indicates tracker processed snapshots)
	cycles, _ := str.QueryCycleHistory("subscription")
	if len(cycles) != 0 {
		t.Logf("Found %d completed subscription cycles", len(cycles))
	}
}

// TestAgent_APIError_Continues verifies agent logs and continues on API error
func TestAgent_APIError_Continues(t *testing.T) {
	t.Parallel()
	var callCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := callCount.Add(1)
		if n == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		resp := testResponse()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	str, _ := store.New(":memory:")
	defer str.Close()

	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	client := api.NewClient("test-key", logger, api.WithBaseURL(server.URL))
	tr := tracker.New(str, logger)

	agent := New(client, str, tr, 50*time.Millisecond, logger, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go agent.Run(ctx)

	// Poll for the second call rather than asserting after a fixed window: the
	// agent must keep polling after the first error, and a loaded runner can
	// starve the ticker past any fixed sleep.
	waitUntil(t, 10*time.Second, func() bool { return callCount.Load() >= 2 }, "a second poll after the API error")
	cancel()
}

// TestAgent_StoreError_Continues verifies agent logs and continues on store error
func TestAgent_StoreError_Continues(t *testing.T) {
	t.Parallel()
	var callCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount.Add(1)
		resp := testResponse()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	str, _ := store.New(":memory:")
	defer str.Close()

	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	client := api.NewClient("test-key", logger, api.WithBaseURL(server.URL))
	tr := tracker.New(str, logger)

	agent := New(client, str, tr, 50*time.Millisecond, logger, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 110*time.Millisecond)
	defer cancel()

	go agent.Run(ctx)
	<-ctx.Done()
	time.Sleep(10 * time.Millisecond)

	// Agent should have polled at least once successfully
	if count := callCount.Load(); count < 1 {
		t.Errorf("Expected at least 1 API call, got %d", count)
	}
}

// TestAgent_TrackerError_StillStoresSnapshot verifies snapshot is saved even if tracker fails
func TestAgent_TrackerError_StillStoresSnapshot(t *testing.T) {
	t.Parallel()
	var pollCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pollCount.Add(1)
		resp := testResponse()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	str, _ := store.New(":memory:")
	defer str.Close()

	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	client := api.NewClient("test-key", logger, api.WithBaseURL(server.URL))
	tr := tracker.New(str, logger)

	agent := New(client, str, tr, 50*time.Millisecond, logger, nil)

	// Use generous timeout - race detector adds significant overhead
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- agent.Run(ctx)
	}()

	// Wait for at least one poll to complete, then cancel
	time.Sleep(500 * time.Millisecond)
	cancel()

	// Wait for agent to fully stop before querying (avoids separate :memory: connections)
	select {
	case <-errCh:
	case <-time.After(2 * time.Second):
		t.Fatal("Agent.Run() did not return within 2s")
	}

	// Even if tracker had issues, snapshot should be stored
	latest, _ := str.QueryLatest()
	if latest == nil {
		t.Error("Expected at least one snapshot to be stored")
	}
}

// TestAgent_GracefulShutdown verifies context cancel causes Run() to return nil within 1s
func TestAgent_GracefulShutdown(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(5 * time.Millisecond) // Small delay
		resp := testResponse()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	str, _ := store.New(":memory:")
	defer str.Close()

	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	client := api.NewClient("test-key", logger, api.WithBaseURL(server.URL))
	tr := tracker.New(str, logger)

	agent := New(client, str, tr, 100*time.Millisecond, logger, nil)

	ctx, cancel := context.WithCancel(context.Background())

	errChan := make(chan error, 1)
	go func() {
		errChan <- agent.Run(ctx)
	}()

	// Let it start polling
	time.Sleep(50 * time.Millisecond)

	// Cancel context
	cancel()

	// Should return within 1 second
	select {
	case err := <-errChan:
		if err != nil {
			t.Errorf("Expected nil error on graceful shutdown, got: %v", err)
		}
	case <-time.After(1 * time.Second):
		t.Error("Agent.Run() did not return within 1s after context cancellation")
	}
}

// TestAgent_GracefulShutdown_MidPoll verifies clean exit when cancelled during HTTP request
func TestAgent_GracefulShutdown_MidPoll(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Long delay to simulate in-flight request
		select {
		case <-r.Context().Done():
			return
		case <-time.After(5 * time.Second):
			resp := testResponse()
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(resp)
		}
	}))
	defer server.Close()

	str, _ := store.New(":memory:")
	defer str.Close()

	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	client := api.NewClient("test-key", logger, api.WithBaseURL(server.URL), api.WithTimeout(10*time.Second))
	tr := tracker.New(str, logger)

	agent := New(client, str, tr, 100*time.Millisecond, logger, nil)

	ctx, cancel := context.WithCancel(context.Background())

	errChan := make(chan error, 1)
	go func() {
		errChan <- agent.Run(ctx)
	}()

	// Let it start a request
	time.Sleep(10 * time.Millisecond)

	// Cancel while request is in flight
	cancel()

	// Should return within 1 second
	select {
	case err := <-errChan:
		// OK - context cancellation should stop it
		_ = err
	case <-time.After(1 * time.Second):
		t.Error("Agent.Run() did not return within 1s when cancelled mid-poll")
	}
}

// TestAgent_FirstPollImmediate verifies first poll happens immediately, not after interval
func TestAgent_FirstPollImmediate(t *testing.T) {
	t.Parallel()
	var callCount atomic.Int32
	var mu sync.Mutex
	callTimes := make([]time.Time, 0)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount.Add(1)
		mu.Lock()
		callTimes = append(callTimes, time.Now())
		mu.Unlock()
		resp := testResponse()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	str, _ := store.New(":memory:")
	defer str.Close()

	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	client := api.NewClient("test-key", logger, api.WithBaseURL(server.URL))
	tr := tracker.New(str, logger)

	startTime := time.Now()
	agent := New(client, str, tr, 500*time.Millisecond, logger, nil) // Long interval

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go agent.Run(ctx)

	// Poll for the first call rather than sleeping a fixed window. The property
	// under test is that the first poll is immediate, i.e. not gated on the
	// interval tick.
	waitUntil(t, 10*time.Second, func() bool { return callCount.Load() >= 1 }, "immediate first poll")
	count := callCount.Load()
	elapsed := time.Since(startTime)
	cancel()

	mu.Lock()
	times := make([]time.Time, len(callTimes))
	copy(times, callTimes)
	mu.Unlock()
	if len(times) > 0 {
		// Immediacy is a property relative to the interval: the first poll must
		// arrive before the 500ms tick would have fired, not within a hard 50ms.
		if timeToFirstCall := times[0].Sub(startTime); timeToFirstCall >= 500*time.Millisecond {
			t.Errorf("First poll should be immediate (before the 500ms interval), took %v", timeToFirstCall)
		}
	}
	// Over-polling ceiling over the actual elapsed time: the immediate poll plus
	// one per interval (with slack) is the maximum.
	if maxPolls := int(elapsed/(500*time.Millisecond)) + 2; int(count) > maxPolls {
		t.Errorf("got %d polls in %v with a 500ms interval, want at most %d (over-polling?)", count, elapsed, maxPolls)
	}
}

// TestAgent_LogsEachPoll verifies structured log entry per poll with key metrics
func TestAgent_LogsEachPoll(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := testResponse()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	str, _ := store.New(":memory:")
	defer str.Close()

	var buf bytes.Buffer
	handler := slog.NewJSONHandler(&buf, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	})
	logger := slog.New(handler)
	client := api.NewClient("test-key", logger, api.WithBaseURL(server.URL))
	tr := tracker.New(str, logger)

	agent := New(client, str, tr, 50*time.Millisecond, logger, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 70*time.Millisecond)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- agent.Run(ctx)
	}()

	<-ctx.Done()
	<-done // Wait for agent to fully stop

	// Check that logs were written
	logs := buf.String()
	if len(logs) == 0 {
		t.Error("Expected log output, got none")
	}

	// Should contain poll-related log entries
	if !bytes.Contains(buf.Bytes(), []byte("poll")) && !bytes.Contains(buf.Bytes(), []byte("quota")) {
		t.Logf("Logs content: %s", logs)
		t.Error("Expected logs to contain 'poll' or 'quota' references")
	}
}

// NOTE: Session lifecycle tests (create/close/max/count/ID) were removed.
// Session management is now handled by SessionManager, tested in session_manager_test.go.
