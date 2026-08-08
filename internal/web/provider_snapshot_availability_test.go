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

// deepSeekBalanceResponse mirrors the DeepSeek current-balance endpoint, where
// `available` is the boolean DeepSeek reports for API usability.
type deepSeekBalanceResponse struct {
	CapturedAt        *string `json:"capturedAt"`
	SnapshotAvailable bool    `json:"snapshotAvailable"`
	Balance           struct {
		SnapshotAvailable bool     `json:"snapshotAvailable"`
		Status            string   `json:"status"`
		Available         *bool    `json:"available"`
		Total             *float64 `json:"total"`
		Granted           *float64 `json:"granted"`
		ToppedUp          *float64 `json:"toppedUp"`
	} `json:"balance"`
}

// moonshotBalanceResponse mirrors the Moonshot current-balance endpoint, where
// `available` is the spendable amount rather than a flag.
type moonshotBalanceResponse struct {
	CapturedAt        *string `json:"capturedAt"`
	SnapshotAvailable bool    `json:"snapshotAvailable"`
	Balance           struct {
		SnapshotAvailable bool     `json:"snapshotAvailable"`
		Status            string   `json:"status"`
		Available         *float64 `json:"available"`
		Voucher           *float64 `json:"voucher"`
		Cash              *float64 `json:"cash"`
	} `json:"balance"`
}

func decodeJSON[T any](t *testing.T, body []byte) T {
	t.Helper()
	var resp T
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode response: %v (%s)", err, body)
	}
	return resp
}

func TestDeepSeekCurrentWithoutSnapshotReportsNoData(t *testing.T) {
	t.Parallel()

	s, err := store.New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })

	h := NewHandler(s, nil, nil, nil, &config.Config{DeepSeekAPIKey: "configured"})
	recorder := httptest.NewRecorder()
	h.Current(recorder, httptest.NewRequest(http.MethodGet, "/api/current?provider=deepseek", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, response = %s", recorder.Code, recorder.Body.String())
	}

	resp := decodeJSON[deepSeekBalanceResponse](t, recorder.Body.Bytes())
	if resp.SnapshotAvailable || resp.Balance.SnapshotAvailable {
		t.Fatal("snapshotAvailable = true, want false without a stored snapshot")
	}
	if resp.CapturedAt != nil {
		t.Fatalf("capturedAt = %q, want null without a stored snapshot", *resp.CapturedAt)
	}
	if resp.Balance.Status != "unknown" {
		t.Fatalf("status = %q, want unknown", resp.Balance.Status)
	}
	if resp.Balance.Available != nil {
		t.Fatalf("available = %v, want null without a stored snapshot", *resp.Balance.Available)
	}
	if resp.Balance.Total != nil || resp.Balance.Granted != nil || resp.Balance.ToppedUp != nil {
		t.Fatalf("balance amounts must be null without a stored snapshot: %s", recorder.Body.String())
	}
}

func TestDeepSeekCurrentWithSnapshotReportsData(t *testing.T) {
	t.Parallel()

	s, err := store.New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })

	if _, err := s.InsertDeepSeekSnapshot(&api.DeepSeekSnapshot{
		CapturedAt:      time.Now().UTC(),
		IsAvailable:     true,
		Currency:        "USD",
		TotalBalance:    18.42,
		GrantedBalance:  3.42,
		ToppedUpBalance: 15,
	}); err != nil {
		t.Fatal(err)
	}

	h := NewHandler(s, nil, nil, nil, &config.Config{DeepSeekAPIKey: "configured"})
	recorder := httptest.NewRecorder()
	h.Current(recorder, httptest.NewRequest(http.MethodGet, "/api/current?provider=deepseek", nil))

	resp := decodeJSON[deepSeekBalanceResponse](t, recorder.Body.Bytes())
	if !resp.SnapshotAvailable || !resp.Balance.SnapshotAvailable {
		t.Fatal("snapshotAvailable = false, want true with a stored snapshot")
	}
	if resp.CapturedAt == nil {
		t.Fatal("capturedAt = null, want the snapshot timestamp")
	}
	if resp.Balance.Total == nil || *resp.Balance.Total != 18.42 {
		t.Fatalf("total = %v, want 18.42", resp.Balance.Total)
	}
	if resp.Balance.Status != "healthy" {
		t.Fatalf("status = %q, want healthy", resp.Balance.Status)
	}
}

