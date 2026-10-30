package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
	"github.com/onllm-dev/onwatch/v2/internal/store"
)

// TASK-1 AC#4: the default /api/current view must resolve the provider's
// default account, never the account that happened to poll most recently.
func TestCurrentResolvesTheDefaultAccountForAntigravity(t *testing.T) {
	t.Parallel()

	t.Run("antigravity", func(t *testing.T) {
		s, _ := store.New(":memory:")
		defer s.Close()
		def, err := s.ResolveDefaultProviderAccount("antigravity")
		if err != nil {
			t.Fatal(err)
		}
		other, err := s.CreateOrRestoreProviderAccount("antigravity", "personal")
		if err != nil {
			t.Fatal(err)
		}
		now := time.Now().UTC()
		for _, snap := range []*api.AntigravitySnapshot{
			{AccountID: def.ID, CapturedAt: now.Add(-time.Minute), Email: "default@example.com"},
			{AccountID: other.ID, CapturedAt: now, Email: "other@example.com"},
		} {
			if _, err := s.InsertAntigravitySnapshot(snap); err != nil {
				t.Fatal(err)
			}
		}
		h := NewHandler(s, nil, nil, nil, createTestConfigWithAntigravity())

		if email := currentField(t, h, "/api/current?provider=antigravity", "email"); email != "default@example.com" {
			t.Fatalf("default view email = %v, want the default account's", email)
		}
		if email := currentField(t, h, "/api/current?provider=antigravity&account="+itoa(other.ID), "email"); email != "other@example.com" {
			t.Fatalf("explicit account email = %v, want other@example.com", email)
		}
	})
}

func TestCurrentAntigravityUsesCanonicalAccountAwarePayload(t *testing.T) {
	t.Parallel()

	s, err := store.New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	account, err := s.CreateOrRestoreProviderAccount("antigravity", "work")
	if err != nil {
		t.Fatal(err)
	}
	captured := time.Now().UTC().Truncate(time.Second)
	reset := captured.Add(2 * time.Hour)
	if _, err := s.InsertAntigravitySnapshot(&api.AntigravitySnapshot{
		AccountID: account.ID, CapturedAt: captured, Email: "work@example.com",
		PlanName: "Pro", PromptCredits: 12.5, MonthlyCredits: 100, Source: api.AntigravitySourceIDE,
		Models: []api.AntigravityModelQuota{{
			ModelID: "claude-4-5-sonnet", Label: "Claude Sonnet", RemainingFraction: 0.5,
			RemainingPercent: 50, ResetTime: &reset, TimeUntilReset: 2 * time.Hour,
		}},
	}); err != nil {
		t.Fatal(err)
	}
	h := NewHandler(s, nil, nil, nil, createTestConfigWithAntigravity())
	response := currentResponse(t, h, "/api/current?provider=antigravity&account="+itoa(account.ID))
	for _, field := range []string{"snapshotAt", "promptCredits", "monthlyCredits", "lowestPool", "account"} {
		if _, ok := response[field]; !ok {
			t.Errorf("canonical field %q missing: %+v", field, response)
		}
	}
	quotas, ok := response["quotas"].([]interface{})
	if !ok {
		t.Fatalf("quotas = %+v", response["quotas"])
	}
	foundReset := false
	for _, raw := range quotas {
		quota := raw.(map[string]interface{})
		if quota["modelId"] != "antigravity_claude_gpt" {
			continue
		}
		foundReset = true
		for _, field := range []string{"resetTime", "timeUntilReset", "timeUntilResetSeconds"} {
			if _, ok := quota[field]; !ok {
				t.Errorf("quota field %q missing: %+v", field, quota)
			}
		}
	}
	if !foundReset {
		t.Errorf("Claude quota with reset fields missing: %+v", quotas)
	}
}

func itoa(id int64) string {
	return strconv.FormatInt(id, 10)
}

func currentResponse(t *testing.T, h *Handler, target string) map[string]interface{} {
	t.Helper()
	rr := httptest.NewRecorder()
	h.Current(rr, httptest.NewRequest(http.MethodGet, target, nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("GET %s = %d; body: %s", target, rr.Code, rr.Body.String())
	}
	var response map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("parse %s: %v", target, err)
	}
	return response
}

func currentField(t *testing.T, h *Handler, target, field string) interface{} {
	t.Helper()
	return currentResponse(t, h, target)[field]
}
