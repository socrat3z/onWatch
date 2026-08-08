package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
	"github.com/onllm-dev/onwatch/v2/internal/config"
	"github.com/onllm-dev/onwatch/v2/internal/store"
)

func TestAppJSDetectsAndRendersDeepSeekBalance(t *testing.T) {
	t.Parallel()

	appJS := readStaticAppJS(t)
	for _, required := range []string{
		"document.getElementById('quota-grid-deepseek')",
		"provider === 'deepseek'",
		"payload.balance",
		"total_balance",
		"granted_balance",
		"topped_up_balance",
	} {
		if !strings.Contains(appJS, required) {
			t.Fatalf("app.js does not contain DeepSeek balance integration %q", required)
		}
	}
}

func TestOpenRouterCurrentAPIExposesAccountBalanceSeparately(t *testing.T) {
	t.Parallel()

	s, err := store.New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })

	keyRemaining, totalCredits, accountUsage, balance := 7.5, 100.5, 25.75, 74.75
	_, err = s.InsertOpenRouterSnapshot(&api.OpenRouterSnapshot{
		CapturedAt:     time.Now().UTC(),
		Usage:          12.5,
		LimitRemaining: &keyRemaining,
		AccountCredits: &totalCredits,
		AccountUsage:   &accountUsage,
		AccountBalance: &balance,
	})
	if err != nil {
		t.Fatal(err)
	}

	h := NewHandler(s, nil, nil, nil, &config.Config{OpenRouterAPIKey: "configured"})
	recorder := httptest.NewRecorder()
	h.Current(recorder, httptest.NewRequest(http.MethodGet, "/api/current?provider=openrouter", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, response = %s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Credits struct {
			Balance           *float64 `json:"balance"`
			KeyLimitRemaining *float64 `json:"keyLimitRemaining"`
			BalanceAvailable  bool     `json:"balanceAvailable"`
		} `json:"credits"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Credits.Balance == nil || *response.Credits.Balance != balance {
		t.Fatalf("balance = %v, want %v", response.Credits.Balance, balance)
	}
	if response.Credits.KeyLimitRemaining == nil || *response.Credits.KeyLimitRemaining != keyRemaining {
		t.Fatalf("key limit remaining = %v, want %v", response.Credits.KeyLimitRemaining, keyRemaining)
	}
	if !response.Credits.BalanceAvailable {
		t.Fatal("balanceAvailable = false, want true")
	}
}

func TestAppJSSeparatesOpenRouterBalanceFromKeyLimit(t *testing.T) {
	t.Parallel()

	appJS := readStaticAppJS(t)
	for _, required := range []string{
		"Account Balance",
		"Key Limit",
		"credits.balance",
		"credits.keyLimitRemaining",
	} {
		if !strings.Contains(appJS, required) {
			t.Fatalf("app.js does not contain OpenRouter balance integration %q", required)
		}
	}
}