// A zero balance from a real poll is exhausted, not "no data": the snapshot
// signal must not swallow a genuine empty account.
func TestDeepSeekCurrentWithZeroSnapshotReportsExhausted(t *testing.T) {
	t.Parallel()

	s, err := store.New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })

	if _, err := s.InsertDeepSeekSnapshot(&api.DeepSeekSnapshot{
		CapturedAt:   time.Now().UTC(),
		IsAvailable:  false,
		Currency:     "USD",
		TotalBalance: 0,
	}); err != nil {
		t.Fatal(err)
	}

	h := NewHandler(s, nil, nil, nil, &config.Config{DeepSeekAPIKey: "configured"})
	recorder := httptest.NewRecorder()
	h.Current(recorder, httptest.NewRequest(http.MethodGet, "/api/current?provider=deepseek", nil))

	resp := decodeJSON[deepSeekBalanceResponse](t, recorder.Body.Bytes())
	if !resp.SnapshotAvailable {
		t.Fatal("snapshotAvailable = false, want true with a stored snapshot")
	}
	if resp.Balance.Status != "exhausted" {
		t.Fatalf("status = %q, want exhausted", resp.Balance.Status)
	}
	if resp.Balance.Total == nil || *resp.Balance.Total != 0 {
		t.Fatalf("total = %v, want 0", resp.Balance.Total)
	}
}

func TestMoonshotCurrentWithoutSnapshotReportsNoData(t *testing.T) {
	t.Parallel()

	s, err := store.New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })

	h := NewHandler(s, nil, nil, nil, &config.Config{MoonshotAPIKey: "configured"})
	recorder := httptest.NewRecorder()
	h.Current(recorder, httptest.NewRequest(http.MethodGet, "/api/current?provider=moonshot", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, response = %s", recorder.Code, recorder.Body.String())
	}

	resp := decodeJSON[moonshotBalanceResponse](t, recorder.Body.Bytes())
	if resp.SnapshotAvailable || resp.Balance.SnapshotAvailable {
		t.Fatal("snapshotAvailable = true, want false without a stored snapshot")
	}
	if resp.CapturedAt != nil {
		t.Fatalf("capturedAt = %q, want null without a stored snapshot", *resp.CapturedAt)
	}
	if resp.Balance.Status != "unknown" {
		t.Fatalf("status = %q, want unknown", resp.Balance.Status)
	}
	if resp.Balance.Available != nil || resp.Balance.Voucher != nil || resp.Balance.Cash != nil {
		t.Fatalf("balance amounts must be null without a stored snapshot: %s", recorder.Body.String())
	}
}

func TestMoonshotCurrentWithSnapshotReportsData(t *testing.T) {
	t.Parallel()

	s, err := store.New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })

	if _, err := s.InsertMoonshotSnapshot(&api.MoonshotSnapshot{
		CapturedAt:       time.Now().UTC(),
		AvailableBalance: 12.5,
		VoucherBalance:   2.5,
		CashBalance:      10,
	}); err != nil {
		t.Fatal(err)
	}

	h := NewHandler(s, nil, nil, nil, &config.Config{MoonshotAPIKey: "configured"})
	recorder := httptest.NewRecorder()
	h.Current(recorder, httptest.NewRequest(http.MethodGet, "/api/current?provider=moonshot", nil))

	resp := decodeJSON[moonshotBalanceResponse](t, recorder.Body.Bytes())
	if !resp.SnapshotAvailable || !resp.Balance.SnapshotAvailable {
		t.Fatal("snapshotAvailable = false, want true with a stored snapshot")
	}
	if resp.Balance.Available == nil || *resp.Balance.Available != 12.5 {
		t.Fatalf("available = %v, want 12.5", resp.Balance.Available)
	}
	if resp.Balance.Status != "healthy" {
		t.Fatalf("status = %q, want healthy", resp.Balance.Status)
	}
}

