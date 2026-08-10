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
// default account for Anthropic and Antigravity alike, never the account that
// happened to poll most recently.
func TestCurrentResolvesTheDefaultAccountForBothProviders(t *testing.T) {
	t.Parallel()

	t.Run("anthropic", func(t *testing.T) {
		s, _ := store.New(":memory:")
		defer s.Close()
		def, err := s.ResolveDefaultProviderAccount("anthropic")
		if err != nil {
			t.Fatal(err)
		}
		other, err := s.CreateOrRestoreProviderAccount("anthropic", "personal")
		if err != nil {
			t.Fatal(err)
		}
		now := time.Now().UTC()
		for _, snap := range []*api.AnthropicSnapshot{
			{AccountID: def.ID, CapturedAt: now.Add(-time.Minute), Quotas: []api.AnthropicQuota{{Name: "five_hour", Utilization: 11}}},
			{AccountID: other.ID, CapturedAt: now, Quotas: []api.AnthropicQuota{{Name: "five_hour", Utilization: 88}}},
		} {
			if _, err := s.InsertAnthropicSnapshot(snap); err != nil {
				t.Fatal(err)
			}
		}
		h := NewHandler(s, nil, nil, nil, createTestConfigWithAnthropic())

		if util := firstQuotaUtilization(t, h, "/api/current?provider=anthropic"); util != 11 {
			t.Fatalf("default view utilization = %v, want the default account's 11", util)
		}
		if util := firstQuotaUtilization(t, h, "/api/current?provider=anthropic&account="+itoa(other.ID)); util != 88 {
			t.Fatalf("explicit account utilization = %v, want 88", util)
		}
	})

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

func firstQuotaUtilization(t *testing.T, h *Handler, target string) float64 {
	t.Helper()
	quotas, ok := currentResponse(t, h, target)["quotas"].([]interface{})
	if !ok || len(quotas) == 0 {
		t.Fatalf("GET %s returned no quotas", target)
	}
	return quotas[0].(map[string]interface{})["utilization"].(float64)
}
