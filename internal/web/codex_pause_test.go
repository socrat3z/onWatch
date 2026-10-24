package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/agent"
	"github.com/onllm-dev/onwatch/v2/internal/api"
	"github.com/onllm-dev/onwatch/v2/internal/store"
)

// fakeCodexPauseReporter reports a fixed set of paused accounts.
type fakeCodexPauseReporter struct {
	paused map[int64]agent.CodexPauseState
}

func (f *fakeCodexPauseReporter) PausedAccounts() map[int64]agent.CodexPauseState {
	return f.paused
}

func TestHandler_CodexCurrent_FlagsPausedAccount(t *testing.T) {
	t.Parallel()
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	resetsAt := time.Now().Add(-6 * time.Hour) // reset already in the past
	if _, err := s.InsertCodexSnapshot(&api.CodexSnapshot{
		CapturedAt: time.Now().Add(-24 * time.Hour),
		AccountID:  DefaultCodexAccountID,
		PlanType:   "plus",
		Quotas: []api.CodexQuota{
			{Name: "seven_day", Utilization: 96, ResetsAt: &resetsAt},
		},
	}); err != nil {
		t.Fatalf("InsertCodexSnapshot: %v", err)
	}

	h := NewHandler(s, nil, nil, nil, createTestConfigWithCodex())
	since := time.Now().Add(-2 * time.Hour).UTC().Truncate(time.Second)
	h.SetCodexPauseReporter(&fakeCodexPauseReporter{paused: map[int64]agent.CodexPauseState{
		DefaultCodexAccountID: {Paused: true, Since: since, Reason: "needs re-auth"},
	}})

	req := httptest.NewRequest(http.MethodGet, "/api/current?provider=codex", nil)
	rr := httptest.NewRecorder()
	h.Current(rr, req)

	var response map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("parse JSON: %v", err)
	}
	if response["paused"] != true {
		t.Errorf("paused = %v, want true", response["paused"])
	}
	if response["authRequired"] != true {
		t.Errorf("authRequired = %v, want true", response["authRequired"])
	}
	if response["status"] != "paused" {
		t.Errorf("status = %v, want paused", response["status"])
	}
	if response["pausedSince"] != since.Format(time.RFC3339) {
		t.Errorf("pausedSince = %v, want %v", response["pausedSince"], since.Format(time.RFC3339))
	}
	if response["isStale"] != true {
		t.Errorf("isStale = %v, want true for a day-old snapshot", response["isStale"])
	}

	quotas, ok := response["quotas"].([]interface{})
	if !ok || len(quotas) == 0 {
		t.Fatalf("expected quotas, got %T", response["quotas"])
	}
	q := quotas[0].(map[string]interface{})
	if secs, _ := q["timeUntilResetSeconds"].(float64); secs < 0 {
		t.Errorf("timeUntilResetSeconds = %v, want clamped to >= 0", q["timeUntilResetSeconds"])
	}
	if q["timeUntilReset"] == "Resetting..." {
		t.Errorf("timeUntilReset = %q, want a non-negative value", q["timeUntilReset"])
	}
	if q["resetElapsed"] != true {
		t.Errorf("resetElapsed = %v, want true for a past reset", q["resetElapsed"])
	}
}

func TestHandler_CodexCurrent_NotPausedHasNoPauseFields(t *testing.T) {
	t.Parallel()
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	resetsAt := time.Now().Add(3 * time.Hour)
	if _, err := s.InsertCodexSnapshot(&api.CodexSnapshot{
		CapturedAt: time.Now().UTC(),
		AccountID:  DefaultCodexAccountID,
		PlanType:   "plus",
		Quotas: []api.CodexQuota{
			{Name: "seven_day", Utilization: 40, ResetsAt: &resetsAt},
		},
	}); err != nil {
		t.Fatalf("InsertCodexSnapshot: %v", err)
	}

	h := NewHandler(s, nil, nil, nil, createTestConfigWithCodex())
	h.SetCodexPauseReporter(&fakeCodexPauseReporter{paused: map[int64]agent.CodexPauseState{}})

	req := httptest.NewRequest(http.MethodGet, "/api/current?provider=codex", nil)
	rr := httptest.NewRecorder()
	h.Current(rr, req)

	var response map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("parse JSON: %v", err)
	}
	if _, ok := response["paused"]; ok {
		t.Errorf("paused field present on an unpaused account: %v", response["paused"])
	}
	if response["isStale"] == true {
		t.Error("a fresh snapshot must not be marked stale")
	}
}

func TestHandler_BuildCodexCurrent_ClampsNegativeCountdown(t *testing.T) {
	t.Parallel()
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	resetsAt := time.Now().Add(-90 * time.Minute)
	if _, err := s.InsertCodexSnapshot(&api.CodexSnapshot{
		CapturedAt: time.Now().UTC(),
		AccountID:  DefaultCodexAccountID,
		PlanType:   "plus",
		Quotas: []api.CodexQuota{
			{Name: "five_hour", Utilization: 10, ResetsAt: &resetsAt},
		},
	}); err != nil {
		t.Fatalf("InsertCodexSnapshot: %v", err)
	}

	h := NewHandler(s, nil, nil, nil, createTestConfigWithCodex())
	result := h.buildCodexCurrent(DefaultCodexAccountID)
	quotas := result["quotas"].([]map[string]interface{})
	if len(quotas) != 1 {
		t.Fatalf("got %d quotas, want 1", len(quotas))
	}
	if secs := quotas[0]["timeUntilResetSeconds"].(int64); secs != 0 {
		t.Errorf("timeUntilResetSeconds = %d, want 0", secs)
	}
	if quotas[0]["timeUntilReset"] == "Resetting..." {
		t.Error("past reset must not render as Resetting...")
	}
}

func TestBuildMenubarSnapshot_CodexPausedStatus(t *testing.T) {
	t.Parallel()
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer s.Close()

	resetsAt := time.Now().Add(-6 * time.Hour)
	if _, err := s.InsertCodexSnapshot(&api.CodexSnapshot{
		CapturedAt: time.Now().Add(-24 * time.Hour),
		AccountID:  DefaultCodexAccountID,
		PlanType:   "plus",
		Quotas: []api.CodexQuota{
			{Name: "seven_day", Utilization: 96, ResetsAt: &resetsAt},
		},
	}); err != nil {
		t.Fatalf("InsertCodexSnapshot: %v", err)
	}

	h := NewHandler(s, nil, nil, nil, createTestConfigWithCodex())
	h.SetCodexPauseReporter(&fakeCodexPauseReporter{paused: map[int64]agent.CodexPauseState{
		DefaultCodexAccountID: {Paused: true, Since: time.Now().Add(-time.Hour)},
	}})

	snapshot, err := h.BuildMenubarSnapshot()
	if err != nil {
		t.Fatalf("BuildMenubarSnapshot: %v", err)
	}
	card := findMenubarProviderCard(t, snapshot, "codex:1")
	// Pause is a warning floor, not a ceiling: at 96% the quota severity wins
	// and the card stays critical, while connection_status signals re-auth.
	if card.Status != "critical" {
		t.Errorf("paused codex card status = %q, want critical", card.Status)
	}
	if card.ConnectionStatus != "paused" {
		t.Errorf("paused codex card connection_status = %q, want paused", card.ConnectionStatus)
	}
}
