package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestRefreshAnthropicToken_429CarriesResponseBody pins that a 429 keeps its
// response body. Status alone cannot distinguish Anthropic's own rate limiter
// from an edge/WAF block, and discarding the body is what made a multi-day
// refresh outage unexplainable from logs.
func TestRefreshAnthropicToken_429CarriesResponseBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`<!DOCTYPE html><html><title>Attention Required!</title><body>you have been blocked</body></html>`))
	}))
	defer srv.Close()
	SetOAuthURLForTest(srv.URL)
	defer SetOAuthURLForTest(AnthropicOAuthTokenURL)

	_, err := RefreshAnthropicToken(context.Background(), "stored-refresh-token")

	if !errors.Is(err, ErrOAuthRateLimited) {
		t.Fatalf("err = %v, want ErrOAuthRateLimited", err)
	}
	body := OAuthResponseBody(err)
	if !strings.Contains(body, "you have been blocked") {
		t.Fatalf("OAuthResponseBody = %q, want the edge-block text preserved", body)
	}
	// A body with no Retry-After must still reach the caller, which the old
	// bare-sentinel return could not do.
	if RetryAfter(err) != 0 {
		t.Errorf("RetryAfter = %s, want 0 when the server sent no header", RetryAfter(err))
	}
}

// TestRefreshAnthropicToken_429KeepsRetryAfterAndBody covers both fields
// travelling together.
func TestRefreshAnthropicToken_429KeepsRetryAfterAndBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "120")
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"error":"rate_limit_exceeded"}`))
	}))
	defer srv.Close()
	SetOAuthURLForTest(srv.URL)
	defer SetOAuthURLForTest(AnthropicOAuthTokenURL)

	_, err := RefreshAnthropicToken(context.Background(), "stored-refresh-token")

	if got := RetryAfter(err); got != 2*time.Minute {
		t.Errorf("RetryAfter = %s, want 2m", got)
	}
	if got := OAuthResponseBody(err); !strings.Contains(got, "rate_limit_exceeded") {
		t.Errorf("OAuthResponseBody = %q, want the error code preserved", got)
	}
	// The message must surface both without needing a debugger.
	if msg := err.Error(); !strings.Contains(msg, "rate_limit_exceeded") || !strings.Contains(msg, "2m") {
		t.Errorf("err.Error() = %q, want both the retry hint and the body", msg)
	}
}

// TestOAuthBodySnippet_Bounded keeps a hostile or huge body loggable.
func TestOAuthBodySnippet_Bounded(t *testing.T) {
	if got := oauthBodySnippet([]byte("  \n\t ")); got != "" {
		t.Errorf("blank body snippet = %q, want empty", got)
	}
	if got := oauthBodySnippet([]byte("a\nb\tc")); got != "a b c" {
		t.Errorf("snippet = %q, want newlines collapsed to spaces", got)
	}
	long := oauthBodySnippet([]byte(strings.Repeat("x", 500)))
	if len(long) > 243 {
		t.Errorf("snippet length = %d, want bounded near 240", len(long))
	}
	if !strings.HasSuffix(long, "...") {
		t.Error("truncated snippet should be marked with an ellipsis")
	}
}

// TestAnthropicOAuthUserAgent_NotStale guards the pinned identity. A
// user-agent naming a long-dead CLI build is the kind of signal an edge scores
// against; this fails loudly if it drifts back to the old hardcoded value.
func TestAnthropicOAuthUserAgent_NotStale(t *testing.T) {
	if anthropicOAuthUserAgent == "claude-code/2.1.69" {
		t.Error("user-agent is back to the stale 2.1.69 build")
	}
	if !strings.HasPrefix(anthropicOAuthUserAgent, "claude-code/") {
		t.Errorf("user-agent = %q, want a claude-code/<version> identity", anthropicOAuthUserAgent)
	}
}
