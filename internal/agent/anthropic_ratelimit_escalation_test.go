package agent

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
)

// escalationLogMsg is the log emitted when a 429 run outlives the threshold.
// Asserted directly because NotificationEngine is a concrete type with
// unexported fields, so a capturing double would need production scaffolding.
// The operational half of "recoverable" - that polling keeps retrying rather
// than pausing - is asserted through agent state instead.
const escalationLogMsg = "OAuth refresh rate limited persistently - alerting"

func newEscalationAgent(t *testing.T) (*AnthropicAgent, *bytes.Buffer) {
	t.Helper()
	logs := &bytes.Buffer{}
	logger := slog.New(slog.NewJSONHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	return &AnthropicAgent{logger: logger}, logs
}

// rateLimitErr builds a 429 error as RefreshAnthropicToken would return one.
func rateLimitErr() error { return api.NewOAuthRateLimitedErrorForTest(0, "rate limit exceeded") }

// stallRun backdates the current 429 run so it reads as having persisted past
// the escalation threshold, without sleeping.
func stallRun(a *AnthropicAgent) {
	a.rateLimitFirstFailAt = time.Now().Add(-2 * rateLimitEscalateAfter)
}

func countEscalations(logs *bytes.Buffer) int {
	return strings.Count(logs.String(), escalationLogMsg)
}

// TestNoteRateLimited_StaysSilentBeforeThreshold pins that a short 429 run is
// handled by backoff alone - transient limits must not alert.
func TestNoteRateLimited_StaysSilentBeforeThreshold(t *testing.T) {
	a, logs := newEscalationAgent(t)

	for i := 0; i < 5; i++ {
		a.noteRateLimited(rateLimitErr())
	}

	if n := countEscalations(logs); n != 0 {
		t.Fatalf("escalations = %d, want 0 before %s elapses", n, rateLimitEscalateAfter)
	}
	if !a.rateLimitPaused {
		t.Fatal("rateLimitPaused = false, want true while backing off")
	}
}

// TestNoteRateLimited_EscalatesOncePastThreshold is the regression for the
// silent multi-day outage: a 429 run that outlives rateLimitEscalateAfter must
// alert, and exactly once rather than on every retry.
func TestNoteRateLimited_EscalatesOncePastThreshold(t *testing.T) {
	a, logs := newEscalationAgent(t)

	a.noteRateLimited(rateLimitErr())
	stallRun(a)
	for i := 0; i < 4; i++ {
		a.noteRateLimited(rateLimitErr())
	}

	if n := countEscalations(logs); n != 1 {
		t.Fatalf("escalations = %d, want exactly 1 per 429 run", n)
	}
	if !a.rateLimitNotified {
		t.Error("rateLimitNotified = false, want true after escalating")
	}
	// The response body is the whole point of carrying it through the error.
	if !strings.Contains(logs.String(), "rate limit exceeded") {
		t.Error("escalation log omits the OAuth response body")
	}
}

// TestNoteRateLimited_KeepsRetryingAfterEscalation pins the chosen policy:
// escalation alerts but never stops the retry schedule, so an IP-level block
// that later clears recovers without human action.
func TestNoteRateLimited_KeepsRetryingAfterEscalation(t *testing.T) {
	a, _ := newEscalationAgent(t)

	a.noteRateLimited(rateLimitErr())
	stallRun(a)
	backoff := a.noteRateLimited(rateLimitErr())

	if a.authPaused {
		t.Fatal("authPaused = true, want false - a 429 must not terminate polling like invalid_grant")
	}
	if backoff <= 0 || backoff > rateLimitMaxBackoff {
		t.Fatalf("backoff = %s, want a positive value within the %s cap", backoff, rateLimitMaxBackoff)
	}
	if a.rateLimitResumeAt.IsZero() {
		t.Fatal("rateLimitResumeAt is zero, want a scheduled retry")
	}
}

// TestNoteRateLimited_HonoursServerRetryAfter keeps the server's own pacing
// authoritative over the computed ladder.
func TestNoteRateLimited_HonoursServerRetryAfter(t *testing.T) {
	a, _ := newEscalationAgent(t)

	backoff := a.noteRateLimited(api.NewOAuthRateLimitedErrorForTest(90*time.Second, ""))

	if backoff != 90*time.Second {
		t.Fatalf("backoff = %s, want the server's 90s Retry-After", backoff)
	}
}

// TestNoteRateLimited_RetryDoesNotResetEscalationClock guards the subtle case
// that would reintroduce the original bug: the backoff-expired retry path also
// decrements the failure count, and if that reset the run timestamp a permanent
// 429 would pace itself out to the cap and never alert.
func TestNoteRateLimited_RetryDoesNotResetEscalationClock(t *testing.T) {
	a, logs := newEscalationAgent(t)

	a.noteRateLimited(rateLimitErr())
	startedAt := a.rateLimitFirstFailAt

	// Mimic the retry path: decrement only, as poll() does when backoff expires.
	if a.rateLimitFailCount > 0 {
		a.rateLimitFailCount--
	}
	if !a.rateLimitFirstFailAt.Equal(startedAt) {
		t.Fatal("retry path moved the run start; the escalation clock must survive retries")
	}

	stallRun(a)
	a.noteRateLimited(rateLimitErr())
	if n := countEscalations(logs); n != 1 {
		t.Fatalf("escalations = %d, want 1 - a persistent block must still alert", n)
	}
}

// TestDecayRateLimitBackoff_EndsRunAndRearmsAlert covers recovery: once healthy
// polling walks the counter to zero the run is over, and a later block may
// alert again rather than being suppressed forever.
func TestDecayRateLimitBackoff_EndsRunAndRearmsAlert(t *testing.T) {
	a, logs := newEscalationAgent(t)

	a.noteRateLimited(rateLimitErr())
	stallRun(a)
	a.noteRateLimited(rateLimitErr())
	if n := countEscalations(logs); n != 1 {
		t.Fatalf("setup: escalations = %d, want 1", n)
	}

	for i := 0; i < 10; i++ {
		a.decayRateLimitBackoff()
	}
	if !a.rateLimitFirstFailAt.IsZero() {
		t.Fatal("rateLimitFirstFailAt not cleared once the failure count reached zero")
	}
	if a.rateLimitNotified {
		t.Fatal("rateLimitNotified still set, so a future outage could never alert")
	}

	a.noteRateLimited(rateLimitErr())
	stallRun(a)
	a.noteRateLimited(rateLimitErr())
	if n := countEscalations(logs); n != 2 {
		t.Fatalf("escalations = %d, want 2 - a new run must re-arm the alert", n)
	}
}

// TestClearRateLimitBackoff_ResetsEscalationState pins that a successful
// refresh wipes the whole run, not just the counter.
func TestClearRateLimitBackoff_ResetsEscalationState(t *testing.T) {
	a, _ := newEscalationAgent(t)

	a.noteRateLimited(rateLimitErr())
	stallRun(a)
	a.noteRateLimited(rateLimitErr())

	a.clearRateLimitBackoff()

	if a.rateLimitFailCount != 0 || a.rateLimitPaused || !a.rateLimitResumeAt.IsZero() {
		t.Error("backoff state not fully cleared")
	}
	if !a.rateLimitFirstFailAt.IsZero() || a.rateLimitNotified {
		t.Error("escalation state not cleared")
	}
}