func TestOpenRouterCurrentWithoutSnapshotReportsNoData(t *testing.T) {
	t.Parallel()

	s, err := store.New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })

	h := NewHandler(s, nil, nil, nil, &config.Config{OpenRouterAPIKey: "configured"})
	recorder := httptest.NewRecorder()
	h.Current(recorder, httptest.NewRequest(http.MethodGet, "/api/current?provider=openrouter", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, response = %s", recorder.Code, recorder.Body.String())
	}

	var resp struct {
		CapturedAt        *string `json:"capturedAt"`
		SnapshotAvailable bool    `json:"snapshotAvailable"`
		Credits           struct {
			SnapshotAvailable bool     `json:"snapshotAvailable"`
			BalanceAvailable  bool     `json:"balanceAvailable"`
			Balance           *float64 `json:"balance"`
			Usage             *float64 `json:"usage"`
			UsageDaily        *float64 `json:"usageDaily"`
			Percent           *float64 `json:"percent"`
		} `json:"credits"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.SnapshotAvailable || resp.Credits.SnapshotAvailable {
		t.Fatal("snapshotAvailable = true, want false without a stored snapshot")
	}
	if resp.CapturedAt != nil {
		t.Fatalf("capturedAt = %q, want null without a stored snapshot", *resp.CapturedAt)
	}
	if resp.Credits.Balance != nil || resp.Credits.Usage != nil || resp.Credits.UsageDaily != nil || resp.Credits.Percent != nil {
		t.Fatalf("credit amounts must be null without a stored snapshot: %s", recorder.Body.String())
	}
}

func TestOpenRouterCurrentWithSnapshotReportsData(t *testing.T) {
	t.Parallel()

	s, err := store.New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })

	balance := 74.75
	if _, err := s.InsertOpenRouterSnapshot(&api.OpenRouterSnapshot{
		CapturedAt:     time.Now().UTC(),
		Usage:          12.5,
		AccountBalance: &balance,
	}); err != nil {
		t.Fatal(err)
	}

	h := NewHandler(s, nil, nil, nil, &config.Config{OpenRouterAPIKey: "configured"})
	recorder := httptest.NewRecorder()
	h.Current(recorder, httptest.NewRequest(http.MethodGet, "/api/current?provider=openrouter", nil))

	var resp struct {
		SnapshotAvailable bool `json:"snapshotAvailable"`
		Credits           struct {
			SnapshotAvailable bool     `json:"snapshotAvailable"`
			Balance           *float64 `json:"balance"`
			Usage             *float64 `json:"usage"`
		} `json:"credits"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if !resp.SnapshotAvailable || !resp.Credits.SnapshotAvailable {
		t.Fatal("snapshotAvailable = false, want true with a stored snapshot")
	}
	if resp.Credits.Balance == nil || *resp.Credits.Balance != balance {
		t.Fatalf("balance = %v, want %v", resp.Credits.Balance, balance)
	}
	if resp.Credits.Usage == nil || *resp.Credits.Usage != 12.5 {
		t.Fatalf("usage = %v, want 12.5", resp.Credits.Usage)
	}
}

// The aggregated "both" payload feeds the homepage cards, so it must carry the
// same snapshot signal as the per-provider endpoints.
func TestCurrentBothCarriesSnapshotAvailability(t *testing.T) {
	t.Parallel()

	s, err := store.New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })

	h := NewHandler(s, nil, nil, nil, &config.Config{
		DeepSeekAPIKey:   "configured",
		MoonshotAPIKey:   "configured",
		OpenRouterAPIKey: "configured",
	})
	recorder := httptest.NewRecorder()
	h.Current(recorder, httptest.NewRequest(http.MethodGet, "/api/current?provider=both", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, response = %s", recorder.Code, recorder.Body.String())
	}

	var resp map[string]struct {
		SnapshotAvailable *bool `json:"snapshotAvailable"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	for _, provider := range []string{"deepseek", "moonshot", "openrouter"} {
		entry, ok := resp[provider]
		if !ok {
			t.Fatalf("both response missing %q: %s", provider, recorder.Body.String())
		}
		if entry.SnapshotAvailable == nil {
			t.Fatalf("%s missing snapshotAvailable: %s", provider, recorder.Body.String())
		}
		if *entry.SnapshotAvailable {
			t.Fatalf("%s snapshotAvailable = true, want false without stored snapshots", provider)
		}
	}
}

func TestAppJSRendersPlaceholderForMissingBalanceSnapshots(t *testing.T) {
	t.Parallel()

	appJS := readStaticAppJS(t)
	for _, required := range []string{
		"payload.snapshotAvailable !== false",
		"hasSnapshot",
		"unknown: { label: 'No data'",
	} {
		if !strings.Contains(appJS, required) {
			t.Fatalf("app.js does not handle missing balance snapshots: missing %q", required)
		}
	}
}

func TestStyleCSSStylesUnknownStatusBadge(t *testing.T) {
	t.Parallel()

	data, err := staticFS.ReadFile("static/style.css")
	if err != nil {
		t.Fatalf("read static/style.css: %v", err)
	}
	if !strings.Contains(string(data), `.status-badge[data-status="unknown"]`) {
		t.Fatal("style.css does not style the unknown status badge")
	}
}
