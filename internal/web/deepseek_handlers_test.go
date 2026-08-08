package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
	"github.com/onllm-dev/onwatch/v2/internal/config"
	"github.com/onllm-dev/onwatch/v2/internal/store"
)

func TestDeepSeekDefaultCurrency(t *testing.T) {
	t.Parallel()

	t.Run("falls back to CNY without a snapshot", func(t *testing.T) {
		t.Parallel()
		if got := (&Handler{}).deepSeekDefaultCurrency(); got != "CNY" {
			t.Fatalf("currency = %q, want CNY", got)
		}
	})

	t.Run("uses the latest snapshot currency", func(t *testing.T) {
		t.Parallel()
		s, err := store.New(":memory:")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = s.Close() })

		_, err = s.InsertDeepSeekSnapshot(&api.DeepSeekSnapshot{
			CapturedAt:   time.Now().UTC(),
			IsAvailable:  true,
			Currency:     "USD",
			TotalBalance: 1.96,
		})
		if err != nil {
			t.Fatal(err)
		}

		if got := (&Handler{store: s}).deepSeekDefaultCurrency(); got != "USD" {
			t.Fatalf("currency = %q, want USD", got)
		}
	})
}

func TestDeepSeekInsightsUsesReportedCurrency(t *testing.T) {
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })

	_, err = s.InsertDeepSeekSnapshot(&api.DeepSeekSnapshot{
		CapturedAt:      time.Now().UTC(),
		IsAvailable:     true,
		Currency:        "USD",
		TotalBalance:    1.96,
		ToppedUpBalance: 1.96,
	})
	if err != nil {
		t.Fatal(err)
	}

	h := NewHandler(s, nil, nil, nil, &config.Config{DeepSeekAPIKey: "configured"})
	recorder := httptest.NewRecorder()
	h.Insights(recorder, httptest.NewRequest(http.MethodGet, "/api/insights?provider=deepseek&range=7d", nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, response = %s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), `"value":"$1.96"`) {
		t.Fatalf("response does not contain USD balance: %s", recorder.Body.String())
	}
}
